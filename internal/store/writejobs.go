package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Write job kinds. Each names an action only the connection's lease holder can
// perform, because only that replica owns the Crew client.
const (
	WriteNote           = "note"            // set a transaction's note (its category)
	WriteCardSubaccount = "card_subaccount" // move a debit card to another pocket
)

// WriteJob is a pending mutation against Crew. Any component that wants to
// change something in Crew enqueues here; the lease holder performs it.
type WriteJob struct {
	ID           uuid.UUID
	ConnectionID uuid.UUID
	Kind         string
	TargetID     string // transaction ID, or debit card ID
	Value        string // note text, or subaccount ID
	Attempts     int
}

// EnqueueWrite queues a mutation, superseding any pending write for the same
// target (last request wins).
func (s *Store) EnqueueWrite(ctx context.Context, connID uuid.UUID, kind, targetID, value string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO crew_write_jobs (connection_id, kind, target_id, value)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (connection_id, kind, target_id)
		DO UPDATE SET value = EXCLUDED.value, attempts = 0, last_error = '', run_after = now()`,
		connID, kind, targetID, value)
	if err != nil {
		return err
	}
	// Wake the holder so the write lands in seconds rather than on the next tick.
	_, _ = s.Pool.Exec(ctx, `SELECT pg_notify('crew_write', $1::text)`, connID.String())
	return nil
}

// EnqueueNoteWrite queues a transaction's category (stored as its Crew note).
func (s *Store) EnqueueNoteWrite(ctx context.Context, connID uuid.UUID, crewTxnID, note string) error {
	return s.EnqueueWrite(ctx, connID, WriteNote, crewTxnID, note)
}

// TakeWriteJobs returns due jobs for a connection the caller holds.
func (s *Store) TakeWriteJobs(ctx context.Context, connID uuid.UUID, limit int) ([]WriteJob, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, connection_id, kind, target_id, value, attempts
		FROM crew_write_jobs
		WHERE connection_id = $1 AND run_after <= now()
		ORDER BY run_after
		LIMIT $2`, connID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WriteJob
	for rows.Next() {
		var j WriteJob
		if err := rows.Scan(&j.ID, &j.ConnectionID, &j.Kind, &j.TargetID, &j.Value, &j.Attempts); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *Store) DeleteWriteJob(ctx context.Context, id uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM crew_write_jobs WHERE id = $1`, id)
	return err
}

// FailWriteJob records an attempt and backs the job off exponentially, giving
// up (deleting) after maxAttempts so a permanently rejected write can't spin.
// It reports whether the job was abandoned, since that silently undoes a change
// the user already saw applied and is worth logging.
func (s *Store) FailWriteJob(ctx context.Context, id uuid.UUID, attempts int, cause string, maxAttempts int) (bool, error) {
	if attempts+1 >= maxAttempts {
		_, err := s.Pool.Exec(ctx, `DELETE FROM crew_write_jobs WHERE id = $1`, id)
		return true, err
	}
	backoff := time.Duration(1<<uint(attempts)) * time.Minute
	if backoff > time.Hour {
		backoff = time.Hour
	}
	_, err := s.Pool.Exec(ctx, `
		UPDATE crew_write_jobs
		SET attempts = attempts + 1, last_error = $2, run_after = now() + $3
		WHERE id = $1`, id, truncate(cause, 500), backoff)
	return false, err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
