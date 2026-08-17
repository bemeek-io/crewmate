package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Cash flow directions, also accepted as the `direction` query parameter.
const (
	DirectionIncome  = "income"
	DirectionExpense = "expense"
)

// CashFlowRow is one category's totals over a window. A category can appear
// with both income and expense — a refund lands in the same category as the
// purchase — so the two directions are kept side by side rather than netted.
type CashFlowRow struct {
	// CategoryID is nil for uncategorized transactions, shown as "Misc".
	CategoryID   *uuid.UUID
	CategoryName string
	Color        string
	SystemKey    *string
	IncomeCents  int64
	ExpenseCents int64 // positive magnitude
	IncomeCount  int
	ExpenseCount int
}

// CashFlow totals every transaction in [start, end) by derived category.
//
// Amount sign is the only income/expense signal Crew gives us: positive is
// money in, negative is money out. Crew's cashTransactions feed carries no
// internal pocket-to-pocket transfers, so those don't inflate either side —
// but a transfer to an outside bank does look like an expense, because at this
// layer it is indistinguishable from one.
func (s *Store) CashFlow(ctx context.Context, familyID uuid.UUID, start, end time.Time) ([]CashFlowRow, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT c.id, COALESCE(c.name, ''), COALESCE(c.color, ''), c.system_key,
		       COALESCE(SUM(t.amount_cents) FILTER (WHERE t.amount_cents > 0), 0),
		       COALESCE(SUM(-t.amount_cents) FILTER (WHERE t.amount_cents < 0), 0),
		       COUNT(*) FILTER (WHERE t.amount_cents > 0),
		       COUNT(*) FILTER (WHERE t.amount_cents < 0)
		FROM transactions t
		LEFT JOIN categories c
		       ON c.family_id = t.family_id AND lower(c.name) = lower(t.note)
		WHERE t.family_id = $1 AND t.occurred_at >= $2 AND t.occurred_at < $3
		  AND t.amount_cents <> 0
		GROUP BY c.id, c.name, c.color, c.system_key
		ORDER BY c.name NULLS LAST`, familyID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]CashFlowRow, 0, 16)
	for rows.Next() {
		var r CashFlowRow
		if err := rows.Scan(&r.CategoryID, &r.CategoryName, &r.Color, &r.SystemKey,
			&r.IncomeCents, &r.ExpenseCents, &r.IncomeCount, &r.ExpenseCount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
