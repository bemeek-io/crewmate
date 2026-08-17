package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// System category keys. Crewmate owns these: they can be recolored but not
// renamed or deleted, so features can refer to them by meaning.
const (
	SystemSubscription = "subscription"
	SystemLoanPayment  = "loan_payment"
)

// CategoryRule assigns a category to transactions matching all of its set
// conditions. Empty/NULL fields mean "don't care".
type CategoryRule struct {
	ID             uuid.UUID
	FamilyID       uuid.UUID
	CategoryID     uuid.UUID
	CategoryName   string
	CategoryColor  string
	Priority       int
	PayeeMatch     string
	MatchType      string // contains | equals | prefix
	MCC            string
	MinAmountCents *int64 // absolute value bounds
	MaxAmountCents *int64
	Direction      string // any | spend | income
	Enabled        bool
	Source         string // user | series
	CreatedAt      time.Time
}

const ruleCols = `
	r.id, r.family_id, r.category_id, c.name, c.color, r.priority, r.payee_match, r.match_type,
	r.mcc, r.min_amount_cents, r.max_amount_cents, r.direction, r.enabled, r.source, r.created_at`

func scanRule(row pgx.Row) (*CategoryRule, error) {
	var r CategoryRule
	if err := row.Scan(&r.ID, &r.FamilyID, &r.CategoryID, &r.CategoryName, &r.CategoryColor,
		&r.Priority, &r.PayeeMatch, &r.MatchType, &r.MCC, &r.MinAmountCents, &r.MaxAmountCents,
		&r.Direction, &r.Enabled, &r.Source, &r.CreatedAt); err != nil {
		return nil, err
	}
	return &r, nil
}

// ListRules returns a family's rules in evaluation order.
func (s *Store) ListRules(ctx context.Context, familyID uuid.UUID) ([]CategoryRule, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+ruleCols+`
		FROM category_rules r JOIN categories c ON c.id = r.category_id
		WHERE r.family_id = $1
		ORDER BY r.priority, r.created_at`, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CategoryRule
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// ListEnabledRules is the evaluation set used by the categorization pipeline.
func (s *Store) ListEnabledRules(ctx context.Context, familyID uuid.UUID) ([]CategoryRule, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+ruleCols+`
		FROM category_rules r JOIN categories c ON c.id = r.category_id
		WHERE r.family_id = $1 AND r.enabled
		ORDER BY r.priority, r.created_at`, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CategoryRule
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

type RuleInput struct {
	CategoryID     uuid.UUID
	Priority       int
	PayeeMatch     string
	MatchType      string
	MCC            string
	MinAmountCents *int64
	MaxAmountCents *int64
	Direction      string
	Enabled        bool
	Source         string
}

func (s *Store) CreateRule(ctx context.Context, familyID uuid.UUID, in RuleInput) (*CategoryRule, error) {
	var id uuid.UUID
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO category_rules (family_id, category_id, priority, payee_match, match_type,
			mcc, min_amount_cents, max_amount_cents, direction, enabled, source)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING id`,
		familyID, in.CategoryID, in.Priority, in.PayeeMatch, in.MatchType, in.MCC,
		in.MinAmountCents, in.MaxAmountCents, in.Direction, in.Enabled, in.Source).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.GetRule(ctx, familyID, id)
}

func (s *Store) GetRule(ctx context.Context, familyID, id uuid.UUID) (*CategoryRule, error) {
	r, err := scanRule(s.Pool.QueryRow(ctx, `SELECT `+ruleCols+`
		FROM category_rules r JOIN categories c ON c.id = r.category_id
		WHERE r.id = $2 AND r.family_id = $1`, familyID, id))
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	return r, err
}

func (s *Store) UpdateRule(ctx context.Context, familyID, id uuid.UUID, in RuleInput) error {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE category_rules
		SET category_id = $3, priority = $4, payee_match = $5, match_type = $6, mcc = $7,
		    min_amount_cents = $8, max_amount_cents = $9, direction = $10, enabled = $11
		WHERE id = $2 AND family_id = $1`,
		familyID, id, in.CategoryID, in.Priority, in.PayeeMatch, in.MatchType, in.MCC,
		in.MinAmountCents, in.MaxAmountCents, in.Direction, in.Enabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteRule(ctx context.Context, familyID, id uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM category_rules WHERE id = $2 AND family_id = $1`, familyID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpsertSeriesRule records "this merchant is always <category>", created by
// labeling a recurring series. Series rules run ahead of hand-written ones.
func (s *Store) UpsertSeriesRule(ctx context.Context, familyID, categoryID uuid.UUID, merchantKey string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO category_rules (family_id, category_id, priority, payee_match, match_type,
			direction, source)
		VALUES ($1, $2, 50, $3, 'equals', 'spend', 'series')
		ON CONFLICT (family_id, payee_match) WHERE source = 'series'
		DO UPDATE SET category_id = EXCLUDED.category_id, enabled = true`,
		familyID, categoryID, merchantKey)
	return err
}

func (s *Store) DeleteSeriesRule(ctx context.Context, familyID uuid.UUID, merchantKey string) error {
	_, err := s.Pool.Exec(ctx, `
		DELETE FROM category_rules
		WHERE family_id = $1 AND payee_match = $2 AND source = 'series'`, familyID, merchantKey)
	return err
}

// SeriesRuleCategory returns the category a merchant's series label assigns,
// or nil when the series isn't labeled.
func (s *Store) SeriesRuleCategory(ctx context.Context, familyID uuid.UUID, merchantKey string) (*Category, error) {
	var c Category
	err := s.Pool.QueryRow(ctx, `
		SELECT c.id, c.family_id, c.name, c.color, c.system_key, c.created_at
		FROM category_rules r JOIN categories c ON c.id = r.category_id
		WHERE r.family_id = $1 AND r.payee_match = $2 AND r.source = 'series'`,
		familyID, merchantKey,
	).Scan(&c.ID, &c.FamilyID, &c.Name, &c.Color, &c.SystemKey, &c.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// GetSystemCategory resolves one of crewmate's own categories for a family.
func (s *Store) GetSystemCategory(ctx context.Context, familyID uuid.UUID, systemKey string) (*Category, error) {
	var c Category
	err := s.Pool.QueryRow(ctx, `
		SELECT id, family_id, name, color, system_key, created_at
		FROM categories WHERE family_id = $1 AND system_key = $2`, familyID, systemKey,
	).Scan(&c.ID, &c.FamilyID, &c.Name, &c.Color, &c.SystemKey, &c.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// EnsureSystemCategories seeds the categories crewmate owns for a family,
// adopting a same-named one the family already made.
func (s *Store) EnsureSystemCategories(ctx context.Context, familyID uuid.UUID) error {
	for _, sc := range []struct{ key, name, color string }{
		{SystemSubscription, "Subscription", "#a78bfa"},
		{SystemLoanPayment, "Loan Payment", "#38bdf8"},
	} {
		if _, err := s.Pool.Exec(ctx, `
			UPDATE categories SET system_key = $2
			WHERE family_id = $1 AND system_key IS NULL AND lower(name) = lower($3)`,
			familyID, sc.key, sc.name); err != nil {
			return err
		}
		if _, err := s.Pool.Exec(ctx, `
			INSERT INTO categories (family_id, name, color, system_key)
			SELECT $1, $2, $3, $4
			WHERE NOT EXISTS (
				SELECT 1 FROM categories
				WHERE family_id = $1 AND (system_key = $4 OR lower(name) = lower($2)))`,
			familyID, sc.name, sc.color, sc.key); err != nil {
			return err
		}
	}
	return nil
}
