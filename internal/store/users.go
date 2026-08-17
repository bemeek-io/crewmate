package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID         uuid.UUID
	CrewUserID string
	FirstName  string
	LastName   string
	CreatedAt  time.Time
}

// UpsertUser creates or refreshes a user keyed by their Crew user ID.
func (s *Store) UpsertUser(ctx context.Context, crewUserID, firstName, lastName string) (*User, error) {
	var u User
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO users (crew_user_id, first_name, last_name)
		VALUES ($1, $2, $3)
		ON CONFLICT (crew_user_id)
		DO UPDATE SET first_name = EXCLUDED.first_name, last_name = EXCLUDED.last_name
		RETURNING id, crew_user_id, first_name, last_name, created_at`,
		crewUserID, firstName, lastName,
	).Scan(&u.ID, &u.CrewUserID, &u.FirstName, &u.LastName, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) GetUser(ctx context.Context, id uuid.UUID) (*User, error) {
	var u User
	err := s.Pool.QueryRow(ctx, `
		SELECT id, crew_user_id, first_name, last_name, created_at
		FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.CrewUserID, &u.FirstName, &u.LastName, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}
