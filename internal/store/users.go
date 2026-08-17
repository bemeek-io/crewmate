package store

import (
	"context"
	"fmt"
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

// EnsureFamily guarantees the user belongs to a family, so signing in is the
// only setup step there is — crewmate never asks anyone to name a household or
// type an invite code.
//
// The family is keyed on the Crew household ID, so the first member to sign in
// creates it and everyone after joins the same one and inherits the shared
// categories. A user whose Crew household is unknown gets a family of their
// own, which an invite code can still be used to grow.
//
// Returns the family the user is in.
func (s *Store) EnsureFamily(ctx context.Context, userID uuid.UUID, crewFamilyID, name string) (uuid.UUID, error) {
	// Two members of the same household signing in at once both try to create
	// it; the unique index on crew_family_id decides, and the loser re-reads.
	for attempt := 0; attempt < 2; attempt++ {
		id, err := s.ensureFamilyOnce(ctx, userID, crewFamilyID, name)
		if err == nil {
			return id, nil
		}
		if !isUniqueViolation(err) {
			return uuid.Nil, err
		}
	}
	return uuid.Nil, fmt.Errorf("could not resolve family for user %s", userID)
}

func (s *Store) ensureFamilyOnce(ctx context.Context, userID uuid.UUID, crewFamilyID, name string) (uuid.UUID, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)

	var existing uuid.UUID
	err = tx.QueryRow(ctx, `SELECT family_id FROM family_members WHERE user_id = $1`, userID).Scan(&existing)
	if err == nil {
		// Already placed — but a family created before the Crew link existed
		// carries no household ID, so nobody else from that household would
		// ever join it. Adopt the ID now, which makes the next member land
		// here instead of in a family of their own. The partial unique index
		// keeps this from stealing a household another family already claims.
		if crewFamilyID != "" {
			if _, err := tx.Exec(ctx, `
				UPDATE families SET crew_family_id = $2
				WHERE id = $1 AND crew_family_id IS NULL
				  AND NOT EXISTS (SELECT 1 FROM families WHERE crew_family_id = $2)`,
				existing, crewFamilyID); err != nil {
				return uuid.Nil, err
			}
			if err := tx.Commit(ctx); err != nil {
				return uuid.Nil, err
			}
		}
		return existing, nil
	}
	if err != pgx.ErrNoRows {
		return uuid.Nil, err
	}

	var familyID uuid.UUID
	var crewKey *string
	if crewFamilyID != "" {
		crewKey = &crewFamilyID
		err = tx.QueryRow(ctx, `SELECT id FROM families WHERE crew_family_id = $1`, crewFamilyID).Scan(&familyID)
		if err != nil && err != pgx.ErrNoRows {
			return uuid.Nil, err
		}
	} else {
		err = pgx.ErrNoRows
	}

	created := false
	if err == pgx.ErrNoRows {
		if name == "" {
			name = "Family"
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO families (name, crew_family_id) VALUES ($1, $2)
			RETURNING id`, name, crewKey).Scan(&familyID); err != nil {
			return uuid.Nil, err
		}
		created = true
	}

	// Whoever creates the household administers it; later arrivals are members.
	role := "member"
	if created {
		role = "admin"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO family_members (family_id, user_id, role) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`, familyID, userID, role); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	if created {
		// Subscription and Loan Payment must exist before the first charge
		// lands, or there'd be nothing to label it with.
		if err := s.EnsureSystemCategories(ctx, familyID); err != nil {
			return uuid.Nil, err
		}
	}
	return familyID, nil
}

// AutoJoinByCrewFamily puts a user into the crewmate family already linked to
// their Crew household, if one exists and they aren't in a family yet.
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
