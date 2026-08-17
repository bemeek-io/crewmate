package categorize

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/bemeek-io/crewmate/internal/store"
)

// Classification of a merchant's repeated charges.
//
// The distinction that matters in practice: a *subscription* bills the same
// amount on a steady schedule (Netflix on the 14th, $15.99, every month). A
// *recurring* spend repeats at a merchant but the amount moves around (a
// biweekly grocery run). Both are worth surfacing; only the first should be
// called a subscription.
const (
	KindSubscription = "subscription"
	KindRecurring    = "recurring"
	KindNone         = "none"
)

// Thresholds. Deliberately strict on the subscription side — a false
// "subscription" is more annoying than a missed one, since the whole point is
// to spot fixed commitments.
const (
	minOccurrences = 3
	// recentWindow: how many of the latest charges must agree on amount. A
	// window (rather than all history) lets a price increase settle in without
	// permanently disqualifying the subscription.
	recentWindow = 4

	// An identical amount, charge after charge, is the single strongest
	// subscription signal — nothing else bills $2,514.82 eight times running.
	// Where the amount is exact, the schedule is allowed to drift more, since
	// posting dates slip around weekends and month lengths.
	exactAmountSpreadAbs   = 50 // ≤$0.50 apart counts as identical
	exactIntervalSpreadPct = 35
	exactDaySpreadDays     = 10

	// A nearly-fixed amount is weaker evidence, so the schedule has to be
	// correspondingly tighter.
	nearAmountSpreadPct   = 2
	nearIntervalSpreadPct = 20
	nearDaySpreadDays     = 4

	// recurring: shows up on a rough schedule, or is a fixed cost that repeats
	// at irregular times (an insurance premium paid whenever it's remembered).
	recIntervalSpreadPct = 55
)

// Occurrence is one charge considered by the classifier.
type Occurrence struct {
	At          time.Time
	AmountCents int64
}

// Analysis is the classifier's verdict plus the evidence behind it.
type Analysis struct {
	Kind              string
	TypicalAmountCent int64
	MinAmountCents    int64
	MaxAmountCents    int64
	Cadence           string
	PeriodDays        *int
	IntervalSpreadPct int
	AmountSpreadPct   int
	DaySpreadDays     int
	Count             int
	FirstSeen         time.Time
	LastSeen          time.Time
}

type cadenceBucket struct {
	name       string
	periodDays int
}

var cadenceBuckets = []cadenceBucket{
	{"weekly", 7},
	{"biweekly", 14},
	{"monthly", 30},
	{"quarterly", 91},
	{"yearly", 365},
}

// classifyCadence maps a median gap onto a named cadence. Monthly gets a wider
// window because month lengths vary (28–31 days) and billing slips weekends.
func classifyCadence(medianDays float64) (string, *int) {
	for _, b := range cadenceBuckets {
		p := float64(b.periodDays)
		lo, hi := p*0.8, p*1.2
		if b.name == "monthly" {
			lo, hi = 26, 35
		}
		if medianDays >= lo && medianDays <= hi {
			d := b.periodDays
			return b.name, &d
		}
	}
	return "unknown", nil
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func medianCents(v []int64) int64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]int64(nil), v...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2]
}

// intervalStats returns the median gap in days and the coefficient of
// variation (stddev/mean) as a percentage — the regularity measure.
func intervalStats(times []time.Time) (medianDays float64, spreadPct int, ok bool) {
	if len(times) < 2 {
		return 0, 0, false
	}
	var gaps []float64
	for i := 1; i < len(times); i++ {
		d := times[i].Sub(times[i-1]).Hours() / 24
		if d >= 0.5 { // collapse same-day duplicates
			gaps = append(gaps, d)
		}
	}
	if len(gaps) == 0 {
		return 0, 0, false
	}
	var sum float64
	for _, g := range gaps {
		sum += g
	}
	mean := sum / float64(len(gaps))
	if mean <= 0 {
		return 0, 0, false
	}
	var sq float64
	for _, g := range gaps {
		sq += (g - mean) * (g - mean)
	}
	std := math.Sqrt(sq / float64(len(gaps)))
	return median(gaps), int(math.Round(std / mean * 100)), true
}

// dayOfMonthSpread measures how tightly monthly charges cluster on the same
// day, accounting for wrap-around (the 1st and the 30th are 2 days apart).
func dayOfMonthSpread(times []time.Time) int {
	if len(times) < 2 {
		return 0
	}
	days := make([]int, 0, len(times))
	for _, t := range times {
		days = append(days, t.Day())
	}
	best := 31
	// Try each day as the anchor and take the tightest max distance.
	for _, anchor := range days {
		worst := 0
		for _, d := range days {
			diff := d - anchor
			if diff < 0 {
				diff = -diff
			}
			if diff > 15 { // wrap: 30th -> 1st
				diff = 31 - diff
			}
			if diff > worst {
				worst = diff
			}
		}
		if worst < best {
			best = worst
		}
	}
	return best
}

