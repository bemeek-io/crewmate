package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type AccountSnapshot struct {
	ConnectionID uuid.UUID
	Payload      []byte // JSON: accounts + subaccounts with balances/goals
	FetchedAt    time.Time
}

func (s *Store) UpsertAccountSnapshot(ctx context.Context, connID uuid.UUID, payload []byte) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO account_snapshots (connection_id, payload, fetched_at)
		VALUES ($1, $2, now())
		ON CONFLICT (connection_id)
		DO UPDATE SET payload = EXCLUDED.payload, fetched_at = now()`, connID, payload)
	return err
}

// ListFamilySnapshots returns the snapshot for each connected member of a family.
type MemberSnapshot struct {
	UserID    uuid.UUID
	FirstName string
	Payload   []byte
	FetchedAt time.Time
}

func (s *Store) ListFamilySnapshots(ctx context.Context, familyID uuid.UUID) ([]MemberSnapshot, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT c.user_id, u.first_name, a.payload, a.fetched_at
		FROM family_members m
		JOIN users u ON u.id = m.user_id
		JOIN crew_connections c ON c.user_id = m.user_id
		JOIN account_snapshots a ON a.connection_id = c.id
		WHERE m.family_id = $1
		ORDER BY u.first_name`, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemberSnapshot
	for rows.Next() {
		var ms MemberSnapshot
		if err := rows.Scan(&ms.UserID, &ms.FirstName, &ms.Payload, &ms.FetchedAt); err != nil {
			return nil, err
		}
		out = append(out, ms)
	}
	return out, rows.Err()
}

// GetSnapshot returns nil when no snapshot exists yet for the connection.
func (s *Store) GetSnapshot(ctx context.Context, connID uuid.UUID) (*AccountSnapshot, error) {
	var a AccountSnapshot
	a.ConnectionID = connID
	err := s.Pool.QueryRow(ctx, `
		SELECT payload, fetched_at FROM account_snapshots WHERE connection_id = $1`, connID,
	).Scan(&a.Payload, &a.FetchedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}
