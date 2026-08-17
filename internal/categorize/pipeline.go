// Package categorize turns freshly ingested transactions into categorized,
// notified ones. The category is stored in Crew's per-transaction note field,
// so the pipeline decides on a category and enqueues a note write; the replica
// holding that connection's lease performs it.
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

// outcome records how a transaction got its category, which decides whether a
// push is worth sending.
type outcome struct {
	category string
	// silent is set when a rule decided the category. A rule match is an
	// outcome the family already asked for, so there's nothing to review.
	silent bool
}

func (p *Pipeline) process(ctx context.Context, item Item) {
	t, err := p.Store.GetTransactionByID(ctx, item.TxnID)
	if err != nil {
		p.Log.Warn("pipeline load txn", zap.Error(err))
		return
	}

	res := p.resolveCategory(ctx, t)

	// Recurring / subscription detection (spends only).
	if t.MerchantKey != "" {
		if seriesID, err := UpdateRecurring(ctx, p.Store, t.FamilyID, t.MerchantKey, t.AmountCents, t.OccurredAt); err != nil {
			p.Log.Warn("pipeline recurring", zap.Error(err))
		} else if seriesID != uuid.Nil {
			_ = p.Store.SetTransactionRecurring(ctx, t.ID, seriesID)
		}
	}

	// Push, exactly once across all replicas. A rule-assigned category is
	// deliberately silent; an LLM guess or an unrecognized merchant is not,
	// since both are things the family may want to correct.
	if !item.Notify || res.silent || time.Since(t.OccurredAt) > notifyWindow {
		_, _ = p.Store.ClaimNotification(ctx, t.ID) // absorb silently
		return
	}
	claimed, err := p.Store.ClaimNotification(ctx, t.ID)
	if err != nil || !claimed {
		return
	}
	p.Push.SendToFamily(ctx, t.FamilyID, buildNotification(t, res.category))
}

// resolveCategory decides this transaction's category and queues the note
// write when one is needed.
//
// Order of precedence:
//  1. a category (or hand-written note) already in Crew — never overwritten
//  2. the family's rules, including labels applied to a recurring series
//  3. what this merchant was categorized as last time
//  4. the LLM
//
// Rules deliberately run ahead of the LLM: they're free, deterministic, and
// they're the family telling us the answer.
func (p *Pipeline) resolveCategory(ctx context.Context, t *store.Transaction) outcome {
	if t.CategoryName != nil {
		return outcome{category: *t.CategoryName, silent: true} // already set in Crew
	}
	if t.Note != "" {
		return outcome{} // user's own note — leave it, ask them to categorize
	}

	if cat, ok := p.applyRules(ctx, t); ok {
		p.queueNote(ctx, t, cat, "rule")
		return outcome{category: cat, silent: true}
	}

	// Prior transactions for this merchant are the merchant→category cache;
	// this also picks up categories set directly in the Crew app.
	if name, ok, err := p.Store.SuggestCategoryForMerchant(ctx, t.FamilyID, t.MerchantKey); err != nil {
		p.Log.Warn("merchant suggestion", zap.Error(err))
	} else if ok {
		p.queueNote(ctx, t, name, "history")
		return outcome{category: name}
	}

	if !p.LLM.Enabled {
		return outcome{}
	}
	cats, err := p.Store.ListCategories(ctx, t.FamilyID)
	if err != nil || len(cats) == 0 {
		return outcome{}
	}
	names := make([]string, len(cats))
	for i, c := range cats {
		names[i] = c.Name
	}
	name, ok := p.LLM.Categorize(ctx, t.Payee, t.MCC, t.AmountCents, names)
	if !ok {
		return outcome{}
	}
	// Normalize to the family's exact spelling so the note matches on read.
	cat, err := p.Store.GetCategoryByName(ctx, t.FamilyID, name)
	if err != nil || cat == nil {
		return outcome{}
	}
	p.queueNote(ctx, t, cat.Name, "llm")
	return outcome{category: cat.Name}
}

// applyRules evaluates the family's rules in priority order.
func (p *Pipeline) applyRules(ctx context.Context, t *store.Transaction) (string, bool) {
	rules, err := p.Store.ListEnabledRules(ctx, t.FamilyID)
	if err != nil {
		p.Log.Warn("load rules", zap.Error(err))
		return "", false
	}
	m := FirstMatch(rules, Candidate{
		Payee:       t.Payee,
		MerchantKey: t.MerchantKey,
		MCC:         t.MCC,
		AmountCents: t.AmountCents,
	})
	if m == nil {
		return "", false
	}
	return m.CategoryName, true
}

func (p *Pipeline) queueNote(ctx context.Context, t *store.Transaction, note, source string) {
	if err := p.Store.EnqueueNoteWrite(ctx, t.ConnectionID, t.CrewTxnID, note); err != nil {
		p.Log.Warn("enqueue note write", zap.Error(err))
		return
	}
	p.Log.Info("categorized",
		zap.String("merchant", t.MerchantKey),
		zap.String("category", note),
		zap.String("source", source))
}

func buildNotification(t *store.Transaction, category string) push.Notification {
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
	if category != "" {
		return push.Notification{
			Title: "New transaction",
			Body:  fmt.Sprintf("%s from %s, auto-categorized as %s", amountStr, payee, category),
			URL:   url,
		}
	}
	return push.Notification{
		Title: "New transaction found",
		Body:  fmt.Sprintf("%s from %s — tap to categorize", amountStr, payee),
		URL:   url,
	}
}
