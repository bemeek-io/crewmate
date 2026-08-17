package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrNotFound = errors.New("not found")

// Membership is the household a user belongs to. It is established from their
// Crew account at sign-in (see EnsureFamily) and is never edited from crewmate
// — there is no creating, joining, inviting, or removing.
type Membership struct {
	FamilyID uuid.UUID
	Role     string
}

// GetMembership returns nil when the user has no family, which means their
// sign-in never completed setup.
func (s *Store) GetMembership(ctx context.Context, userID uuid.UUID) (*Membership, error) {
	var m Membership
	err := s.Pool.QueryRow(ctx, `
		SELECT family_id, role FROM family_members WHERE user_id = $1`, userID,
	).Scan(&m.FamilyID, &m.Role)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}
