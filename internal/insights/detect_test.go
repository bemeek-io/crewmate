package insights

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bemeek-io/crewmate/internal/store"
)

// The window used throughout: Sep 2025 – Aug 2026 full, Sep 2026 in progress.
var (
	testNow    = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	testMonths = func() []string {
		var out []string
		for m := time.Date(2025, 9, 1, 0, 0, 0, 0, time.UTC); !m.After(testNow); m = m.AddDate(0, 1, 0) {
			out = append(out, m.Format("2006-01"))
		}
		return out
	}()
)

type cat struct {
	id        uuid.UUID
	name      string
	systemKey *string
}

func newCat(name string) cat { return cat{id: uuid.New(), name: name} }

// spend builds one row per month from cents, oldest first, one charge each.
func spend(merchant string, c *cat, cents ...int64) []store.MerchantMonthSpend {
	var rows []store.MerchantMonthSpend
	for i, v := range cents {
		if v == 0 {
			continue
		}
		m, _ := time.Parse("2006-01", testMonths[i])
		r := store.MerchantMonthSpend{MerchantKey: merchant, Month: m, Cents: v, Count: 1}
		if c != nil {
			id := c.id
			r.CategoryID, r.CategoryName, r.SystemKey = &id, c.name, c.systemKey
		}
		rows = append(rows, r)
	}
	return rows
}

func flat(cents int64) []int64 {
	out := make([]int64, 13)
	for i := range out {
		out[i] = cents
	}
	return out
}

func find(t *testing.T, got []Suggestion, key string) Suggestion {
	t.Helper()
	for _, s := range got {
		if s.Key() == key {
			return s
		}
	}
	t.Fatalf("no suggestion %s in %+v", key, got)
	return Suggestion{}
}

func absent(t *testing.T, got []Suggestion, key string) {
	t.Helper()
	for _, s := range got {
		if s.Key() == key {
			t.Errorf("unexpected suggestion %s: %+v", key, s)
		}
	}
}

func TestSubscriptions(t *testing.T) {
	loan := store.SystemLoanPayment
	in := Input{
		Now:    testNow,
		Months: testMonths,
		Profiles: map[string]store.MerchantProfile{
			"netflix": {MerchantKey: "netflix", Payee: "Netflix.com", MCC: "4899"},
		},
		Series: []store.RecurringSeries{
			{MerchantKey: "netflix", Kind: KindSubscription, Cadence: "monthly",
				TypicalAmountCents: 15_49, LastSeenAt: testNow.AddDate(0, 0, -10)},
			// Last charged in March: long since cancelled.
			{MerchantKey: "hulu", Kind: KindSubscription, Cadence: "monthly",
				TypicalAmountCents: 9_99, LastSeenAt: time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)},
			// Bills like a subscription, but the family labelled it a loan.
			{MerchantKey: "car", Kind: KindSubscription, Cadence: "monthly",
				TypicalAmountCents: 400_00, LastSeenAt: testNow.AddDate(0, 0, -5), LabelSystemKey: &loan},
		},
	}
	in.Rows = append(in.Rows, spend("netflix", nil, flat(15_49)...)...)
	in.Rows = append(in.Rows, spend("hulu", nil, 9_99, 9_99, 9_99, 9_99, 9_99, 9_99, 9_99)...)
	in.Rows = append(in.Rows, spend("car", nil, flat(400_00)...)...)

	got := Detect(in)
	n := find(t, got, "merchant:netflix")
	if n.Kind != KindSubscription || n.Title != "Netflix.com" {
		t.Errorf("netflix = %+v", n)
	}
	if n.SavingsCents != 12*15_49 || n.ProjectedCents != 12*15_49 {
		t.Errorf("netflix savings = %d, want %d", n.SavingsCents, 12*15_49)
	}
	// Twelve full months, not the one in progress.
	if n.Last12Cents != 12*15_49 || n.Charges != 12 {
		t.Errorf("netflix last12 = %d over %d charges", n.Last12Cents, n.Charges)
	}
	absent(t, got, "merchant:hulu")
	absent(t, got, "merchant:car")
}

func TestHabits(t *testing.T) {
	dining := newCat("Dining")
	in := Input{Now: testNow, Months: testMonths}
	// Five of the last six full months; lately $80, $90, $100. Steady
	// before that, so Dining as a whole isn't rising.
	in.Rows = append(in.Rows, spend("doordash", &dining,
		90_00, 90_00, 90_00, 90_00, 90_00, 90_00, 90_00, 0, 90_00, 80_00, 90_00, 100_00, 20_00)...)
	// Only twice in six months: not a habit.
	in.Rows = append(in.Rows, spend("steakhouse", &dining,
		0, 0, 0, 0, 0, 0, 0, 0, 120_00, 0, 0, 130_00, 0)...)
	// Steady but tiny.
	in.Rows = append(in.Rows, spend("vending", &dining, flat(5_00)...)...)

	got := Detect(in)
	d := find(t, got, "merchant:doordash")
	if d.Kind != KindHabit || d.PaceCents != 90_00 {
		t.Errorf("doordash = %+v, want habit at $90/mo", d)
	}
	if d.SavingsCents != 12*90_00 {
		t.Errorf("doordash savings = %d", d.SavingsCents)
	}
	if d.CategoryName != "Dining" {
		t.Errorf("doordash category = %q", d.CategoryName)
	}
	absent(t, got, "merchant:steakhouse")
	absent(t, got, "merchant:vending")
}

