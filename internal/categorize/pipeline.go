// Package categorize turns freshly ingested transactions into categorized,
// notified ones: merchant rule -> recurring detection -> LLM fallback -> push.
package categorize

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bemeek-io/crewmate/internal/push"
	"github.com/bemeek-io/crewmate/internal/store"
)

// notifyWindow: transactions older than this (reconciliation backfills) are
// ingested silently instead of producing a burst of stale notifications.
const notifyWindow = 24 * time.Hour

type Item struct {
	TxnID  uuid.UUID
	Notify bool
}

type Pipeline struct {
	Store *store.Store
	LLM   *LLM
	Push  push.Sender
	Log   *zap.Logger

	queue chan Item
}

func NewPipeline(st *store.Store, llm *LLM, sender push.Sender, log *zap.Logger) *Pipeline {
	return &Pipeline{Store: st, LLM: llm, Push: sender, Log: log, queue: make(chan Item, 1024)}
}

// Enqueue never blocks — it runs on the watcher's poll goroutine. A full queue
// drops the item; the periodic sweep re-picks anything unnotified.
func (p *Pipeline) Enqueue(item Item) {
	select {
	case p.queue <- item:
	default:
		p.Log.Warn("categorize queue full, dropping (sweep will retry)", zap.String("txn", item.TxnID.String()))
	}
}

// Start launches the worker pool plus the safety-net sweeper.
func (p *Pipeline) Start(ctx context.Context, workers int) {
	for i := 0; i < workers; i++ {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case item := <-p.queue:
					p.process(ctx, item)
				}
			}
		}()
	}
	go p.sweepLoop(ctx)
}

// sweepLoop re-enqueues ingested-but-never-notified transactions (crash
// between ingest and processing, or queue overflow). ClaimNotification makes
// concurrent sweeps across replicas harmless.
func (p *Pipeline) sweepLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(5*time.Minute + time.Duration(rand.Intn(60))*time.Second):
		}
		ids, err := p.Store.SweepUnnotified(ctx, 100)
		if err != nil {
			p.Log.Warn("sweep unnotified", zap.Error(err))
			continue
		}
		for _, id := range ids {
			p.Enqueue(Item{TxnID: id, Notify: true})
		}
	}
}

func (p *Pipeline) process(ctx context.Context, item Item) {
	t, err := p.Store.GetTransactionByID(ctx, item.TxnID)
	if err != nil {
		p.Log.Warn("pipeline load txn", zap.Error(err))
		return
	}
	if t.NotifiedAt != nil && t.CategoryID != nil {
		return // fully processed
	}

	// 1. Categorize: existing merchant rule wins; LLM fills gaps and caches
	//    its answer as a rule so each merchant costs at most one API call.
	if t.CategoryID == nil && t.MerchantKey != "" {
		rule, err := p.Store.GetMerchantRule(ctx, t.FamilyID, t.MerchantKey)
		if err != nil {
			p.Log.Warn("pipeline rule lookup", zap.Error(err))
		}
		switch {
		case rule != nil:
			if err := p.Store.SetTransactionCategory(ctx, t.FamilyID, t.ID, &rule.CategoryID, "rule"); err == nil {
				t.CategoryID, t.CategoryName = &rule.CategoryID, &rule.CategoryName
				t.CategorySource = "rule"
			}
		case p.LLM.Enabled:
			p.categorizeWithLLM(ctx, t)
		}
	}

	// 2. Recurring / subscription detection (spends only).
	if t.MerchantKey != "" {
		if seriesID, err := UpdateRecurring(ctx, p.Store, t.FamilyID, t.MerchantKey, t.AmountCents, t.OccurredAt); err != nil {
			p.Log.Warn("pipeline recurring", zap.Error(err))
		} else if seriesID != uuid.Nil {
			_ = p.Store.SetTransactionRecurring(ctx, t.ID, seriesID)
		}
	}

	// 3. Push, exactly once across all replicas.
	if !item.Notify || time.Since(t.OccurredAt) > notifyWindow {
		// Silently absorb old backfills: claim without sending.
		_, _ = p.Store.ClaimNotification(ctx, t.ID)
		return
	}
	claimed, err := p.Store.ClaimNotification(ctx, t.ID)
	if err != nil || !claimed {
		return
	}
	p.Push.SendToFamily(ctx, t.FamilyID, p.buildNotification(t))
}

func (p *Pipeline) categorizeWithLLM(ctx context.Context, t *store.Transaction) {
	cats, err := p.Store.ListCategories(ctx, t.FamilyID)
	if err != nil || len(cats) == 0 {
		return
	}
	names := make([]string, len(cats))
	for i, c := range cats {
		names[i] = c.Name
	}
	name, ok := p.LLM.Categorize(ctx, t.Payee, t.MCC, t.AmountCents, names)
	if !ok {
		return
	}
	cat, err := p.Store.GetCategoryByName(ctx, t.FamilyID, name)
	if err != nil || cat == nil {
		return
	}
	if err := p.Store.SetTransactionCategory(ctx, t.FamilyID, t.ID, &cat.ID, "llm"); err != nil {
		return
	}
	t.CategoryID, t.CategoryName = &cat.ID, &cat.Name
	t.CategorySource = "llm"
	// Cache so this merchant never hits the LLM again for this family.
	if err := p.Store.UpsertMerchantRule(ctx, t.FamilyID, t.MerchantKey, cat.ID, "llm", "high"); err != nil {
		p.Log.Warn("cache llm rule", zap.Error(err))
	}
}

func (p *Pipeline) buildNotification(t *store.Transaction) push.Notification {
	amount := float64(t.AmountCents) / 100
	amountStr := fmt.Sprintf("$%.2f", amount)
	if amount < 0 {
		amountStr = fmt.Sprintf("$%.2f", -amount)
	} else {
		amountStr = "+" + amountStr
	}
	url := "/transactions/" + t.ID.String()
	payee := t.Payee
	if payee == "" {
		payee = "your account"
	}
	if t.CategoryName != nil {
		return push.Notification{
			Title: "New transaction",
			Body:  fmt.Sprintf("%s from %s, auto-categorized as %s", amountStr, payee, *t.CategoryName),
			URL:   url,
		}
	}
	return push.Notification{
		Title: "New transaction found",
		Body:  fmt.Sprintf("%s from %s — tap to categorize", amountStr, payee),
		URL:   url,
	}
}
