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

// monthWindows are the preset spans, counted in calendar months including
// the current one. "all" and a custom from/to are handled separately.
var monthWindows = map[string]int{"6": 6, "12": 12}

const defaultMonths = "6"

// maxMonths bounds any one report at ten years of columns. A custom range
// longer than that is refused; "all" keeps the most recent ten years.
const maxMonths = 120

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
	// ProjectedCents extrapolates the month in progress at its pace so far;
	// zero when the window ends in a finished month.
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

// pace is how far through the month in progress the report was built.
type pace struct {
	DaysElapsed int
	DaysInMonth int
}

type monthlyReport struct {
	Months []string `json:"months"` // "2006-01", oldest first
	TZ     string   `json:"tz"`
	// InProgress says the last month is the current one, and so partial. A
	// custom range can end in a finished month, and then every month counts.
	InProgress bool `json:"in_progress"`
	// HistoryStart is the index of the first month with any spending. Earlier
	// months predate the family's history and are left out of every average.
	HistoryStart int               `json:"history_start"`
	DaysElapsed  int               `json:"days_elapsed"`
	DaysInMonth  int               `json:"days_in_month"`
	Total        monthlySeries     `json:"total"`
	Categories   []monthlyCategory `json:"categories"`
}

// MonthlySpend handles
//
//	GET /api/cashflow/monthly?months=6|12|all&tz=<IANA zone>
//	GET /api/cashflow/monthly?from=2025-01&to=2025-12&tz=<IANA zone>
//
// Spending per calendar month, overall and by category, with each series'
// average and trend. The zone comes from the browser so months end at the
// family's midnight rather than the server's.
func (h *Handlers) MonthlySpend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
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
	current := monthOf(now)
	famID := family.FamilyID(ctx)

	var from, to time.Time
	if q.Get("from") != "" || q.Get("to") != "" {
		var msg string
		from, to, msg = customWindow(q.Get("from"), q.Get("to"), current)
		if msg != "" {
			httpx.Error(w, http.StatusBadRequest, "bad_request", msg)
			return
		}
	} else {
		key := q.Get("months")
		if key == "" {
			key = defaultMonths
		}
		to = current
		if key == "all" {
			first, err := h.Store.FirstSpend(ctx, famID)
			if err != nil {
				h.Log.Error("first spend", zap.Error(err))
				httpx.Error(w, http.StatusInternalServerError, "internal", "could not build the report")
				return
			}
			from = current
			if first != nil {
				from = monthOf(first.In(loc))
			}
			from = laterOf(from, current.AddDate(0, -(maxMonths-1), 0))
		} else if n, ok := monthWindows[key]; ok {
			from = current.AddDate(0, -(n - 1), 0)
		} else {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "months must be 6, 12 or all")
			return
		}
	}

	months, start, end := monthSpan(from, to)
	rows, err := h.Store.MonthlySpend(ctx, famID, start, end, loc.String())
	if err != nil {
		h.Log.Error("monthly spend", zap.Error(err))
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not build the report")
		return
	}

	var p *pace
	if to.Equal(current) {
		p = &pace{DaysElapsed: now.Day(), DaysInMonth: end.AddDate(0, 0, -1).Day()}
	}
	rep := buildMonthly(months, rows, p)
	rep.TZ = loc.String()
	httpx.JSON(w, http.StatusOK, rep)
}

// monthOf is midnight on the first of t's month, in t's zone.
func monthOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
}

func laterOf(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// customWindow parses an inclusive from/to pair of "2006-01" months. It
// returns a message for the caller when the range can't be reported.
func customWindow(fromRaw, toRaw string, current time.Time) (from, to time.Time, msg string) {
	loc := current.Location()
	from, err := time.ParseInLocation("2006-01", fromRaw, loc)
	if err != nil {
		return from, to, "from must be a month like 2026-01"
	}
	to, err = time.ParseInLocation("2006-01", toRaw, loc)
	if err != nil {
		return from, to, "to must be a month like 2026-01"
	}
	switch {
	case to.Before(from):
		return from, to, "from must not be after to"
	case to.After(current):
		return from, to, "to must not be in the future"
	case monthsBetween(from, to) > maxMonths:
		return from, to, "a range can be at most 120 months"
	}
	return from, to, ""
}

// monthsBetween counts the months from a to b inclusive.
func monthsBetween(a, b time.Time) int {
	return (b.Year()-a.Year())*12 + int(b.Month()-a.Month()) + 1
}

// monthSpan returns the keys of the calendar months from through to,
// inclusive, and the instants bounding them.
func monthSpan(from, to time.Time) (keys []string, start, end time.Time) {
	start = monthOf(from)
	end = monthOf(to).AddDate(0, 1, 0)
	for m := start; m.Before(end); m = m.AddDate(0, 1, 0) {
		keys = append(keys, m.Format("2006-01"))
	}
	return keys, start, end
}

// buildMonthly lays rows out as one series per category plus the total.
// p is nil when the last month is already over.
func buildMonthly(months []string, rows []store.MonthlySpendRow, p *pace) monthlyReport {
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

	// Leading empty months are before the family's history began. The last
	// month always counts as history, even if nothing has landed yet.
	historyStart := len(months) - 1
	for i, c := range total {
		if c > 0 {
			historyStart = min(i, historyStart)
			break
		}
	}

	rep := monthlyReport{
		Months:       months,
		InProgress:   p != nil,
		HistoryStart: historyStart,
		Total:        seriesStats(total, historyStart, p),
		Categories:   make([]monthlyCategory, 0, len(order)),
	}
	if p != nil {
		rep.DaysElapsed, rep.DaysInMonth = p.DaysElapsed, p.DaysInMonth
	}
	for _, c := range order {
		c.monthlySeries = seriesStats(c.Cents, historyStart, p)
		rep.Categories = append(rep.Categories, *c)
	}
	// Biggest first, so the list reads in order of what matters.
	sort.SliceStable(rep.Categories, func(i, j int) bool {
		return rep.Categories[i].TotalCents > rep.Categories[j].TotalCents
	})
	return rep
}

func seriesStats(cents []int64, historyStart int, p *pace) monthlySeries {
	s := monthlySeries{Cents: cents}
	last := len(cents) - 1
	if last < 0 {
		return s
	}
	// A month still in progress is shown, but it isn't evidence of anything.
	completeEnd := len(cents)
	if p != nil {
		completeEnd = last
	}
	complete := cents[historyStart:completeEnd]

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

	if p != nil && p.DaysElapsed > 0 {
		s.ProjectedCents = int64(math.Round(float64(cents[last]) / float64(p.DaysElapsed) * float64(p.DaysInMonth)))
	}
	return s
}
