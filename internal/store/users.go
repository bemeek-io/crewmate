package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type User struct {
	ID         uuid.UUID
	CrewUserID string
	FirstName  string
	LastName   string
	// CrewFamilyID is Crew's own household identifier, used to auto-join
	// members of the same Crew family to the same crewmate family.
	CrewFamilyID string
	CreatedAt    time.Time
}

// UpsertUser creates or refreshes a user keyed by their Crew user ID.
func (s *Store) UpsertUser(ctx context.Context, crewUserID, firstName, lastName, crewFamilyID string) (*User, error) {
	var u User
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO users (crew_user_id, first_name, last_name, crew_family_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (crew_user_id)
		DO UPDATE SET first_name = EXCLUDED.first_name, last_name = EXCLUDED.last_name,
		              crew_family_id = CASE WHEN EXCLUDED.crew_family_id <> ''
		                                    THEN EXCLUDED.crew_family_id
		                                    ELSE users.crew_family_id END
		RETURNING id, crew_user_id, first_name, last_name, crew_family_id, created_at`,
		crewUserID, firstName, lastName, crewFamilyID,
	).Scan(&u.ID, &u.CrewUserID, &u.FirstName, &u.LastName, &u.CrewFamilyID, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) GetUser(ctx context.Context, id uuid.UUID) (*User, error) {
	var u User
	err := s.Pool.QueryRow(ctx, `
		SELECT id, crew_user_id, first_name, last_name, crew_family_id, created_at
		FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.CrewUserID, &u.FirstName, &u.LastName, &u.CrewFamilyID, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// AutoJoinByCrewFamily puts a user into the crewmate family already linked to
// their Crew household, if one exists and they aren't in a family yet. This is
// what lets a second member sign in and immediately share categories.
// Returns the family joined, or uuid.Nil when there was nothing to join.
func (s *Store) AutoJoinByCrewFamily(ctx context.Context, userID uuid.UUID, crewFamilyID string) (uuid.UUID, error) {
	if crewFamilyID == "" {
		return uuid.Nil, nil
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)

	var existing uuid.UUID
	err = tx.QueryRow(ctx, `SELECT family_id FROM family_members WHERE user_id = $1`, userID).Scan(&existing)
	if err == nil {
		return uuid.Nil, nil // already in a family; leave it alone
	}
	if err != pgx.ErrNoRows {
		return uuid.Nil, err
	}

	var familyID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM families WHERE crew_family_id = $1`, crewFamilyID).Scan(&familyID)
	if err == pgx.ErrNoRows {
		return uuid.Nil, nil // nobody from this Crew household has set up yet
	}
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO family_members (family_id, user_id, role) VALUES ($1, $2, 'member')
		ON CONFLICT DO NOTHING`, familyID, userID); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return familyID, nil
}
