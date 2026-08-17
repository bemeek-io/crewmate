package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrInviteInvalid = errors.New("invite code invalid, expired, or exhausted")
	ErrAlreadyMember = errors.New("user already belongs to a family")
	ErrNotFound      = errors.New("not found")
)

type Family struct {
	ID        uuid.UUID
	Name      string
	CreatedAt time.Time
}

type FamilyMember struct {
	UserID    uuid.UUID
	FirstName string
	LastName  string
	Role      string
	JoinedAt  time.Time
}

type Membership struct {
	FamilyID uuid.UUID
	Role     string
}

type Invite struct {
	ID        uuid.UUID
	Code      string
	ExpiresAt time.Time
	MaxUses   int
	UseCount  int
}

// GetMembership returns nil when the user has no family.
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

// CreateFamily creates a family and makes the caller its admin. It is stamped
// with the creator's Crew household ID (when known) so other members of that
// Crew family are auto-joined when they sign in.
func (s *Store) CreateFamily(ctx context.Context, name string, creator uuid.UUID) (*Family, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var crewFamilyID *string
	var raw string
	if err := tx.QueryRow(ctx, `SELECT crew_family_id FROM users WHERE id = $1`, creator).Scan(&raw); err == nil && raw != "" {
		// Only claim the Crew household if no other family already has it.
		var taken bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM families WHERE crew_family_id = $1)`, raw).Scan(&taken); err == nil && !taken {
			crewFamilyID = &raw
		}
	}

	var f Family
	if err := tx.QueryRow(ctx, `
		INSERT INTO families (name, crew_family_id) VALUES ($1, $2)
		RETURNING id, name, created_at`, name, crewFamilyID,
	).Scan(&f.ID, &f.Name, &f.CreatedAt); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO family_members (family_id, user_id, role) VALUES ($1, $2, 'admin')`,
		f.ID, creator); err != nil {
		if isUniqueViolation(err) {
			return nil, ErrAlreadyMember
		}
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *Store) GetFamily(ctx context.Context, id uuid.UUID) (*Family, error) {
	var f Family
	err := s.Pool.QueryRow(ctx, `SELECT id, name, created_at FROM families WHERE id = $1`, id).Scan(&f.ID, &f.Name, &f.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *Store) ListFamilyMembers(ctx context.Context, familyID uuid.UUID) ([]FamilyMember, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT m.user_id, u.first_name, u.last_name, m.role, m.joined_at
		FROM family_members m JOIN users u ON u.id = m.user_id
		WHERE m.family_id = $1 ORDER BY m.joined_at`, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FamilyMember
	for rows.Next() {
		var m FamilyMember
		if err := rows.Scan(&m.UserID, &m.FirstName, &m.LastName, &m.Role, &m.JoinedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) RemoveFamilyMember(ctx context.Context, familyID, userID uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `
		DELETE FROM family_members WHERE family_id = $1 AND user_id = $2`, familyID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateInvite(ctx context.Context, familyID, createdBy uuid.UUID, code string, ttl time.Duration, maxUses int) (*Invite, error) {
	var inv Invite
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO family_invites (family_id, code, created_by, expires_at, max_uses)
		VALUES ($1, $2, $3, now() + $4, $5)
		RETURNING id, code, expires_at, max_uses, use_count`,
		familyID, code, createdBy, ttl, maxUses,
	).Scan(&inv.ID, &inv.Code, &inv.ExpiresAt, &inv.MaxUses, &inv.UseCount)
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

// RedeemInvite atomically consumes one use of an invite and joins the user.
func (s *Store) RedeemInvite(ctx context.Context, code string, userID uuid.UUID) (uuid.UUID, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)

	var familyID uuid.UUID
	err = tx.QueryRow(ctx, `
		UPDATE family_invites
		SET use_count = use_count + 1
		WHERE code = $1 AND expires_at > now() AND use_count < max_uses
		RETURNING family_id`, code,
	).Scan(&familyID)
	if err == pgx.ErrNoRows {
		return uuid.Nil, ErrInviteInvalid
	}
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO family_members (family_id, user_id, role) VALUES ($1, $2, 'member')`,
		familyID, userID); err != nil {
		if isUniqueViolation(err) {
			return uuid.Nil, ErrAlreadyMember
		}
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return familyID, nil
}