func TestHabitsNeedHistory(t *testing.T) {
	in := Input{Now: testNow, Months: testMonths}
	// Only the last five months exist at all.
	in.Rows = spend("doordash", nil, 0, 0, 0, 0, 0, 0, 0, 0, 90_00, 90_00, 90_00, 90_00, 10_00)
	if got := Detect(in); len(got) != 0 {
		t.Errorf("suggestions from five months of history: %+v", got)
	}
}

func TestRisingCategory(t *testing.T) {
	dining := newCat("Dining")
	in := Input{Now: testNow, Months: testMonths}
	// Baseline months (Dec–May) at $200, the last three at $300.
	in.Rows = append(in.Rows, spend("chipotle", &dining,
		100_00, 100_00, 100_00, 100_00, 100_00, 100_00, 100_00, 100_00, 100_00, 100_00, 100_00, 100_00, 0)...)
	in.Rows = append(in.Rows, spend("sushi", &dining,
		0, 0, 0, 100_00, 100_00, 100_00, 100_00, 100_00, 100_00, 200_00, 200_00, 200_00, 0)...)

	got := Detect(in)
	r := find(t, got, "category:"+dining.id.String())
	if r.Kind != KindRising || r.BaselineCents != 200_00 || r.PaceCents != 300_00 {
		t.Errorf("rising = %+v, want $200 → $300", r)
	}
	if r.SavingsCents != 12*100_00 {
		t.Errorf("rising savings = %d, want back-to-baseline %d", r.SavingsCents, 12*100_00)
	}
	if len(r.Drivers) != 1 || r.Drivers[0].MerchantKey != "sushi" || r.Drivers[0].DeltaCents != 100_00 {
		t.Errorf("drivers = %+v, want sushi +$100", r.Drivers)
	}
}

func TestDismissalsAndLoans(t *testing.T) {
	giving := newCat("Tithing")
	loanKey := store.SystemLoanPayment
	loan := cat{id: uuid.New(), name: "Loan Payment", systemKey: &loanKey}
	dining := newCat("Dining")
	in := Input{
		Now:    testNow,
		Months: testMonths,
		Dismissed: []store.InsightDismissal{
			{SubjectType: store.SubjectCategory, SubjectKey: giving.id.String(), Label: "Tithing"},
			{SubjectType: store.SubjectMerchant, SubjectKey: "starbucks", Label: "Starbucks"},
		},
	}
	in.Rows = append(in.Rows, spend("church", &giving, flat(500_00)...)...)
	in.Rows = append(in.Rows, spend("bank", &loan, flat(300_00)...)...)
	in.Rows = append(in.Rows, spend("starbucks", &dining, flat(60_00)...)...)
	in.Rows = append(in.Rows, spend("doordash", &dining, flat(60_00)...)...)

	got := Detect(in)
	absent(t, got, "merchant:church")
	absent(t, got, "merchant:bank")
	absent(t, got, "merchant:starbucks")
	find(t, got, "merchant:doordash")
}

func TestOrderedBySavings(t *testing.T) {
	in := Input{Now: testNow, Months: testMonths}
	in.Rows = append(in.Rows, spend("small", nil, flat(30_00)...)...)
	in.Rows = append(in.Rows, spend("big", nil, flat(90_00)...)...)
	got := Detect(in)
	if len(got) != 2 || got[0].SubjectKey != "big" {
		t.Errorf("order = %+v", got)
	}
}

type fakeJudge struct {
	results []JudgeResult
	err     error
	asked   []JudgeItem
}

func (f *fakeJudge) Available() bool { return true }
func (f *fakeJudge) JudgeSpending(_ context.Context, items []JudgeItem, _ []string) ([]JudgeResult, error) {
	f.asked = append(f.asked, items...)
	return f.results, f.err
}

