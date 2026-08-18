package watcher

import (
	"context"
	"errors"

	crew "github.com/bemeek-io/go-crew"
	"go.uber.org/zap"

	"github.com/bemeek-io/crewmate/internal/crewcards"
	"github.com/bemeek-io/crewmate/internal/store"
)

const (
	// writeBatchSize paces note writes against Crew's undocumented rate
	// limits: at most this many mutations per drain.
	writeBatchSize = 10
	// maxWriteAttempts before a note write is abandoned.
	maxWriteAttempts = 5
)

// drainWriteJobs performs queued note writes for this connection. Only the
// lease holder runs it, because only the holder owns the Crew client — that is
// why writes are queued in Postgres rather than issued by the HTTP handler.
func (r *Runner) drainWriteJobs(ctx context.Context, client *crew.Client, log *zap.Logger) {
	jobs, err := r.Store.TakeWriteJobs(ctx, r.Conn.ID, writeBatchSize)
	if err != nil {
		log.Warn("take write jobs", zap.Error(err))
		return
	}
	for _, j := range jobs {
		if ctx.Err() != nil {
			return
		}
		var err error
		switch j.Kind {
		case store.WriteCardSubaccount:
			err = crewcards.MovePocket(ctx, client, j.Value)
		default:
			_, err = client.UpdateCashTransaction(ctx, crew.UpdateCashTransactionInput{
				CashTransactionID: j.TargetID,
				Note:              j.Value,
			})
		}
		if err != nil {
			// An auth failure is the session's problem, not this job's: leave
			// the job queued and let the session loop handle re-login.
			if errors.Is(err, crew.ErrUnauthorized) {
				r.setLastErr(err)
				return
			}
			log.Warn("crew write failed", zap.String("kind", j.Kind),
				zap.String("target", j.TargetID), zap.Int("attempts", j.Attempts), zap.Error(err))
			if ferr := r.Store.FailWriteJob(ctx, j.ID, j.Attempts, err.Error(), maxWriteAttempts); ferr != nil {
				log.Warn("record write failure", zap.Error(ferr))
			}
			continue
		}
		// Mirror into the local cache so the UI reflects it before the next poll.
		if j.Kind == store.WriteNote {
			if err := r.Store.SetLocalNote(ctx, r.Conn.FamilyID, j.TargetID, j.Value); err != nil {
				log.Warn("mirror note locally", zap.Error(err))
			}
		} else {
			// A card move changes the snapshot, not a transaction row.
			r.RequestRefresh()
		}
		if err := r.Store.DeleteWriteJob(ctx, j.ID); err != nil {
			log.Warn("delete write job", zap.Error(err))
		}
	}
}
