package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// WriteJob is a pending note write against Crew. Only the replica holding a
// connection's lease owns its Crew client, so any component that wants to set
// a note enqueues here and the holder performs the write.
type WriteJob struct {
	ID           uuid.UUID
	ConnectionID uuid.UUID
	CrewTxnID    string
	Note         string
	Attempts     int
}

// EnqueueNoteWrite queues a note write, superseding any pending write for the
// same transaction (last request wins).
func (s *Store) EnqueueNoteWrite(ctx context.Context, connID uuid.UUID, crewTxnID, note string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO crew_write_jobs (connection_id, crew_txn_id, note)
		VALUES ($1, $2, $3)
		ON CONFLICT (connection_id, crew_txn_id)
		DO UPDATE SET note = EXCLUDED.note, attempts = 0, last_error = '', run_after = now()`,
		connID, crewTxnID, note)
	if err != nil {
		return err
	}
	// Wake the holder so the write lands in seconds rather than on the next tick.
	_, _ = s.Pool.Exec(ctx, `SELECT pg_notify('crew_write', $1::text)`, connID.String())
	return nil
}

// TakeWriteJobs returns due jobs for a connection the caller holds.
func (s *Store) TakeWriteJobs(ctx context.Context, connID uuid.UUID, limit int) ([]WriteJob, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, connection_id, crew_txn_id, note, attempts
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
		if err := rows.Scan(&j.ID, &j.ConnectionID, &j.CrewTxnID, &j.Note, &j.Attempts); err != nil {
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
func (s *Store) FailWriteJob(ctx context.Context, id uuid.UUID, attempts int, cause string, maxAttempts int) error {
	if attempts+1 >= maxAttempts {
		_, err := s.Pool.Exec(ctx, `DELETE FROM crew_write_jobs WHERE id = $1`, id)
		return err
	}
	backoff := time.Duration(1<<uint(attempts)) * time.Minute
	if backoff > time.Hour {
		backoff = time.Hour
	}
	_, err := s.Pool.Exec(ctx, `
		UPDATE crew_write_jobs
		SET attempts = attempts + 1, last_error = $2, run_after = now() + $3
		WHERE id = $1`, id, truncate(cause, 500), backoff)
	return err
}

// PendingWriteNotes returns the notes queued for a set of transactions, so the
// UI can show a category optimistically while the Crew write is in flight.
func (s *Store) PendingWriteNotes(ctx context.Context, connIDs []uuid.UUID, crewTxnIDs []string) (map[string]string, error) {
	if len(crewTxnIDs) == 0 {
		return nil, nil
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT crew_txn_id, note FROM crew_write_jobs
		WHERE connection_id = ANY($1) AND crew_txn_id = ANY($2)`, connIDs, crewTxnIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var id, note string
		if err := rows.Scan(&id, &note); err != nil {
			return nil, err
		}
		out[id] = note
	}
	return out, rows.Err()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