func TestVerdicts(t *testing.T) {
	a := Suggestion{Kind: KindHabit, SubjectType: store.SubjectMerchant, SubjectKey: "doordash", Title: "DoorDash"}
	b := Suggestion{Kind: KindHabit, SubjectType: store.SubjectMerchant, SubjectKey: "church", Title: "First Church"}
	c := Suggestion{Kind: KindSubscription, SubjectType: store.SubjectMerchant, SubjectKey: "hbo", Title: "Max"}

	cached := map[string]store.InsightVerdict{
		// Fresh and still about the same thing: reused without asking.
		a.Key(): {Fingerprint: Fingerprint(a), Verdict: VerdictDiscretionary, Note: "Food delivery.",
			CreatedAt: testNow.Add(-time.Hour)},
		// Made about something else (it was a subscription then): asked again.
		c.Key(): {Fingerprint: "subscription|Old|", Verdict: VerdictEssential, CreatedAt: testNow.Add(-time.Hour)},
	}
	j := &fakeJudge{results: []JudgeResult{
		{ID: c.Key(), Verdict: VerdictDiscretionary, Note: "Streaming."},
		{ID: b.Key(), Verdict: "bogus"},
	}}
	got, fresh, err := Verdicts(context.Background(), []Suggestion{a, b, c}, cached, j, nil, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(j.asked) != 2 {
		t.Errorf("asked about %d items, want 2 (not the cached one)", len(j.asked))
	}
	if got[0].Verdict != VerdictDiscretionary || got[0].VerdictSource != SourceAI || got[0].Note != "Food delivery." {
		t.Errorf("cached verdict = %+v", got[0])
	}
	// An invalid answer falls back to the rules, which know a church.
	if got[1].Verdict != VerdictEssential || got[1].VerdictSource != SourceRules {
		t.Errorf("church = %+v", got[1])
	}
	if got[2].Verdict != VerdictDiscretionary || got[2].VerdictSource != SourceAI {
		t.Errorf("re-judged = %+v", got[2])
	}
	if len(fresh) != 1 || fresh[0].SubjectKey != "hbo" || fresh[0].Fingerprint != Fingerprint(c) {
		t.Errorf("fresh = %+v", fresh)
	}
}

func TestVerdictsSurviveJudgeFailure(t *testing.T) {
	s := Suggestion{Kind: KindSubscription, SubjectType: store.SubjectMerchant, SubjectKey: "x", Title: "Spotify"}
	got, fresh, err := Verdicts(context.Background(), []Suggestion{s}, nil,
		&fakeJudge{err: errors.New("down")}, nil, testNow)
	if err == nil || len(fresh) != 0 {
		t.Errorf("err = %v, fresh = %v", err, fresh)
	}
	if got[0].Verdict != VerdictDiscretionary || got[0].VerdictSource != SourceRules {
		t.Errorf("fallback = %+v", got[0])
	}
}

func TestRuleVerdict(t *testing.T) {
	cases := []struct {
		s    Suggestion
		want string
	}{
		{Suggestion{Title: "Grace Church Tithe"}, VerdictEssential},
		{Suggestion{Title: "IRS USATAXPYMT"}, VerdictEssential},
		{Suggestion{Title: "Whatever", CategoryName: "Taxes"}, VerdictEssential},
		{Suggestion{Title: "Kroger", MCC: "5411"}, VerdictEssential},
		// Not taxes, not rent.
		{Suggestion{Title: "Yellow Taxi"}, VerdictUnclear},
		{Suggestion{Title: "Parent Teacher Store"}, VerdictUnclear},
		{Suggestion{Kind: KindSubscription, Title: "Spotify"}, VerdictDiscretionary},
	}
	for _, c := range cases {
		if got := ruleVerdict(c.s); got != c.want {
			t.Errorf("ruleVerdict(%q / %q) = %s, want %s", c.s.Title, c.s.CategoryName, got, c.want)
		}
	}
}

// A merchant inside a rising category is part of that category's suggestion,
// not a second one: both would claim the same dollars.
func TestHabitFoldsIntoRisingCategory(t *testing.T) {
	home := newCat("Home Improvement")
	venmoCat := newCat("Venmo")
	in := Input{Now: testNow, Months: testMonths,
		Profiles: map[string]store.MerchantProfile{"homedepot": {Payee: "The Home Depot"}}}
	// Home Depot doubles; Lowe's stays put; both are in Home Improvement.
	in.Rows = append(in.Rows, spend("homedepot", &home,
		600_00, 600_00, 600_00, 600_00, 600_00, 600_00, 600_00, 600_00, 600_00, 1300_00, 1300_00, 1300_00, 200_00)...)
	in.Rows = append(in.Rows, spend("lowes", &home, flat(100_00)...)...)
	// A merchant with a category of its own name.
	in.Rows = append(in.Rows, spend("venmo", &venmoCat,
		300_00, 300_00, 300_00, 300_00, 300_00, 300_00, 300_00, 300_00, 300_00, 650_00, 700_00, 700_00, 100_00)...)

	got := Detect(in)
	absent(t, got, "merchant:homedepot")
	absent(t, got, "merchant:venmo")
	h := find(t, got, "category:"+home.id.String())
	if len(h.Drivers) == 0 || h.Drivers[0].Payee != "The Home Depot" {
		t.Errorf("home improvement drivers = %+v, want Home Depot first", h.Drivers)
	}
	if h.SavingsCents != 12*(1400_00-700_00) {
		t.Errorf("home improvement savings = %d, want back-to-baseline", h.SavingsCents)
	}
	find(t, got, "category:"+venmoCat.id.String())
	// Lowe's is steady and mostly in the rising category too: folded, not
	// flagged on its own.
	absent(t, got, "merchant:lowes")
	if len(got) != 2 {
		t.Errorf("want exactly the two categories, got %+v", got)
	}
}
