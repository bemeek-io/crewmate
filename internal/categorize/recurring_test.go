package categorize

import (
	"testing"
	"time"
)

func at(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
}

func occ(pairs ...any) []Occurrence {
	var out []Occurrence
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, Occurrence{At: pairs[i].(time.Time), AmountCents: int64(pairs[i+1].(int))})
	}
	return out
}

// A real subscription: identical amount, same day each month.
func TestClassifySubscription(t *testing.T) {
	a := Classify(occ(
		at(2026, 3, 14), -1599,
		at(2026, 4, 14), -1599,
		at(2026, 5, 14), -1599,
		at(2026, 6, 14), -1599,
	))
	if a.Kind != KindSubscription {
		t.Fatalf("kind = %q, want subscription (interval spread %d%%, amount spread %d%%, day spread %d)",
			a.Kind, a.IntervalSpreadPct, a.AmountSpreadPct, a.DaySpreadDays)
	}
	if a.Cadence != "monthly" {
		t.Errorf("cadence = %q, want monthly", a.Cadence)
	}
}

// Billing dates drift by a day or two (weekends) but it's still a subscription.
func TestClassifySubscriptionSlightDrift(t *testing.T) {
	a := Classify(occ(
		at(2026, 3, 14), -1599,
		at(2026, 4, 15), -1599,
		at(2026, 5, 13), -1599,
		at(2026, 6, 16), -1599,
	))
	if a.Kind != KindSubscription {
		t.Fatalf("kind = %q, want subscription", a.Kind)
	}
}

// A price increase: the recent window agrees on the new price.
func TestClassifySubscriptionAfterPriceIncrease(t *testing.T) {
	a := Classify(occ(
		at(2026, 1, 14), -1599,
		at(2026, 2, 14), -1599,
		at(2026, 3, 14), -1799,
		at(2026, 4, 14), -1799,
		at(2026, 5, 14), -1799,
		at(2026, 6, 14), -1799,
	))
	if a.Kind != KindSubscription {
		t.Fatalf("kind = %q, want subscription after a price step", a.Kind)
	}
	if a.TypicalAmountCent != -1799 {
		t.Errorf("typical = %d, want the current price -1799", a.TypicalAmountCent)
	}
}

// The reported miss: biweekly grocery runs of roughly similar size are
// recurring spending, not a subscription.
func TestClassifyGroceryRunsAreRecurringNotSubscription(t *testing.T) {
	a := Classify(occ(
		at(2026, 3, 2), -8734,
		at(2026, 3, 16), -10250,
		at(2026, 3, 30), -9120,
		at(2026, 4, 13), -11890,
		at(2026, 4, 27), -7645,
	))
	if a.Kind != KindRecurring {
		t.Fatalf("kind = %q, want recurring (amount spread %d%%)", a.Kind, a.AmountSpreadPct)
	}
}

// Same merchant, same-ish amounts, but wildly irregular timing: not a
// subscription no matter how close the amounts are.
func TestClassifyIrregularTimingIsNotSubscription(t *testing.T) {
	a := Classify(occ(
		at(2026, 1, 3), -2000,
		at(2026, 1, 9), -2000,
		at(2026, 3, 27), -2000,
		at(2026, 4, 2), -2000,
	))
	if a.Kind == KindSubscription {
		t.Fatalf("kind = subscription, want not (interval spread %d%%)", a.IntervalSpreadPct)
	}
}

// Monthly cadence but landing on random days of the month.
func TestClassifyRandomDayOfMonthIsNotSubscription(t *testing.T) {
	a := Classify(occ(
		at(2026, 1, 3), -5000,
		at(2026, 2, 19), -5000,
		at(2026, 3, 8), -5000,
		at(2026, 4, 25), -5000,
	))
	if a.Kind == KindSubscription {
		t.Fatalf("kind = subscription, want not (day spread %d)", a.DaySpreadDays)
	}
}

// Two charges is never enough to call anything.
func TestClassifyTooFewOccurrences(t *testing.T) {
	a := Classify(occ(at(2026, 5, 1), -999, at(2026, 6, 1), -999))
	if a.Kind != KindNone {
		t.Fatalf("kind = %q, want none", a.Kind)
	}
}

// Weekly and yearly subscriptions are recognized too.
func TestClassifyOtherCadences(t *testing.T) {
	weekly := Classify(occ(
		at(2026, 6, 1), -499,
		at(2026, 6, 8), -499,
		at(2026, 6, 15), -499,
		at(2026, 6, 22), -499,
	))
	if weekly.Kind != KindSubscription || weekly.Cadence != "weekly" {
		t.Errorf("weekly: kind=%q cadence=%q", weekly.Kind, weekly.Cadence)
	}

	yearly := Classify(occ(
		at(2024, 2, 10), -9900,
		at(2025, 2, 11), -9900,
		at(2026, 2, 9), -9900,
	))
	if yearly.Kind != KindSubscription || yearly.Cadence != "yearly" {
		t.Errorf("yearly: kind=%q cadence=%q", yearly.Kind, yearly.Cadence)
	}
}

// An identical amount is the strongest signal there is, so a couple of days of
// posting drift shouldn't demote a subscription (seen with GitHub, Adobe).
func TestClassifyIdenticalAmountToleratesPostingDrift(t *testing.T) {
	a := Classify(occ(
		at(2026, 2, 3), -430,
		at(2026, 3, 9), -430,
		at(2026, 4, 2), -430,
		at(2026, 5, 8), -430,
		at(2026, 6, 4), -430,
	))
	if a.Kind != KindSubscription {
		t.Fatalf("kind = %q, want subscription (interval %d%%, day spread %d)",
			a.Kind, a.IntervalSpreadPct, a.DaySpreadDays)
	}
}

// A fixed obligation paid at erratic times is still worth surfacing — just not
// as a subscription (seen with an insurance premium and a loan payment).
func TestClassifyFixedAmountErraticTimingIsRecurring(t *testing.T) {
	a := Classify(occ(
		at(2026, 1, 5), -5075,
		at(2026, 2, 4), -5075,
		at(2026, 5, 6), -5075,
		at(2026, 6, 5), -5075,
	))
	if a.Kind != KindRecurring {
		t.Fatalf("kind = %q, want recurring (interval spread %d%%)", a.Kind, a.IntervalSpreadPct)
	}
}

// Guard against the original complaint returning: high amount variance must
// never read as a subscription, however regular the timing.
func TestClassifyVariableAmountNeverSubscription(t *testing.T) {
	a := Classify(occ(
		at(2026, 3, 1), -4700,
		at(2026, 3, 15), -21200,
		at(2026, 3, 29), -9300,
		at(2026, 4, 12), -15600,
	))
	if a.Kind == KindSubscription {
		t.Fatalf("kind = subscription, want not (amount spread %d%%)", a.AmountSpreadPct)
	}
}

func TestDayOfMonthSpreadWrapsAroundMonthEnd(t *testing.T) {
	// The 30th and the 1st are two days apart, not 29.
	got := dayOfMonthSpread([]time.Time{at(2026, 3, 30), at(2026, 5, 1), at(2026, 6, 1)})
	if got > 3 {
		t.Fatalf("spread = %d, want small (wrap-around handled)", got)
	}
}
