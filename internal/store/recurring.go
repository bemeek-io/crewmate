package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type RecurringSeries struct {
	ID              uuid.UUID
	FamilyID        uuid.UUID
	MerchantKey     string
	AmountCents     int64
	AmountTolerance int
	Cadence         string
	PeriodDays      *int
	FirstSeenAt     time.Time
	LastSeenAt      time.Time
	OccurrenceCount int
	IsSubscription  bool
	Dismissed       bool
}

// FindRecurringSeries matches by merchant and amount within each series' tolerance.
func (s *Store) FindRecurringSeries(ctx context.Context, familyID uuid.UUID, merchantKey string, amountCents int64) (*RecurringSeries, error) {
	var r RecurringSeries
	err := s.Pool.QueryRow(ctx, `
		SELECT id, family_id, merchant_key, amount_cents, amount_tolerance, cadence, period_days,
		       first_seen_at, last_seen_at, occurrence_count, is_subscription, dismissed
		FROM recurring_series
		WHERE family_id = $1 AND merchant_key = $2
		  AND abs(amount_cents - $3) <= amount_tolerance
		ORDER BY abs(amount_cents - $3) LIMIT 1`,
		familyID, merchantKey, amountCents,
	).Scan(&r.ID, &r.FamilyID, &r.MerchantKey, &r.AmountCents, &r.AmountTolerance, &r.Cadence,
		&r.PeriodDays, &r.FirstSeenAt, &r.LastSeenAt, &r.OccurrenceCount, &r.IsSubscription, &r.Dismissed)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) CreateRecurringSeries(ctx context.Context, familyID uuid.UUID, merchantKey string, amountCents int64, tolerance int, seenAt time.Time) (*RecurringSeries, error) {
	var r RecurringSeries
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO recurring_series (family_id, merchant_key, amount_cents, amount_tolerance, first_seen_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $5)
		ON CONFLICT (family_id, merchant_key, amount_cents)
		DO UPDATE SET last_seen_at = GREATEST(recurring_series.last_seen_at, EXCLUDED.last_seen_at)
		RETURNING id, family_id, merchant_key, amount_cents, amount_tolerance, cadence, period_days,
		          first_seen_at, last_seen_at, occurrence_count, is_subscription, dismissed`,
		familyID, merchantKey, amountCents, tolerance, seenAt,
	).Scan(&r.ID, &r.FamilyID, &r.MerchantKey, &r.AmountCents, &r.AmountTolerance, &r.Cadence,
		&r.PeriodDays, &r.FirstSeenAt, &r.LastSeenAt, &r.OccurrenceCount, &r.IsSubscription, &r.Dismissed)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) UpdateRecurringSeries(ctx context.Context, id uuid.UUID, cadence string, periodDays *int, occurrenceCount int, lastSeen time.Time, isSubscription bool) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE recurring_series
		SET cadence = $2, period_days = $3, occurrence_count = $4,
		    last_seen_at = GREATEST(last_seen_at, $5),
		    is_subscription = (is_subscription OR $6) AND NOT dismissed
		WHERE id = $1`,
		id, cadence, periodDays, occurrenceCount, lastSeen, isSubscription)
	return err
}

func (s *Store) ListRecurringSeries(ctx context.Context, familyID uuid.UUID) ([]RecurringSeries, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, family_id, merchant_key, amount_cents, amount_tolerance, cadence, period_days,
		       first_seen_at, last_seen_at, occurrence_count, is_subscription, dismissed
		FROM recurring_series
		WHERE family_id = $1 AND occurrence_count >= 2
		ORDER BY is_subscription DESC, last_seen_at DESC`, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecurringSeries
	for rows.Next() {
		var r RecurringSeries
		if err := rows.Scan(&r.ID, &r.FamilyID, &r.MerchantKey, &r.AmountCents, &r.AmountTolerance,
			&r.Cadence, &r.PeriodDays, &r.FirstSeenAt, &r.LastSeenAt, &r.OccurrenceCount,
			&r.IsSubscription, &r.Dismissed); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) SetRecurringDismissed(ctx context.Context, familyID, id uuid.UUID, dismissed bool) error {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE recurring_series
		SET dismissed = $3, is_subscription = is_subscription AND NOT $3
		WHERE id = $2 AND family_id = $1`, familyID, id, dismissed)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
