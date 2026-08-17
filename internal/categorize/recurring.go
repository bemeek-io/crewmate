package categorize

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/bemeek-io/crewmate/internal/store"
)

// amountTolerance allows small price drift within a series (streaming price
// bumps, tax changes): 5% of the amount, min 100 cents.
func amountTolerance(amountCents int64) int {
	t := amountCents
	if t < 0 {
		t = -t
	}
	tol := t / 20
	if tol < 100 {
		tol = 100
	}
	if tol > 10000 {
		tol = 10000
	}
	return int(tol)
}

type cadence struct {
	name       string
	periodDays int
}

// cadenceBuckets: median interval must be within ±20% of the period.
var cadenceBuckets = []cadence{
	{"weekly", 7},
	{"biweekly", 14},
	{"monthly", 30},
	{"quarterly", 91},
	{"yearly", 365},
}

func classifyCadence(medianDays float64) (string, *int) {
	for _, b := range cadenceBuckets {
		p := float64(b.periodDays)
		if medianDays >= p*0.8 && medianDays <= p*1.2 {
			d := b.periodDays
			return b.name, &d
		}
	}
	return "unknown", nil
}

func medianInterval(times []time.Time) (float64, bool) {
	if len(times) < 2 {
		return 0, false
	}
	intervals := make([]float64, 0, len(times)-1)
	for i := 1; i < len(times); i++ {
		d := times[i].Sub(times[i-1]).Hours() / 24
		if d >= 1 { // ignore same-day duplicates
			intervals = append(intervals, d)
		}
	}
	if len(intervals) == 0 {
		return 0, false
	}
	sort.Float64s(intervals)
	return intervals[len(intervals)/2], true
}

// UpdateRecurring folds a transaction into its recurring series (creating one
// if needed), recomputes cadence from the merchant's history, and promotes the
// series to a subscription at >=3 occurrences with a stable cadence.
// Returns the series ID for linking, or uuid.Nil when skipped.
func UpdateRecurring(ctx context.Context, st *store.Store, familyID uuid.UUID, merchantKey string, amountCents int64, occurredAt time.Time) (uuid.UUID, error) {
	if merchantKey == "" || amountCents >= 0 {
		// Only spends (negative amounts) participate in subscription detection.
		return uuid.Nil, nil
	}
	series, err := st.FindRecurringSeries(ctx, familyID, merchantKey, amountCents)
	if err != nil {
		return uuid.Nil, err
	}
	if series == nil {
		series, err = st.CreateRecurringSeries(ctx, familyID, merchantKey, amountCents,
			amountTolerance(amountCents), occurredAt)
		if err != nil {
			return uuid.Nil, err
		}
	}

	occurrences, err := st.MerchantOccurrences(ctx, familyID, merchantKey,
		series.AmountCents, int64(series.AmountTolerance))
	if err != nil {
		return series.ID, err
	}

	cadenceName, periodDays := "unknown", (*int)(nil)
	if med, ok := medianInterval(occurrences); ok {
		cadenceName, periodDays = classifyCadence(med)
	}
	isSub := len(occurrences) >= 3 && cadenceName != "unknown"
	if err := st.UpdateRecurringSeries(ctx, series.ID, cadenceName, periodDays,
		len(occurrences), occurredAt, isSub); err != nil {
		return series.ID, err
	}
	return series.ID, nil
}
