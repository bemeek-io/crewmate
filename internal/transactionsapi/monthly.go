package transactionsapi

import (
	"math"
	"net/http"
	"sort"
	"time"
	// The runtime image is distroless and may carry no zoneinfo, and the
	// month boundaries depend on the family's zone.
	_ "time/tzdata"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bemeek-io/crewmate/internal/family"
	"github.com/bemeek-io/crewmate/internal/httpx"
	"github.com/bemeek-io/crewmate/internal/store"
)

// monthWindows are the selectable spans, counted in calendar months including
// the current one.
var monthWindows = map[string]int{"6": 6, "12": 12}

const defaultMonths = "6"

// trend is a least-squares line through a series' complete months. x is the
// index into the report's months, so the line can be drawn straight onto the
// chart and extended into the month in progress.
type trend struct {
	SlopeCents     float64 `json:"slope_cents"`
	InterceptCents float64 `json:"intercept_cents"`
}

type monthlySeries struct {
	Cents []int64 `json:"cents"`
	// AvgCents is the mean of the complete months with history — the month
	// in progress would drag it down, and months before the family's first
	// transaction aren't months of zero spending.
	AvgCents float64 `json:"avg_cents"`
	Trend    *trend  `json:"trend"`
	// ProjectedCents extrapolates the month in progress at its pace so far.
	ProjectedCents int64 `json:"projected_cents"`
}

type monthlyCategory struct {
	CategoryID   *uuid.UUID `json:"category_id"`
	CategoryName string     `json:"category_name"`
	Color        string     `json:"color"`
	SystemKey    *string    `json:"system_key"`
	TotalCents   int64      `json:"total_cents"`
	monthlySeries
}

type monthlyReport struct {
	Months []string `json:"months"` // "2006-01", oldest first; the last is in progress
	TZ     string   `json:"tz"`
	// HistoryStart is the index of the first month with any spending. Earlier
	// months predate the family's history and are left out of every average.
	HistoryStart int               `json:"history_start"`
	DaysElapsed  int               `json:"days_elapsed"`
	DaysInMonth  int               `json:"days_in_month"`
	Total        monthlySeries     `json:"total"`
	Categories   []monthlyCategory `json:"categories"`
}

// MonthlySpend handles GET /api/cashflow/monthly?months=6|12&tz=<IANA zone>.
//
// Spending per calendar month, overall and by category, with each series'
// average and trend. The zone comes from the browser so months end at the
// family's midnight rather than the server's.
func (h *Handlers) MonthlySpend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	key := q.Get("months")
	if key == "" {
		key = defaultMonths
	}
	n, ok := monthWindows[key]
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "months must be 6 or 12")
		return
	}
	tz := q.Get("tz")
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "unknown time zone")
		return
	}

	now := time.Now().In(loc)
	months, start, end := monthSpan(now, n)

	rows, err := h.Store.MonthlySpend(ctx, family.FamilyID(ctx), start, end, loc.String())
	if err != nil {
		h.Log.Error("monthly spend", zap.Error(err))
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not build the report")
		return
	}

	daysInMonth := end.AddDate(0, 0, -1).Day()
	rep := buildMonthly(months, rows, now.Day(), daysInMonth)
	rep.TZ = loc.String()
	httpx.JSON(w, http.StatusOK, rep)
}

// monthSpan returns the keys of the n calendar months ending with now's, and
// the instants bounding them.
func monthSpan(now time.Time, n int) (keys []string, start, end time.Time) {
	cur := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	start = cur.AddDate(0, -(n - 1), 0)
	end = cur.AddDate(0, 1, 0)
	for m := start; m.Before(end); m = m.AddDate(0, 1, 0) {
		keys = append(keys, m.Format("2006-01"))
	}
	return keys, start, end
}

// buildMonthly lays rows out as one series per category plus the total.
func buildMonthly(months []string, rows []store.MonthlySpendRow, daysElapsed, daysInMonth int) monthlyReport {
	idx := make(map[string]int, len(months))
	for i, k := range months {
		idx[k] = i
	}

	total := make([]int64, len(months))
	byCat := map[string]*monthlyCategory{}
	var order []*monthlyCategory
	for _, r := range rows {
		i, ok := idx[r.Month.Format("2006-01")]
		if !ok {
			continue
		}
		ck := "misc"
		if r.CategoryID != nil {
			ck = r.CategoryID.String()
		}
		c := byCat[ck]
		if c == nil {
			c = &monthlyCategory{
				CategoryID:   r.CategoryID,
				CategoryName: r.CategoryName,
				Color:        r.Color,
				SystemKey:    r.SystemKey,
			}
			c.Cents = make([]int64, len(months))
			byCat[ck] = c
			order = append(order, c)
		}
		c.Cents[i] += r.Cents
		c.TotalCents += r.Cents
		total[i] += r.Cents
	}

	// Leading empty months are before the family's history began. The month
	// in progress always counts as history, even if nothing has landed yet.
	historyStart := len(months) - 1
	for i, c := range total {
		if c > 0 {
			historyStart = min(i, historyStart)
			break
		}
	}

	rep := monthlyReport{
		Months:       months,
		HistoryStart: historyStart,
		DaysElapsed:  daysElapsed,
		DaysInMonth:  daysInMonth,
		Total:        seriesStats(total, historyStart, daysElapsed, daysInMonth),
		Categories:   make([]monthlyCategory, 0, len(order)),
	}
	for _, c := range order {
		c.monthlySeries = seriesStats(c.Cents, historyStart, daysElapsed, daysInMonth)
		rep.Categories = append(rep.Categories, *c)
	}
	// Biggest first, so the list reads in order of what matters.
	sort.SliceStable(rep.Categories, func(i, j int) bool {
		return rep.Categories[i].TotalCents > rep.Categories[j].TotalCents
	})
	return rep
}

func seriesStats(cents []int64, historyStart, daysElapsed, daysInMonth int) monthlySeries {
	s := monthlySeries{Cents: cents}
	last := len(cents) - 1
	if last < 0 {
		return s
	}
	complete := cents[historyStart:last]

	if len(complete) > 0 {
		var sum int64
		for _, c := range complete {
			sum += c
		}
		s.AvgCents = float64(sum) / float64(len(complete))
	}

	// Two points make a line, but not a trend worth drawing.
	if len(complete) >= 3 {
		var sx, sy, sxx, sxy float64
		for k, c := range complete {
			x, y := float64(historyStart+k), float64(c)
			sx += x
			sy += y
			sxx += x * x
			sxy += x * y
		}
		n := float64(len(complete))
		if d := n*sxx - sx*sx; d != 0 {
			slope := (n*sxy - sx*sy) / d
			s.Trend = &trend{SlopeCents: slope, InterceptCents: (sy - slope*sx) / n}
		}
	}

	if daysElapsed > 0 && daysInMonth > 0 {
		s.ProjectedCents = int64(math.Round(float64(cents[last]) / float64(daysElapsed) * float64(daysInMonth)))
	}
	return s
}
