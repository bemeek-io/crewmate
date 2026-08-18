package watcher

import (
	"context"
	"time"

	crew "github.com/bemeek-io/go-crew"
	"go.uber.org/zap"

	"github.com/bemeek-io/crewmate/internal/categorize"
)

const (
	// noteSyncInterval: how often to pull recent transactions purely to pick
	// up note edits. Kept well above the poll interval because hand-edited
	// notes are rare and each sync is an extra Crew call.
	noteSyncInterval = 5 * time.Minute
	// noteSyncPageSize: how far back a sync looks.
	noteSyncPageSize = 100
)

// syncNotes reconciles Crew's note field into the local cache.
//
// This exists because the SDK watcher only fires OnTransactionUpdate when a
// transaction's status or amount changes — editing a note in the Crew app
// changes neither, so without this a category typed directly into Crew would
// never reach crewmate.
func (r *Runner) syncNotes(ctx context.Context, client *crew.Client, log *zap.Logger) {
	page, err := client.CashTransactions(ctx, crew.CashTransactionsOptions{First: noteSyncPageSize})
	if err != nil {
		r.setLastErr(err)
		log.Warn("note sync fetch failed", zap.Error(err))
		return
	}
	changed := 0
	for _, tx := range page.Transactions {
		updated, err := r.Store.SyncNoteFromCrew(ctx, r.Conn.ID, tx.ID, tx.Note)
		if err != nil {
			log.Warn("note sync write", zap.Error(err))
			continue
		}
		if !updated {
			continue
		}
		changed++
		// A note may have just become a category (or stopped being one), so
		// let the pipeline re-evaluate without re-notifying.
		if t, err := r.Store.GetTransactionByCrewID(ctx, r.Conn.FamilyID, tx.ID); err == nil {
			r.Pipeline.Enqueue(categorize.Item{TxnID: t.ID, Notify: false})
		}
	}
	if changed > 0 {
		log.Info("synced notes from crew", zap.Int("changed", changed))
	}
}
