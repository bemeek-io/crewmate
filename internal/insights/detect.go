// Package insights finds spending a family might cut back on, and works out
// what doing so would save.
//
// Every number here is computed from transactions. A model may later judge
// whether a flagged spend looks necessary and say why, but it never supplies
// a figure: a savings claim has to be traceable to the charges behind it.
package insights

import (
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bemeek-io/crewmate/internal/store"
)

// Kinds of suggestion.
const (
	// KindSubscription is an active subscription: cancelling it saves a
	// known amount on a known schedule.
	KindSubscription = "subscription"
	// KindHabit is a merchant paid most months at a steady pace — takeout,
	// coffee, a marketplace — where stopping or cutting back saves the pace.
	KindHabit = "habit"
	// KindRising is a category whose spending has climbed well above where
	// it used to be; getting back to the old level saves the difference.
	KindRising = "rising"
)

// Window layout. Months holds WindowMonths full months and then the month in
// progress, oldest first.
const (
	WindowMonths = 12
	// The recent pace is the last three full months: long enough to smooth a
	// one-off, short enough to reflect what's happening now.
	paceMonths = 3
	// A category's baseline is the six full months before the recent pace.
	baselineMonths = 6
)

// Thresholds below which a suggestion isn't worth anyone's attention.
const (
	minHabitPaceCents   = 25_00 // per month
	minHabitMonths      = 4     // of the last six full months
	minBaselineCents    = 50_00 // per month, for a category to be compared at all
	minRiseCents        = 40_00 // per month above baseline
	minRiseRatio        = 1.25
	minSubscriptionCost = 1_00 // per year
	// Subscriptions are "active" if the next charge isn't overdue by more
	// than half a cycle.
	activeGrace = 1.5
)

// Driver is a merchant behind a category's rise.
type Driver struct {
	MerchantKey string `json:"merchant_key"`
	Payee       string `json:"payee"`
	// DeltaCents is how much more per month it gets now than at baseline.
	DeltaCents int64 `json:"delta_cents"`
}

// Suggestion is one thing the family could spend less on, with the evidence.
type Suggestion struct {
	Kind        string `json:"kind"`
	SubjectType string `json:"subject_type"`
	SubjectKey  string `json:"subject_key"`
	Title       string `json:"title"`

	// The category the spending is filed under — for a merchant, the one
	// most of its charges carry.
	CategoryID   *uuid.UUID `json:"category_id"`
	CategoryName string     `json:"category_name"`
	Color        string     `json:"color"`
	MCC          string     `json:"mcc"`

	// Cents is spending per month of the window; the last is in progress.
	Cents []int64 `json:"cents"`
	// Last12Cents and Charges cover the full months of the window.
	Last12Cents int64 `json:"last12_cents"`
	Charges     int   `json:"charges"`

	// PaceCents is the monthly rate the projection runs at.
	PaceCents int64 `json:"pace_cents"`
	// BaselineCents is the earlier monthly level a rising category is
	// compared with.
	BaselineCents int64 `json:"baseline_cents"`
	// Subscriptions only.
	PerChargeCents int64  `json:"per_charge_cents"`
	Cadence        string `json:"cadence"`

	// ProjectedCents is the next twelve months at the current pace, and
	// SavingsCents what acting on the suggestion would keep of it.
	ProjectedCents int64 `json:"projected_cents"`
	SavingsCents   int64 `json:"savings_cents"`

	Drivers []Driver `json:"drivers"`
}

// Key identifies the suggestion's subject across requests.
func (s Suggestion) Key() string { return s.SubjectType + ":" + s.SubjectKey }

// Input is everything detection looks at.
type Input struct {
	Now time.Time
	// Months is WindowMonths full months then the current one.
	Months    []string
	Rows      []store.MerchantMonthSpend
	Profiles  map[string]store.MerchantProfile
	Series    []store.RecurringSeries
	Dismissed []store.InsightDismissal
}

type monthly struct {
	cents  []int64
	counts []int
}

func newMonthly(n int) *monthly { return &monthly{cents: make([]int64, n), counts: make([]int, n)} }

