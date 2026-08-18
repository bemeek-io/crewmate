package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Category is a family's reusable label. This is the only categorization state
// crewmate stores: a transaction's category lives in Crew's note field and is
// resolved by matching that note against these names.
type Category struct {
	ID       uuid.UUID
	FamilyID uuid.UUID
	Name     string
	Color    string
	// SystemKey marks a category crewmate owns (see store.System* constants).
	// These can be recolored but not renamed or deleted, because features
	// refer to them by meaning rather than by name.
	SystemKey *string
	// ExcludeFromLLM withholds this category from auto-categorization; it
	// stays available by hand and to rules.
	ExcludeFromLLM bool
	// UsageCount is how many transactions carry this category, so a picker can
	// lead with the ones a family actually reaches for. Populated only by
	// ListCategories.
	UsageCount int
	CreatedAt  time.Time
}

func (s *Store) ListCategories(ctx context.Context, familyID uuid.UUID) ([]Category, error) {
	// Counted from the notes rather than stored, for the same reason the
	// category itself isn't stored on the transaction: Crew's note field is the
	// source of truth, so a tally kept alongside would drift the moment someone
	// edited a note in the Crew app.
	rows, err := s.Pool.Query(ctx, `
		SELECT c.id, c.family_id, c.name, c.color, c.system_key, c.exclude_from_llm,
		       (SELECT COUNT(*) FROM transactions t
		         WHERE t.family_id = c.family_id AND lower(t.note) = lower(c.name)),
		       c.created_at
		FROM categories c WHERE c.family_id = $1 ORDER BY lower(c.name)`, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.FamilyID, &c.Name, &c.Color, &c.SystemKey,
			&c.ExcludeFromLLM, &c.UsageCount, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CreateCategory(ctx context.Context, familyID, createdBy uuid.UUID, name, color string) (*Category, error) {
	var c Category
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO categories (family_id, name, color, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id, family_id, name, color, system_key, exclude_from_llm, created_at`,
		familyID, name, color, createdBy,
	).Scan(&c.ID, &c.FamilyID, &c.Name, &c.Color, &c.SystemKey, &c.ExcludeFromLLM, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) GetCategory(ctx context.Context, familyID, id uuid.UUID) (*Category, error) {
	var c Category
	err := s.Pool.QueryRow(ctx, `
		SELECT id, family_id, name, color, system_key, exclude_from_llm, created_at
		FROM categories WHERE id = $2 AND family_id = $1`, familyID, id,
	).Scan(&c.ID, &c.FamilyID, &c.Name, &c.Color, &c.SystemKey, &c.ExcludeFromLLM, &c.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) UpdateCategory(ctx context.Context, familyID, id uuid.UUID, name, color string, excludeFromLLM bool) error {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE categories SET name = $3, color = $4, exclude_from_llm = $5
		WHERE id = $2 AND family_id = $1`, familyID, id, name, color, excludeFromLLM)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteCategory(ctx context.Context, familyID, id uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `
		DELETE FROM categories WHERE id = $2 AND family_id = $1`, familyID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetCategoryByName resolves a family category case-insensitively (used to
// validate an LLM answer against the family's real list).
func (s *Store) GetCategoryByName(ctx context.Context, familyID uuid.UUID, name string) (*Category, error) {
	var c Category
	err := s.Pool.QueryRow(ctx, `
		SELECT id, family_id, name, color, system_key, exclude_from_llm, created_at
		FROM categories WHERE family_id = $1 AND lower(name) = lower($2)`, familyID, name,
	).Scan(&c.ID, &c.FamilyID, &c.Name, &c.Color, &c.SystemKey, &c.ExcludeFromLLM, &c.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}
