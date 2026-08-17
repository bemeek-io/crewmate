package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type ConnStatus string

const (
	ConnActive       ConnStatus = "active"
	ConnNeedsRelogin ConnStatus = "needs_relogin"
	ConnDisabled     ConnStatus = "disabled"
)

type CrewConnection struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	FamilyID        uuid.UUID // resolved via family_members; uuid.Nil when the user has no family yet
	TokenCiphertext []byte
	Status          ConnStatus
	LastRotatedAt   *time.Time
	LastPolledAt    *time.Time
	LeaseEpoch      int64
}

// Lease identifies a replica's claim over a connection. Every holder-side
// write is fenced on (holder, epoch) so a stale replica can never win.
type Lease struct {
	ConnID uuid.UUID
	Holder string
	Epoch  int64
}

const connCols = `
	c.id, c.user_id, COALESCE(m.family_id, '00000000-0000-0000-0000-000000000000'::uuid),
	c.token_ciphertext, c.status, c.last_rotated_at, c.last_polled_at, c.lease_epoch`

func scanConn(row pgx.Row) (*CrewConnection, error) {
	var c CrewConnection
	if err := row.Scan(&c.ID, &c.UserID, &c.FamilyID, &c.TokenCiphertext, &c.Status,
		&c.LastRotatedAt, &c.LastPolledAt, &c.LeaseEpoch); err != nil {
		return nil, err
	}
	return &c, nil
}

// UpsertConnection stores a freshly authenticated Crew token for a user and
// releases any existing lease so the next scheduler tick reloads it.
func (s *Store) UpsertConnection(ctx context.Context, userID uuid.UUID, tokenCiphertext func(connID uuid.UUID) ([]byte, error)) (uuid.UUID, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)

	var connID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM crew_connections WHERE user_id = $1`, userID).Scan(&connID)
	if err == pgx.ErrNoRows {
		// Insert a placeholder ciphertext first so the row ID exists for AAD binding.
		if err := tx.QueryRow(ctx, `
			INSERT INTO crew_connections (user_id, token_ciphertext) VALUES ($1, ''::bytea)
			RETURNING id`, userID).Scan(&connID); err != nil {
			return uuid.Nil, err
		}
	} else if err != nil {
		return uuid.Nil, err
	}

	ct, err := tokenCiphertext(connID)
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE crew_connections
		SET token_ciphertext = $2, status = 'active', last_rotated_at = now(),
		    lease_holder = NULL, lease_expires_at = NULL, updated_at = now()
		WHERE id = $1`, connID, ct); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return connID, nil
}

func (s *Store) GetConnectionByUser(ctx context.Context, userID uuid.UUID) (*CrewConnection, error) {
	c, err := scanConn(s.Pool.QueryRow(ctx, `
		SELECT `+connCols+` FROM crew_connections c
		LEFT JOIN family_members m ON m.user_id = c.user_id
		WHERE c.user_id = $1`, userID))
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return c, err
}