// Classify decides whether a merchant's charges are a subscription, a looser
// recurring spend, or neither.
func Classify(occ []Occurrence) Analysis {
	a := Analysis{Kind: KindNone, Cadence: "unknown", Count: len(occ)}
	if len(occ) == 0 {
		return a
	}
	sorted := append([]Occurrence(nil), occ...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })
	a.FirstSeen = sorted[0].At
	a.LastSeen = sorted[len(sorted)-1].At

	times := make([]time.Time, len(sorted))
	amounts := make([]int64, len(sorted))
	for i, o := range sorted {
		times[i] = o.At
		amounts[i] = o.AmountCents
	}

	// Amount agreement is measured over the most recent charges so a price
	// increase doesn't disqualify a subscription forever.
	recent := amounts
	if len(recent) > recentWindow {
		recent = recent[len(recent)-recentWindow:]
	}
	typical := medianCents(recent)
	a.TypicalAmountCent = typical
	minA, maxA := recent[0], recent[0]
	for _, v := range recent {
		if v < minA {
			minA = v
		}
		if v > maxA {
			maxA = v
		}
	}
	a.MinAmountCents, a.MaxAmountCents = minA, maxA
	spread := maxA - minA
	if spread < 0 {
		spread = -spread
	}
	absTypical := typical
	if absTypical < 0 {
		absTypical = -absTypical
	}
	if absTypical > 0 {
		a.AmountSpreadPct = int(math.Round(float64(spread) / float64(absTypical) * 100))
	}

	medianDays, intervalSpread, ok := intervalStats(times)
	if !ok {
		return a
	}
	a.IntervalSpreadPct = intervalSpread
	a.Cadence, a.PeriodDays = classifyCadence(medianDays)
	a.DaySpreadDays = dayOfMonthSpread(times)

	exactAmount := spread <= exactAmountSpreadAbs
	nearAmount := absTypical > 0 && a.AmountSpreadPct <= nearAmountSpreadPct

	if len(occ) >= minOccurrences && a.Cadence != "unknown" {
		// Day-of-month consistency only means something for monthly and longer
		// cycles; a weekly charge sweeps the calendar by design.
		dated := a.Cadence == "monthly" || a.Cadence == "quarterly" || a.Cadence == "yearly"
		switch {
		case exactAmount &&
			intervalSpread <= exactIntervalSpreadPct &&
			(!dated || a.DaySpreadDays <= exactDaySpreadDays):
			a.Kind = KindSubscription
		case nearAmount &&
			intervalSpread <= nearIntervalSpreadPct &&
			(!dated || a.DaySpreadDays <= nearDaySpreadDays):
			a.Kind = KindSubscription
		}
	}
	if a.Kind == KindNone && len(occ) >= minOccurrences {
		// A fixed cost that repeats is worth surfacing even when the timing is
		// erratic; so is a varying spend that arrives on a rough schedule.
		if exactAmount || intervalSpread <= recIntervalSpreadPct {
			a.Kind = KindRecurring
		}
	}
	return a
}

// minChargesToClassify: below this a merchant can't show any pattern, so it's
// skipped entirely rather than stored as kind=none.
const minChargesToClassify = 3

// ReclassifyFamily rebuilds every merchant's series from stored history.
//
// Classification normally runs as transactions arrive, which leaves existing
// history unclassified after a schema change or a fresh import. This walks the
// merchants already on file and brings their series up to date. It is
// idempotent, so concurrent replicas running it are harmless.
func ReclassifyFamily(ctx context.Context, st *store.Store, familyID uuid.UUID) (int, error) {
	merchants, err := st.MerchantsForClassification(ctx, familyID, minChargesToClassify)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range merchants {
		if ctx.Err() != nil {
			return n, ctx.Err()
		}
		history, err := st.MerchantHistory(ctx, familyID, m)
		if err != nil {
			return n, err
		}
		if len(history) == 0 {
			continue
		}
		occ := make([]Occurrence, 0, len(history))
		for _, h := range history {
			occ = append(occ, Occurrence{At: h.OccurredAt, AmountCents: h.AmountCents})
		}
		a := Classify(occ)
		if _, err := st.UpsertRecurringSeries(ctx, toUpsert(familyID, m, a)); err != nil {
			return n, err
		}
		if a.Kind != KindNone {
			n++
		}
	}
	return n, nil
}

func toUpsert(familyID uuid.UUID, merchantKey string, a Analysis) store.RecurringUpsert {
	return store.RecurringUpsert{
		FamilyID:           familyID,
		MerchantKey:        merchantKey,
		Kind:               a.Kind,
		TypicalAmountCents: a.TypicalAmountCent,
		MinAmountCents:     a.MinAmountCents,
		MaxAmountCents:     a.MaxAmountCents,
		Cadence:            a.Cadence,
		PeriodDays:         a.PeriodDays,
		IntervalSpreadPct:  a.IntervalSpreadPct,
		AmountSpreadPct:    a.AmountSpreadPct,
		DaySpreadDays:      a.DaySpreadDays,
		FirstSeenAt:        a.FirstSeen,
		LastSeenAt:         a.LastSeen,
		OccurrenceCount:    a.Count,
	}
}

// UpdateRecurring folds a transaction into its merchant's series, reclassifies
// from the merchant's full history, and returns the series ID for linking.
func UpdateRecurring(ctx context.Context, st *store.Store, familyID uuid.UUID, merchantKey string, amountCents int64, occurredAt time.Time) (uuid.UUID, error) {
	if merchantKey == "" || amountCents >= 0 {
		// Only spends participate; income and transfers aren't subscriptions.
		return uuid.Nil, nil
	}
	history, err := st.MerchantHistory(ctx, familyID, merchantKey)
	if err != nil {
		return uuid.Nil, err
	}
	occ := make([]Occurrence, 0, len(history))
	for _, h := range history {
		occ = append(occ, Occurrence{At: h.OccurredAt, AmountCents: h.AmountCents})
	}
	a := Classify(occ)
	if a.Count == 0 {
		return uuid.Nil, nil
	}
	return st.UpsertRecurringSeries(ctx, toUpsert(familyID, merchantKey, a))
}
