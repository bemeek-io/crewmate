package watcher

import (
	"context"
	"time"

	crew "github.com/bemeek-io/go-crew"
	"go.uber.org/zap"
)

// reconcile catches up on transactions that arrived while no replica held this
// connection. The SDK watcher's dedupe is in-memory only and its baseline poll
// silently absorbs whatever exists at start, so this runs on EVERY lease
// acquisition, before StartWatching.
//
// Strategy: page newest-first through CashTransactions into the idempotent
// ingest. Stop once a full page produced zero fresh inserts AND its oldest
// transaction predates the catch-up horizon (last_polled_at - overlap).
// First-ever acquisition back-fills BackfillMonths of history silently.
func (r *Runner) reconcile(ctx context.Context, client *crew.Client, log *zap.Logger) error {
	firstRun := r.Conn.LastPolledAt == nil
	var horizon time.Time
	if firstRun {
		horizon = time.Now().AddDate(0, -r.BackfillMonths, 0)
	} else {
		horizon = r.Conn.LastPolledAt.Add(-reconcileOverlap)
	}

	after := ""
	pages, inserted := 0, 0
	for {
		page, err := client.CashTransactions(ctx, crew.CashTransactionsOptions{First: 50, After: after})
		if err != nil {
			return err
		}
		pages++
		freshInPage := 0
		var oldest time.Time
		for _, tx := range page.Transactions {
			oldest = tx.OccurredAt
			if tx.OccurredAt.Before(horizon) {
				continue
			}
			// Notify only for recent arrivals; process() additionally
			// suppresses anything older than the notify window, so a first-run
			// backfill never produces a notification storm.
			if r.ingest(ctx, tx, !firstRun, log) {
				freshInPage++
			}
		}
		inserted += freshInPage

		done := !page.PageInfo.HasNextPage ||
			(freshInPage == 0 && !oldest.IsZero() && oldest.Before(horizon)) ||
			(!firstRun && !oldest.IsZero() && oldest.Before(horizon))
		if done {
			break
		}
		after = page.PageInfo.EndCursor
		if pages > 400 { // hard stop: 20k transactions
			log.Warn("reconcile page cap reached")
			break
		}
	}
	if inserted > 0 || firstRun {
		log.Info("reconcile complete", zap.Int("pages", pages), zap.Int("ingested", inserted), zap.Bool("first_run", firstRun))
	}
	return nil
}