// Detect returns every suggestion worth making, biggest saving first.
func Detect(in Input) []Suggestion {
	n := len(in.Months)
	if n != WindowMonths+1 {
		return nil
	}
	idx := make(map[string]int, n)
	for i, k := range in.Months {
		idx[k] = i
	}

	dismissed := map[string]bool{}
	for _, d := range in.Dismissed {
		dismissed[d.SubjectType+":"+d.SubjectKey] = true
	}
	// Spending the family has put beyond suggestion: a loan, or anything in a
	// category they've said never to flag.
	excluded := func(r store.MerchantMonthSpend) bool {
		if r.SystemKey != nil && *r.SystemKey == store.SystemLoanPayment {
			return true
		}
		return r.CategoryID != nil && dismissed[store.SubjectCategory+":"+r.CategoryID.String()]
	}

	type catInfo struct {
		id          *uuid.UUID
		name, color string
		systemKey   *string
	}
	byMerchant := map[string]*monthly{}
	byCategory := map[string]*monthly{}
	catMerchant := map[string]map[string]*monthly{}
	cats := map[string]catInfo{}
	// Which category each merchant's money mostly went to.
	merchantCat := map[string]map[string]int64{}
	history := n - 1

	for _, r := range in.Rows {
		i, ok := idx[r.Month.Format("2006-01")]
		if !ok {
			continue
		}
		if r.Cents > 0 {
			history = min(history, i)
		}
		if excluded(r) {
			continue
		}
		m := byMerchant[r.MerchantKey]
		if m == nil {
			m = newMonthly(n)
			byMerchant[r.MerchantKey] = m
		}
		m.cents[i] += r.Cents
		m.counts[i] += r.Count

		ck := ""
		if r.CategoryID != nil {
			ck = r.CategoryID.String()
			cats[ck] = catInfo{r.CategoryID, r.CategoryName, r.Color, r.SystemKey}
			c := byCategory[ck]
			if c == nil {
				c = newMonthly(n)
				byCategory[ck] = c
				catMerchant[ck] = map[string]*monthly{}
			}
			c.cents[i] += r.Cents
			cm := catMerchant[ck][r.MerchantKey]
			if cm == nil {
				cm = newMonthly(n)
				catMerchant[ck][r.MerchantKey] = cm
			}
			cm.cents[i] += r.Cents
		}
		if merchantCat[r.MerchantKey] == nil {
			merchantCat[r.MerchantKey] = map[string]int64{}
		}
		merchantCat[r.MerchantKey][ck] += r.Cents
	}

	full := n - 1 // index of the month in progress
	paceFrom := full - paceMonths
	baseFrom := paceFrom - baselineMonths

	payee := func(key string) (string, string) {
		if p, ok := in.Profiles[key]; ok && p.Payee != "" {
			return p.Payee, p.MCC
		}
		return key, ""
	}
	withCategory := func(s *Suggestion, merchant string) {
		if ci, ok := cats[dominantCategory(merchantCat[merchant])]; ok {
			s.CategoryID, s.CategoryName, s.Color = ci.id, ci.name, ci.color
		}
	}
	fill := func(s *Suggestion, m *monthly) {
		s.Cents = m.cents
		for i := 0; i < full; i++ {
			s.Last12Cents += m.cents[i]
			s.Charges += m.counts[i]
		}
	}

	var out []Suggestion

	// Subscriptions.
	subscribed := map[string]bool{}
	for _, sr := range in.Series {
		if sr.Kind != KindSubscription {
			continue
		}
		subscribed[sr.MerchantKey] = true
		if sr.LabelSystemKey != nil && *sr.LabelSystemKey == store.SystemLoanPayment {
			continue
		}
		if dismissed[store.SubjectMerchant+":"+sr.MerchantKey] {
			continue
		}
		m := byMerchant[sr.MerchantKey]
		if m == nil {
			// Every charge was in an excluded category, or outside the window.
			continue
		}
		period, perYear := cadence(sr)
		if perYear == 0 {
			continue
		}
		overdue := in.Now.Sub(sr.LastSeenAt)
		if overdue > time.Duration(float64(period)*activeGrace*float64(24*time.Hour)) {
			continue // cancelled already, by the look of it
		}
		annual := int64(float64(sr.TypicalAmountCents) * perYear)
		if annual < minSubscriptionCost {
			continue
		}
		title, mcc := payee(sr.MerchantKey)
		s := Suggestion{
			Kind:           KindSubscription,
			SubjectType:    store.SubjectMerchant,
			SubjectKey:     sr.MerchantKey,
			Title:          title,
			MCC:            mcc,
			PerChargeCents: sr.TypicalAmountCents,
			Cadence:        sr.Cadence,
			PaceCents:      annual / 12,
			ProjectedCents: annual,
			SavingsCents:   annual,
		}
		fill(&s, m)
		withCategory(&s, sr.MerchantKey)
		out = append(out, s)
	}

	// Rising categories: the baseline has to be real history, not months
	// before the family started using crewmate.
	risingCats := map[string]bool{}
	if history <= baseFrom {
		for ck, c := range byCategory {
			if dismissed[store.SubjectCategory+":"+ck] {
				continue
			}
			base := avg(c.cents[baseFrom:paceFrom])
			pace := avg(c.cents[paceFrom:full])
			if base < minBaselineCents || pace-base < minRiseCents ||
				float64(pace) < float64(base)*minRiseRatio {
				continue
			}
			risingCats[ck] = true
			ci := cats[ck]
			s := Suggestion{
				Kind:           KindRising,
				SubjectType:    store.SubjectCategory,
				SubjectKey:     ck,
				Title:          ci.name,
				CategoryID:     ci.id,
				CategoryName:   ci.name,
				Color:          ci.color,
				PaceCents:      pace,
				BaselineCents:  base,
				ProjectedCents: pace * 12,
				SavingsCents:   (pace - base) * 12,
			}
			s.Cents = c.cents
			for i := 0; i < full; i++ {
				s.Last12Cents += c.cents[i]
			}
			for mk, mm := range catMerchant[ck] {
				if d := avg(mm.cents[paceFrom:full]) - avg(mm.cents[baseFrom:paceFrom]); d > 0 {
					p, _ := payee(mk)
					s.Drivers = append(s.Drivers, Driver{MerchantKey: mk, Payee: p, DeltaCents: d})
				}
			}
			sort.Slice(s.Drivers, func(i, j int) bool {
				if s.Drivers[i].DeltaCents != s.Drivers[j].DeltaCents {
					return s.Drivers[i].DeltaCents > s.Drivers[j].DeltaCents
				}
				return s.Drivers[i].MerchantKey < s.Drivers[j].MerchantKey
			})
			if len(s.Drivers) > 3 {
				s.Drivers = s.Drivers[:3]
			}
			out = append(out, s)
		}
	}

	// Habits: merchants paid most months, at a pace worth noticing. Needs
	// enough history to tell a habit from a new arrival.
	//
	// A merchant whose money mostly goes to a rising category is left to that
	// category's suggestion, where it's named among what drove the rise.
	// Both would claim the same dollars — Home Depot inside Home Improvement
	// — and side by side their savings read as if the merchant outweighed
	// the category it's part of: one is "stop entirely", the other "back to
	// where you were".
	if history <= full-6 {
		for key, m := range byMerchant {
			if subscribed[key] || dismissed[store.SubjectMerchant+":"+key] {
				continue
			}
			if risingCats[dominantCategory(merchantCat[key])] {
				continue
			}
			active := 0
			for i := full - 6; i < full; i++ {
				if m.cents[i] > 0 {
					active++
				}
			}
			pace := avg(m.cents[paceFrom:full])
			stillGoing := m.cents[full-2]+m.cents[full-1]+m.cents[full] > 0
			if active < minHabitMonths || pace < minHabitPaceCents || !stillGoing {
				continue
			}
			title, mcc := payee(key)
			s := Suggestion{
				Kind:           KindHabit,
				SubjectType:    store.SubjectMerchant,
				SubjectKey:     key,
				Title:          title,
				MCC:            mcc,
				PaceCents:      pace,
				ProjectedCents: pace * 12,
				SavingsCents:   pace * 12,
			}
			fill(&s, m)
			withCategory(&s, key)
			out = append(out, s)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].SavingsCents != out[j].SavingsCents {
			return out[i].SavingsCents > out[j].SavingsCents
		}
		return out[i].Key() < out[j].Key()
	})
	return out
}