// DisableConnection stops tracking for a user: wipes the stored token and
// flips status. The holding runner notices within one poll tick and exits.
// The row (and its transactions) are kept — history survives a disconnect.
func (s *Store) DisableConnection(ctx context.Context, userID uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE crew_connections
		SET status = 'disabled', token_ciphertext = ''::bytea, updated_at = now()
		WHERE user_id = $1`, userID)
	return err
}

// NotifyRefresh asks whichever replica holds the connection to refresh its
// account snapshot early (best effort).
func (s *Store) NotifyRefresh(ctx context.Context, connID uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `SELECT pg_notify('crew_refresh', $1::text)`, connID.String())
	return err
}

// --- Lease operations -------------------------------------------------------

// ClaimLeases atomically claims up to limit unheld/expired active connections
// for holder, returning the claimed connections with their new epochs.
// Only connections whose user belongs to a family are claimable — ingest
// requires a family scope, and onboarding forces family setup right after
// login, so unclaimed pre-family connections are picked up a tick later.
func (s *Store) ClaimLeases(ctx context.Context, holder string, ttl time.Duration, limit int) ([]*CrewConnection, error) {
	if limit <= 0 {
		limit = 1000000
	}
	rows, err := s.Pool.Query(ctx, `
		WITH claimable AS (
			SELECT cc.id FROM crew_connections cc
			WHERE cc.status = 'active'
			  AND (cc.lease_holder IS NULL OR cc.lease_expires_at IS NULL OR cc.lease_expires_at < now())
			  AND EXISTS (SELECT 1 FROM family_members fm WHERE fm.user_id = cc.user_id)
			ORDER BY cc.lease_expires_at NULLS FIRST
			LIMIT $3
			FOR UPDATE SKIP LOCKED
		)
		UPDATE crew_connections c
		SET lease_holder = $1, lease_epoch = c.lease_epoch + 1,
		    lease_expires_at = now() + $2, updated_at = now()
		FROM claimable WHERE c.id = claimable.id
		RETURNING c.id, c.user_id,
		          (SELECT fm.family_id FROM family_members fm WHERE fm.user_id = c.user_id),
		          c.token_ciphertext, c.status, c.last_rotated_at, c.last_polled_at, c.lease_epoch`,
		holder, ttl, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*CrewConnection
	for rows.Next() {
		var c CrewConnection
		if err := rows.Scan(&c.ID, &c.UserID, &c.FamilyID, &c.TokenCiphertext, &c.Status,
			&c.LastRotatedAt, &c.LastPolledAt, &c.LeaseEpoch); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// RenewLease extends a held lease; returns false when the lease was lost
// (or the connection is no longer active), in which case the holder must
// stop using its client immediately.
func (s *Store) RenewLease(ctx context.Context, l Lease, ttl time.Duration) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE crew_connections
		SET lease_expires_at = now() + $4
		WHERE id = $1 AND lease_holder = $2 AND lease_epoch = $3 AND status = 'active'`,
		l.ConnID, l.Holder, l.Epoch, ttl)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ReleaseLease clears a lease if this holder still owns it (graceful shutdown).
func (s *Store) ReleaseLease(ctx context.Context, l Lease) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE crew_connections
		SET lease_holder = NULL, lease_expires_at = NULL
		WHERE id = $1 AND lease_holder = $2 AND lease_epoch = $3`,
		l.ConnID, l.Holder, l.Epoch)
	return err
}

// PersistRotatedToken writes a rotated Crew token under fencing. Returns false
// when the lease was lost and the write did not happen.
func (s *Store) PersistRotatedToken(ctx context.Context, l Lease, ciphertext []byte) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE crew_connections
		SET token_ciphertext = $4, last_rotated_at = now(), updated_at = now()
		WHERE id = $1 AND lease_holder = $2 AND lease_epoch = $3`,
		l.ConnID, l.Holder, l.Epoch, ciphertext)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ReadTokenFenced re-reads the stored token if this holder still owns the lease.
func (s *Store) ReadTokenFenced(ctx context.Context, l Lease) ([]byte, bool, error) {
	var ct []byte
	err := s.Pool.QueryRow(ctx, `
		SELECT token_ciphertext FROM crew_connections
		WHERE id = $1 AND lease_holder = $2 AND lease_epoch = $3`,
		l.ConnID, l.Holder, l.Epoch).Scan(&ct)
	if err == pgx.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return ct, true, nil
}

// MarkNeedsRelogin flips a connection to needs_relogin under fencing and
// releases the lease. Returns false when the lease was already lost.
func (s *Store) MarkNeedsRelogin(ctx context.Context, l Lease) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE crew_connections
		SET status = 'needs_relogin', lease_holder = NULL, lease_expires_at = NULL, updated_at = now()
		WHERE id = $1 AND lease_holder = $2 AND lease_epoch = $3`,
		l.ConnID, l.Holder, l.Epoch)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// TouchPolled records a successful poll under fencing.
func (s *Store) TouchPolled(ctx context.Context, l Lease) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE crew_connections SET last_polled_at = now()
		WHERE id = $1 AND lease_holder = $2 AND lease_epoch = $3`,
		l.ConnID, l.Holder, l.Epoch)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ConnectionStatusFenced reports the connection's current status if the lease
// is still held (used to notice user-initiated disconnects).
func (s *Store) ConnectionStatusFenced(ctx context.Context, l Lease) (ConnStatus, bool, error) {
	var st ConnStatus
	err := s.Pool.QueryRow(ctx, `
		SELECT status FROM crew_connections
		WHERE id = $1 AND lease_holder = $2 AND lease_epoch = $3`,
		l.ConnID, l.Holder, l.Epoch).Scan(&st)
	if err == pgx.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return st, true, nil
}
