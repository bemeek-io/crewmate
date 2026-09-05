package watcher

import (
	"context"
	"time"

	crew "github.com/bemeek-io/go-crew"
	"go.uber.org/zap"

	"github.com/bemeek-io/crewmate/internal/store"
)

const (
	// vanishGrace: how long a pending transaction must have been stored before
	// its absence from Crew counts as evidence. It covers the window between
	// fetching a page and reading the database, in which a genuinely new
	// transaction would be missing from the former but present in the latter.
	vanishGrace = time.Hour
	// vanishPageCap bounds how far past the first page a deep sweep will chase a
	// buried pending transaction.
	vanishPageCap = 20
)

// sweepVanished deletes pending transactions Crew has stopped reporting.
//
// A pending authorization can be cancelled rather than settled — a card's
// six-cent verification charge, a hold the merchant drops — and Crew then drops
// it from the list entirely. Nothing in the watch stream says so: there is no
// update event for a transaction that no longer exists. Left alone the row sits
// in Activity forever as uncategorized, and it cannot even be categorized away,
// because the note write behind a category targets a Crew ID that is gone.
//
// The sweep reasons from coverage: a contiguous newest-first walk of Crew's list
// vouches for every transaction it reaches, so a stored one inside that window
// and absent from the walk is gone. Only pending rows are swept — a cleared
// transaction missing from a page is far more likely to mean the page is wrong
// than that the money moved back.
//
// deep allows walking past the first page to reach a pending transaction newer
// activity has buried. Only the sweep at session start does: a charge is
// cancelled within a day or so of being authorized, long before a hundred
// transactions bury it, so the recurring sweeps would pay for that walk on every
// tick and almost never find anything. What they would find, they find on the
// first page.
func (r *Runner) sweepVanished(ctx context.Context, client *crew.Client, first *crew.CashTransactionPage, deep bool, log *zap.Logger) {
	floor, ok := pageFloor(first.Transactions, first.PageInfo.HasNextPage)
	if !ok {
		return // an empty page vouches for nothing
	}
	// Nothing old enough to judge means no sweep, and no paging — which is the
	// normal case, so it costs one indexed query.
	oldest, stale, err := r.Store.OldestPendingForConnection(ctx, r.Conn.ID, vanishGrace)
	if err != nil {
		log.Warn("oldest pending lookup", zap.Error(err))
		return
	}
	if !stale {
		return
	}

	seen := crewIDs(first.Transactions)
	cursor, more := first.PageInfo.EndCursor, first.PageInfo.HasNextPage
	// Keep walking while something stored sits below what the walk has reached.
	for pages := 0; deep && more && !floor.IsZero() && oldest.Before(floor) && pages < vanishPageCap; pages++ {
		page, err := client.CashTransactions(ctx, crew.CashTransactionsOptions{First: recentPageSize, After: cursor})
		if err != nil {
			// A walk with a hole in it cannot vouch for anything below the hole,
			// and the part above it will be swept on the next pass anyway.
			log.Warn("vanish sweep paging failed", zap.Error(err))
			return
		}
		for _, tx := range page.Transactions {
			seen[tx.ID] = struct{}{}
		}
		cursor, more = page.PageInfo.EndCursor, page.PageInfo.HasNextPage
		// An empty page leaves the floor where it was: it adds no coverage, and
		// the previous floor already describes how deep the walk reached.
		if f, ok := pageFloor(page.Transactions, more); ok {
			floor = f
		}
	}

	local, err := r.Store.PendingForConnection(ctx, r.Conn.ID, floor, vanishGrace)
	if err != nil {
		log.Warn("load pending for vanish sweep", zap.Error(err))
		return
	}
	for _, t := range vanished(seen, local, floor) {
		if err := r.Store.DeleteVanishedTransaction(ctx, t.ID, r.Conn.ID, t.CrewTxnID); err != nil {
			log.Warn("delete vanished txn", zap.Error(err), zap.String("crew_txn", t.CrewTxnID))
			continue
		}
		log.Info("dropped a pending transaction crew no longer reports",
			zap.String("crew_txn", t.CrewTxnID), zap.String("payee", t.Payee),
			zap.Int64("amount_cents", t.AmountCents))
	}
}

// crewIDs is the set of transaction IDs a page reported.
func crewIDs(page []crew.CashTransaction) map[string]struct{} {
	seen := make(map[string]struct{}, len(page))
	for _, tx := range page {
		seen[tx.ID] = struct{}{}
	}
	return seen
}

// pageFloor is the oldest occurrence time a page of Crew transactions vouches
// for, and whether it vouches for anything at all.
//
// A page only speaks for the window it covers: older than its oldest entry
// means the page ran out, not that the transaction is gone. Only the end of the
// list speaks for the rest of history — reported as the zero time, which is
// before everything.
//
// hasNextPage, not the page's length, is what says the list ended. A short page
// is not proof: Crew is free to return fewer rows than `first` asked for and
// still report more behind them, and reading that as the end of history would
// let a single truncated response condemn every stored pending transaction the
// page happens not to mention.
func pageFloor(page []crew.CashTransaction, hasNextPage bool) (time.Time, bool) {
	if len(page) == 0 {
		return time.Time{}, false
	}
	if !hasNextPage {
		return time.Time{}, true
	}
	oldest := page[0].OccurredAt
	for _, tx := range page[1:] {
		if tx.OccurredAt.Before(oldest) {
			oldest = tx.OccurredAt
		}
	}
	return oldest, true
}

// vanished picks out the local transactions that fall inside the walked window
// but were never seen in it.
func vanished(seen map[string]struct{}, local []*store.Transaction, floor time.Time) []*store.Transaction {
	var out []*store.Transaction
	for _, t := range local {
		if t.OccurredAt.Before(floor) {
			continue // outside what the walk can speak for
		}
		if _, ok := seen[t.CrewTxnID]; ok {
			continue
		}
		out = append(out, t)
	}
	return out
}
