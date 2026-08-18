package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// VendorSpend is one merchant's total over a window.
type VendorSpend struct {
	MerchantKey string
	// Payee is the most recent display name for the merchant; merchant_key is
	// normalized for matching and is not what anyone wants to read.
	Payee string
	Cents int64
	Count int
}

// SubscriptionSpend totals what the family actually pays in subscriptions over
// a window, per vendor.
//
// Driven by the series classification rather than by the Subscription
// category, which is the distinction that makes it useful: a subscription
// filed under Tech is still a subscription, and a category named Subscription
// isn't evidence that anything recurs. Only kind='subscription' counts —
// same vendor, same amount, same point in the cycle — so the merely recurring
// (groceries every fortnight) is excluded, as is anything dismissed.
//
// EXISTS rather than a join: a merchant can have several series (one per
// amount), and joining would count its transactions once per series.
func (s *Store) SubscriptionSpend(ctx context.Context, familyID uuid.UUID, start, end time.Time) ([]VendorSpend, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT t.merchant_key,
		       (array_agg(t.payee ORDER BY t.occurred_at DESC))[1],
		       SUM(-t.amount_cents),
		       COUNT(*)
		FROM transactions t
		WHERE t.family_id = $1
		  AND t.occurred_at >= $2 AND t.occurred_at < $3
		  AND t.amount_cents < 0
		  AND EXISTS (
		      SELECT 1 FROM recurring_series s
		       WHERE s.family_id = t.family_id
		         AND s.merchant_key = t.merchant_key
		         AND s.kind = 'subscription'
		         AND NOT s.dismissed
		  )
		  -- A car loan bills like a subscription — same vendor, same amount,
		  -- same day — so the classifier calls it one. Filing it under Loan
		  -- Payment is the family saying otherwise, and a loan left in would
		  -- swamp every real subscription.
		  --
		  -- Judged on the transaction's own category, so it holds however that
		  -- category was applied: by hand, by a rule, or by labelling the
		  -- series. Checking only the series label missed the other two.
		  AND NOT EXISTS (
		      SELECT 1 FROM categories lc
		       WHERE lc.family_id = t.family_id
		         AND lc.system_key = '`+SystemLoanPayment+`'
		         AND lower(lc.name) = lower(t.note)
		  )
		  -- For charges not categorized at all, the series label still decides.
		  AND NOT EXISTS (
		      SELECT 1 FROM category_rules r
		        JOIN categories lc ON lc.id = r.category_id
		       WHERE r.family_id = t.family_id
		         AND r.payee_match = t.merchant_key
		         AND r.source = 'series'
		         AND lc.system_key = '`+SystemLoanPayment+`'
		  )
		GROUP BY t.merchant_key
		ORDER BY SUM(-t.amount_cents) DESC`, familyID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanVendorSpend(rows)
}

// CountSubscriptionSeries reports how many merchants detection currently
// classifies as subscriptions, before loans and dismissals are removed.
//
// Exposed so a total that looks short can be diagnosed from the app itself: a
// merchant missing from both this count and the total is a classification
// question, not a fault in the totalling.
func (s *Store) CountSubscriptionSeries(ctx context.Context, familyID uuid.UUID) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM recurring_series
		 WHERE family_id = $1 AND kind = 'subscription' AND NOT dismissed`, familyID).Scan(&n)
	return n, err
}

// CashFlowVendors breaks one cash flow line down by merchant.
//
// categoryID nil with uncategorized true selects transactions whose note names
// no category; otherwise it selects that category. direction picks the side,
// matching how the report splits income from expenses.
func (s *Store) CashFlowVendors(ctx context.Context, familyID uuid.UUID, start, end time.Time,
	categoryID *uuid.UUID, uncategorized bool, direction string) ([]VendorSpend, error) {

	q := `
		SELECT t.merchant_key,
		       (array_agg(t.payee ORDER BY t.occurred_at DESC))[1],
		       SUM(` + signExpr(direction) + `),
		       COUNT(*)
		FROM transactions t
		LEFT JOIN categories c
		       ON c.family_id = t.family_id AND lower(c.name) = lower(t.note)
		WHERE t.family_id = $1 AND t.occurred_at >= $2 AND t.occurred_at < $3
		  AND ` + directionPredicate(direction)
	args := []any{familyID, start, end}
	switch {
	case categoryID != nil:
		args = append(args, *categoryID)
		q += ` AND c.id = $4`
	case uncategorized:
		q += ` AND c.id IS NULL`
	}
	q += ` GROUP BY t.merchant_key ORDER BY SUM(` + signExpr(direction) + `) DESC`

	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanVendorSpend(rows)
}

// Totals are reported as magnitudes on both sides, matching the report.
func signExpr(direction string) string {
	if direction == DirectionIncome {
		return "t.amount_cents"
	}
	return "-t.amount_cents"
}

func directionPredicate(direction string) string {
	if direction == DirectionIncome {
		return "t.amount_cents > 0"
	}
	return "t.amount_cents < 0"
}

func scanVendorSpend(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]VendorSpend, error) {
	out := make([]VendorSpend, 0, 16)
	for rows.Next() {
		var v VendorSpend
		var payee *string
		if err := rows.Scan(&v.MerchantKey, &payee, &v.Cents, &v.Count); err != nil {
			return nil, err
		}
		if payee != nil {
			v.Payee = *payee
		}
		if v.Payee == "" {
			v.Payee = v.MerchantKey
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
