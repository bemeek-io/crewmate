package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Category struct {
	ID        uuid.UUID
	FamilyID  uuid.UUID
	Name      string
	Emoji     string
	Color     string
	CreatedAt time.Time
}

type MerchantRule struct {
	ID           uuid.UUID
	FamilyID     uuid.UUID
	MerchantKey  string
	CategoryID   uuid.UUID
	CategoryName string
	Source       string // user | llm
	Confidence   string
	CreatedAt    time.Time
}

func (s *Store) ListCategories(ctx context.Context, familyID uuid.UUID) ([]Category, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, family_id, name, emoji, color, created_at
		FROM categories WHERE family_id = $1 ORDER BY lower(name)`, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.FamilyID, &c.Name, &c.Emoji, &c.Color, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CreateCategory(ctx context.Context, familyID, createdBy uuid.UUID, name, emoji, color string) (*Category, error) {
	var c Category
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO categories (family_id, name, emoji, color, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, family_id, name, emoji, color, created_at`,
		familyID, name, emoji, color, createdBy,
	).Scan(&c.ID, &c.FamilyID, &c.Name, &c.Emoji, &c.Color, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) UpdateCategory(ctx context.Context, familyID, id uuid.UUID, name, emoji, color string) error {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE categories SET name = $3, emoji = $4, color = $5
		WHERE id = $2 AND family_id = $1`, familyID, id, name, emoji, color)
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

// GetCategoryByName resolves a family category case-insensitively (LLM results).
func (s *Store) GetCategoryByName(ctx context.Context, familyID uuid.UUID, name string) (*Category, error) {
	var c Category
	err := s.Pool.QueryRow(ctx, `
		SELECT id, family_id, name, emoji, color, created_at
		FROM categories WHERE family_id = $1 AND lower(name) = lower($2)`, familyID, name,
	).Scan(&c.ID, &c.FamilyID, &c.Name, &c.Emoji, &c.Color, &c.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// --- Merchant rules ---------------------------------------------------------

func (s *Store) GetMerchantRule(ctx context.Context, familyID uuid.UUID, merchantKey string) (*MerchantRule, error) {
	var r MerchantRule
	err := s.Pool.QueryRow(ctx, `
		SELECT r.id, r.family_id, r.merchant_key, r.category_id, c.name, r.source, r.confidence, r.created_at
		FROM merchant_rules r JOIN categories c ON c.id = r.category_id
		WHERE r.family_id = $1 AND r.merchant_key = $2`, familyID, merchantKey,
	).Scan(&r.ID, &r.FamilyID, &r.MerchantKey, &r.CategoryID, &r.CategoryName, &r.Source, &r.Confidence, &r.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) ListMerchantRules(ctx context.Context, familyID uuid.UUID) ([]MerchantRule, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT r.id, r.family_id, r.merchant_key, r.category_id, c.name, r.source, r.confidence, r.created_at
		FROM merchant_rules r JOIN categories c ON c.id = r.category_id
		WHERE r.family_id = $1 ORDER BY r.merchant_key`, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MerchantRule
	for rows.Next() {
		var r MerchantRule
		if err := rows.Scan(&r.ID, &r.FamilyID, &r.MerchantKey, &r.CategoryID, &r.CategoryName, &r.Source, &r.Confidence, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpsertMerchantRule: user rules always overwrite; LLM rules never overwrite
// an existing rule (ON CONFLICT DO NOTHING) so user choices are sticky.
func (s *Store) UpsertMerchantRule(ctx context.Context, familyID uuid.UUID, merchantKey string, categoryID uuid.UUID, source, confidence string) error {
	if source == "user" {
		_, err := s.Pool.Exec(ctx, `
			INSERT INTO merchant_rules (family_id, merchant_key, category_id, source, confidence)
			VALUES ($1, $2, $3, 'user', $4)
			ON CONFLICT (family_id, merchant_key)
			DO UPDATE SET category_id = EXCLUDED.category_id, source = 'user',
			              confidence = EXCLUDED.confidence, updated_at = now()`,
			familyID, merchantKey, categoryID, confidence)
		return err
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO merchant_rules (family_id, merchant_key, category_id, source, confidence)
		VALUES ($1, $2, $3, 'llm', $4)
		ON CONFLICT (family_id, merchant_key) DO NOTHING`,
		familyID, merchantKey, categoryID, confidence)
	return err
}

func (s *Store) UpdateMerchantRule(ctx context.Context, familyID, id, categoryID uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE merchant_rules SET category_id = $3, source = 'user', updated_at = now()
		WHERE id = $2 AND family_id = $1`, familyID, id, categoryID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteMerchantRule(ctx context.Context, familyID, id uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `
		DELETE FROM merchant_rules WHERE id = $2 AND family_id = $1`, familyID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
