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
// With overwrite false it only fills blanks, so creating a rule can complete
// history without disturbing decisions already made.
//
// With overwrite true it recategorizes — which is the point when a category is
// being split out of an existing one, say moving credit card bills off Loan
// Payment onto a new Credit Card category. That deliberately replaces system
// categories too: the family is saying where these belong, and refusing would
// leave them editing the entries by hand, which is what they asked to avoid.
//
// Neither mode touches a hand-written Crew note. Those aren't categories, and
// they're the one thing here a person typed themselves.
func (p *Pipeline) ApplyRuleToHistory(ctx context.Context, familyID uuid.UUID, rule store.CategoryRule, overwrite bool) (int, error) {
	// No lower bound: "all previous transactions" means all of them.
	var candidates []*store.Transaction
	var err error
	if overwrite {
		candidates, err = p.Store.ListSince(ctx, familyID, time.Time{}, ReassessLimit)
	} else {
		candidates, err = p.Store.ListUncategorizedSince(ctx, familyID, time.Time{}, ReassessLimit)
	}
	if err != nil {
		return 0, err
	}

	matched := make([]*store.Transaction, 0, len(candidates))
	for _, t := range candidates {
		if !t.Replaceable() {
			continue
		}
		if rule.CategoryName != "" && t.Note == rule.CategoryName {
			continue // already says what the rule would say
		}
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

	if overwrite {
		// resolveCategory returns early when a category is already set, so
		// recategorizing has to write the rule's answer directly.
		p.runRecategorize(ctx, familyID, matched, rule.CategoryName, "rule-recategorize")
	} else {
		p.runReassess(ctx, familyID, matched, "rule-backfill")
	}
	return len(matched), nil
}

// runRecategorize writes one category over a set of transactions, in the
// background and sequentially, for the same reasons as runReassess.
func (p *Pipeline) runRecategorize(ctx context.Context, familyID uuid.UUID, txns []*store.Transaction, note, reason string) {
	go func() {
		runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Minute)
		defer cancel()
		for _, t := range txns {
			if runCtx.Err() != nil {
				return
			}
			p.queueNote(runCtx, t, note, reason)
		}
		p.Log.Info("recategorized",
			zap.String("reason", reason),
			zap.String("family", familyID.String()),
			zap.String("category", note),
			zap.Int("transactions", len(txns)))
	}()
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