// dominantCategory is the category most of a merchant's money went to; ""
// for uncategorized.
func dominantCategory(byCat map[string]int64) string {
	var best string
	var bestCents int64 = -1
	for ck, c := range byCat {
		if c > bestCents || (c == bestCents && ck < best) {
			best, bestCents = ck, c
		}
	}
	return best
}

// cadence returns a series' billing period in days and charges per year.
func cadence(s store.RecurringSeries) (periodDays float64, perYear float64) {
	switch s.Cadence {
	case "weekly":
		return 7, 52
	case "biweekly":
		return 14, 26
	case "monthly":
		return 31, 12
	case "quarterly":
		return 92, 4
	case "yearly":
		return 366, 1
	}
	if s.PeriodDays != nil && *s.PeriodDays > 0 {
		return float64(*s.PeriodDays), 365 / float64(*s.PeriodDays)
	}
	return 0, 0
}

func avg(cents []int64) int64 {
	if len(cents) == 0 {
		return 0
	}
	var sum int64
	for _, c := range cents {
		sum += c
	}
	return sum / int64(len(cents))
}

// Fingerprint is what a verdict about s was made on. A verdict whose
// fingerprint no longer matches is stale.
func Fingerprint(s Suggestion) string {
	return strings.Join([]string{s.Kind, s.Title, s.CategoryName}, "|")
}
