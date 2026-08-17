package transactionsapi

import (
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/bemeek-io/crewmate/internal/family"
	"github.com/bemeek-io/crewmate/internal/httpx"
)

// ranges are the selectable windows, capped at a year.
var ranges = map[string]struct {
	label string
	start func(now time.Time) time.Time
}{
	"1w": {"Last week", func(n time.Time) time.Time { return n.AddDate(0, 0, -7) }},
	"1m": {"Last month", func(n time.Time) time.Time { return n.AddDate(0, -1, 0) }},
	"3m": {"Last quarter", func(n time.Time) time.Time { return n.AddDate(0, -3, 0) }},
	"6m": {"Last 6 months", func(n time.Time) time.Time { return n.AddDate(0, -6, 0) }},
	"1y": {"Last year", func(n time.Time) time.Time { return n.AddDate(-1, 0, 0) }},
}

const defaultRange = "1m"

// CashFlow handles GET /api/cashflow?range=1w|1m|3m|6m|1y.
//
// Income and expenses are reported separately per category rather than netted,
// so a category with a refund still shows what was actually spent. Categories
// are derived from the Crew note, and anything without a match is "Misc".
func (h *Handlers) CashFlow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key := r.URL.Query().Get("range")
	if key == "" {
		key = defaultRange
	}
	spec, ok := ranges[key]
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "range must be 1w, 1m, 3m, 6m or 1y")
		return
	}
	end := time.Now()
	start := spec.start(end)

	rows, err := h.Store.CashFlow(ctx, family.FamilyID(ctx), start, end)
	if err != nil {
		h.Log.Error("cash flow", zap.Error(err))
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not build the report")
		return
	}

	income := make([]map[string]any, 0, len(rows))
	expenses := make([]map[string]any, 0, len(rows))
	var totalIn, totalOut int64
	for _, row := range rows {
		entry := func(cents int64, count int) map[string]any {
			return map[string]any{
				"category_id":   row.CategoryID,
				"category_name": row.CategoryName,
				"color":         row.Color,
				"system_key":    row.SystemKey,
				"cents":         cents,
				"count":         count,
			}
		}
		if row.IncomeCents > 0 {
			income = append(income, entry(row.IncomeCents, row.IncomeCount))
			totalIn += row.IncomeCents
		}
		if row.ExpenseCents > 0 {
			expenses = append(expenses, entry(row.ExpenseCents, row.ExpenseCount))
			totalOut += row.ExpenseCents
		}
	}
	// Biggest first — that's the order these are read in.
	sortByCents(income)
	sortByCents(expenses)

	httpx.JSON(w, http.StatusOK, map[string]any{
		"range":         key,
		"range_label":   spec.label,
		"start":         start,
		"end":           end,
		"income":        income,
		"expenses":      expenses,
		"income_cents":  totalIn,
		"expense_cents": totalOut,
		"net_cents":     totalIn - totalOut,
	})
}

func sortByCents(rows []map[string]any) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j]["cents"].(int64) > rows[j-1]["cents"].(int64); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}
