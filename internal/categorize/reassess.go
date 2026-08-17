package categorize

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bemeek-io/crewmate/internal/store"
)

// ReassessLimit caps one run. Each transaction can cost an LLM call, so this
// bounds both spend and how long the Crew write queue is backed up.
const ReassessLimit = 500

// Reassess re-runs categorization over transactions in the window that were
// never categorized — the point being to pick up categories added since.
//
// It reuses resolveCategory rather than reimplementing it, which is what makes
// the guarantees hold:
//
//   - a transaction already carrying a category is returned before anything
//     else runs, so a rule's answer, a Subscription/Loan Payment label, and a
//     manual choice are all untouchable;
//   - a hand-written note is likewise left alone;
//   - rules are evaluated ahead of the LLM, so a rule added later wins here
//     too rather than being second-guessed;
//   - the LLM is only ever offered non-system categories.
//
// Only candidates with an empty note are loaded, so in practice this fills in
// blanks. Notifications are off: these are old transactions the family has
// already seen, and a burst of pushes for historical spend is not a feature.
//
// Returns the number of candidates it will work through. The work itself
// happens in the background — an LLM call per transaction is far too slow to
// hold a request open for.
func (p *Pipeline) Reassess(ctx context.Context, familyID uuid.UUID, since time.Time) (int, error) {
	candidates, err := p.Store.ListUncategorizedSince(ctx, familyID, since, ReassessLimit)
	if err != nil {
		return 0, err
	}
	if len(candidates) == 0 {
		return 0, nil
	}
	p.runReassess(ctx, familyID, candidates, "reassess")
	return len(candidates), nil
}

// ApplyRuleToHistory categorizes past transactions that the given rule matches.
//
// Same safety as Reassess — only uncategorized transactions are considered, so
// creating a rule can fill in history without rewriting decisions already made.
// The rule is matched here to pick candidates, then the pipeline applies it, so
// there is one implementation of what a rule means.
func (p *Pipeline) ApplyRuleToHistory(ctx context.Context, familyID uuid.UUID, rule store.CategoryRule) (int, error) {
	// No lower bound: "all previous transactions" means all of them.
	candidates, err := p.Store.ListUncategorizedSince(ctx, familyID, time.Time{}, ReassessLimit)
	if err != nil {
		return 0, err
	}
	matched := make([]*store.Transaction, 0, len(candidates))
	for _, t := range candidates {
		if MatchRule(rule, Candidate{
			Payee:       t.Payee,
			MerchantKey: t.MerchantKey,
			MCC:         t.MCC,
			AmountCents: t.AmountCents,
		}) {
			matched = append(matched, t)
		}
	}
	if len(matched) == 0 {
		return 0, nil
	}
	p.runReassess(ctx, familyID, matched, "rule-backfill")
	return len(matched), nil
}

// runReassess works through candidates in the background, one at a time.
//
// Sequential on purpose: this shares the LLM and the Crew write queue with
// live ingestion, and a few hundred historical transactions finishing a minute
// later costs nothing, whereas starving the live path would be felt.
func (p *Pipeline) runReassess(ctx context.Context, familyID uuid.UUID, candidates []*store.Transaction, reason string) {
	// Detached from the request that triggered it; bounded so a stuck run
	// can't linger indefinitely.
	go func() {
		runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Minute)
		defer cancel()

		start := time.Now()
		for _, t := range candidates {
			if runCtx.Err() != nil {
				return
			}
			p.process(runCtx, Item{TxnID: t.ID, Notify: false})
		}
		p.Log.Info("re-assessment finished",
			zap.String("reason", reason),
			zap.String("family", familyID.String()),
			zap.Int("transactions", len(candidates)),
			zap.Duration("took", time.Since(start)))
	}()
}
