package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RecurringSeries is one merchant's repeated-charge profile. Kind separates a
// true subscription (fixed amount, steady schedule) from a looser recurring
// spend; the spread fields are the evidence behind that call.
type RecurringSeries struct {
	ID                 uuid.UUID
	FamilyID           uuid.UUID
	MerchantKey        string
	Kind               string
	TypicalAmountCents int64
	MinAmountCents     int64
	MaxAmountCents     int64
	Cadence            string
	PeriodDays         *int
	IntervalSpreadPct  int
	AmountSpreadPct    int
	DaySpreadDays      int
	FirstSeenAt        time.Time
	LastSeenAt         time.Time
	OccurrenceCount    int
	Dismissed          bool
	// Label is the system category this merchant was labeled with, carried by
	// the series rule (nil when unlabeled).
	LabelSystemKey *string
	LabelName      *string
}

type RecurringUpsert struct {
	FamilyID           uuid.UUID
	MerchantKey        string
	Kind               string
	TypicalAmountCents int64
	MinAmountCents     int64
	MaxAmountCents     int64
	Cadence            string
	PeriodDays         *int
	IntervalSpreadPct  int
	AmountSpreadPct    int
	DaySpreadDays      int
	FirstSeenAt        time.Time
	LastSeenAt         time.Time
	OccurrenceCount    int
}

// UpsertRecurringSeries writes the merchant's current classification. A series
// the user dismissed stays dismissed.
func (s *Store) UpsertRecurringSeries(ctx context.Context, u RecurringUpsert) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO recurring_series (
			family_id, merchant_key, kind, typical_amount_cents, min_amount_cents,
			max_amount_cents, cadence, period_days, interval_spread_pct, amount_spread_pct,
			day_spread_days, first_seen_at, last_seen_at, occurrence_count
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (family_id, merchant_key) DO UPDATE SET
			kind = EXCLUDED.kind,
			typical_amount_cents = EXCLUDED.typical_amount_cents,
			min_amount_cents = EXCLUDED.min_amount_cents,
			max_amount_cents = EXCLUDED.max_amount_cents,
			cadence = EXCLUDED.cadence,
			period_days = EXCLUDED.period_days,
			interval_spread_pct = EXCLUDED.interval_spread_pct,
			amount_spread_pct = EXCLUDED.amount_spread_pct,
			day_spread_days = EXCLUDED.day_spread_days,
			first_seen_at = LEAST(recurring_series.first_seen_at, EXCLUDED.first_seen_at),
			last_seen_at = GREATEST(recurring_series.last_seen_at, EXCLUDED.last_seen_at),
			occurrence_count = EXCLUDED.occurrence_count
		RETURNING id`,
		u.FamilyID, u.MerchantKey, u.Kind, u.TypicalAmountCents, u.MinAmountCents,
		u.MaxAmountCents, u.Cadence, u.PeriodDays, u.IntervalSpreadPct, u.AmountSpreadPct,
		u.DaySpreadDays, u.FirstSeenAt, u.LastSeenAt, u.OccurrenceCount).Scan(&id)
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// The rule join surfaces a merchant's label (see UpsertSeriesRule).
const seriesFrom = `
	FROM recurring_series s
	LEFT JOIN category_rules rr
	       ON rr.family_id = s.family_id AND rr.payee_match = s.merchant_key AND rr.source = 'series'
	LEFT JOIN categories lc ON lc.id = rr.category_id`

const seriesCols = `
	s.id, s.family_id, s.merchant_key, s.kind, s.typical_amount_cents, s.min_amount_cents,
	s.max_amount_cents, s.cadence, s.period_days, s.interval_spread_pct, s.amount_spread_pct,
	s.day_spread_days, s.first_seen_at, s.last_seen_at, s.occurrence_count, s.dismissed,
	lc.system_key, lc.name`

func scanSeries(row pgx.Row) (*RecurringSeries, error) {
	var r RecurringSeries
	if err := row.Scan(&r.ID, &r.FamilyID, &r.MerchantKey, &r.Kind, &r.TypicalAmountCents,
		&r.MinAmountCents, &r.MaxAmountCents, &r.Cadence, &r.PeriodDays, &r.IntervalSpreadPct,
		&r.AmountSpreadPct, &r.DaySpreadDays, &r.FirstSeenAt, &r.LastSeenAt,
		&r.OccurrenceCount, &r.Dismissed, &r.LabelSystemKey, &r.LabelName); err != nil {
		return nil, err
	}
	return &r, nil
}

// ListRecurringSeries returns everything classified as recurring or better.
func (s *Store) ListRecurringSeries(ctx context.Context, familyID uuid.UUID) ([]RecurringSeries, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+seriesCols+seriesFrom+`
		WHERE s.family_id = $1 AND s.kind <> 'none'
		ORDER BY (s.kind = 'subscription') DESC, s.last_seen_at DESC`, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecurringSeries
	for rows.Next() {
		r, err := scanSeries(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *Store) GetRecurringSeries(ctx context.Context, familyID, id uuid.UUID) (*RecurringSeries, error) {
	r, err := scanSeries(s.Pool.QueryRow(ctx,
		`SELECT `+seriesCols+seriesFrom+` WHERE s.id = $2 AND s.family_id = $1`, familyID, id))
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	return r, err
}

func (s *Store) SetRecurringDismissed(ctx context.Context, familyID, id uuid.UUID, dismissed bool) error {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE recurring_series SET dismissed = $3 WHERE id = $2 AND family_id = $1`,
		familyID, id, dismissed)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MerchantsForClassification lists a family's merchants with enough spend
// history to be worth classifying. Used to (re)build series from existing
// transactions rather than only on new ingest.
func (s *Store) MerchantsForClassification(ctx context.Context, familyID uuid.UUID, minCharges int) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT merchant_key FROM transactions
		WHERE family_id = $1 AND merchant_key <> '' AND amount_cents < 0
		GROUP BY merchant_key
		HAVING count(*) >= $2`, familyID, minCharges)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// FamiliesWithTransactions lists families that have any transaction history.
func (s *Store) FamiliesWithTransactions(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT family_id FROM transactions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// MerchantCharge is one occurrence used for recurring classification.
type MerchantCharge struct {
	OccurredAt  time.Time
	AmountCents int64
}

// MerchantHistory returns a merchant's spend history, oldest first.
func (s *Store) MerchantHistory(ctx context.Context, familyID uuid.UUID, merchantKey string) ([]MerchantCharge, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT occurred_at, amount_cents FROM transactions
		WHERE family_id = $1 AND merchant_key = $2 AND amount_cents < 0
		ORDER BY occurred_at`, familyID, merchantKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MerchantCharge
	for rows.Next() {
		var c MerchantCharge
		if err := rows.Scan(&c.OccurredAt, &c.AmountCents); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
