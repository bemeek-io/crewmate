package watcher

import (
	"context"
	"errors"

	crew "github.com/bemeek-io/go-crew"
	"go.uber.org/zap"
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
		_, err := client.UpdateCashTransaction(ctx, crew.UpdateCashTransactionInput{
			CashTransactionID: j.CrewTxnID,
			Note:              j.Note,
		})
		if err != nil {
			// An auth failure is the session's problem, not this job's: leave
			// the job queued and let the session loop handle re-login.
			if errors.Is(err, crew.ErrUnauthorized) {
				r.setLastErr(err)
				return
			}
			log.Warn("crew note write failed",
				zap.String("crew_txn", j.CrewTxnID), zap.Int("attempts", j.Attempts), zap.Error(err))
			if ferr := r.Store.FailWriteJob(ctx, j.ID, j.Attempts, err.Error(), maxWriteAttempts); ferr != nil {
				log.Warn("record write failure", zap.Error(ferr))
			}
			continue
		}
		// Mirror into the local cache so the UI reflects it before the next poll.
		if err := r.Store.SetLocalNote(ctx, r.Conn.ID, j.CrewTxnID, j.Note); err != nil {
			log.Warn("mirror note locally", zap.Error(err))
		}
		if err := r.Store.DeleteWriteJob(ctx, j.ID); err != nil {
			log.Warn("delete write job", zap.Error(err))
		}
	}
}
