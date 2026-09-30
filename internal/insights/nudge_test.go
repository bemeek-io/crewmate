package insights

import (
	"strings"
	"testing"
	"time"

	"github.com/bemeek-io/crewmate/internal/store"
)

func TestMatch(t *testing.T) {
	sugg := []Suggestion{
		{Kind: KindRising, SubjectType: store.SubjectCategory, SubjectKey: "c1", Title: "Dining", CategoryName: "Dining"},
		{Kind: KindHabit, SubjectType: store.SubjectMerchant, SubjectKey: "doordash", Title: "DoorDash", CategoryName: "Dining"},
	}
	// The merchant's own suggestion beats the category it's filed under.
	if m := Match(sugg, "doordash", "Dining"); m == nil || m.SubjectKey != "doordash" {
		t.Errorf("doordash matched %+v", m)
	}
	// Any other purchase in a rising category matches the category.
	if m := Match(sugg, "sushi", "dining"); m == nil || m.SubjectKey != "c1" {
		t.Errorf("sushi matched %+v", m)
	}
	if m := Match(sugg, "sushi", ""); m != nil {
		t.Errorf("uncategorized purchase matched %+v", m)
	}
	if m := Match(sugg, "kroger", "Groceries"); m != nil {
		t.Errorf("unrelated purchase matched %+v", m)
	}
}

func TestVerdictFor(t *testing.T) {
	s := Suggestion{Kind: KindHabit, SubjectType: store.SubjectMerchant, SubjectKey: "x", Title: "Grace Church"}
	if v := VerdictFor(s, nil, testNow); v != VerdictEssential {
		t.Errorf("uncached church = %s, want the rules' essential", v)
	}
	cached := map[string]store.InsightVerdict{s.Key(): {
		Fingerprint: Fingerprint(s), Verdict: VerdictDiscretionary, CreatedAt: testNow.Add(-time.Hour)}}
	if v := VerdictFor(s, cached, testNow); v != VerdictDiscretionary {
		t.Errorf("cached = %s, want the model's verdict", v)
	}
	if v := VerdictFor(s, cached, testNow.Add(40*24*time.Hour)); v != VerdictEssential {
		t.Errorf("expired cache = %s, want the rules again", v)
	}
}

func TestBuildNudge(t *testing.T) {
	sub := Suggestion{Kind: KindSubscription, SubjectType: store.SubjectMerchant, SubjectKey: "netflix com",
		Title: "Netflix", Last12Cents: 185_88, SavingsCents: 185_88}
	n := BuildNudge(sub, -15_49, "NETFLIX.COM", "")
	if n.Title != "NETFLIX.COM charged $15.49" {
		t.Errorf("title = %q", n.Title)
	}
	if n.Body != "That's $186 over the last 12 months. Cancelling would save about $186 over the next 12." {
		t.Errorf("body = %q", n.Body)
	}
	if n.URL != "/cashflow?insight=merchant%3Anetflix+com" {
		t.Errorf("url = %q", n.URL)
	}

	habit := Suggestion{Kind: KindHabit, SubjectType: store.SubjectMerchant, SubjectKey: "doordash",
		Title: "DoorDash", Last12Cents: 1127_00, PaceCents: 170_00, SavingsCents: 2040_00}
	n = BuildNudge(habit, -23_40, "DoorDash", "Dining")
	for _, want := range []string{"$1,127 over the last 12 months", "$170 a month", "about $1,020 a year", "Filed under Dining."} {
		if !strings.Contains(n.Body, want) {
			t.Errorf("habit body %q is missing %q", n.Body, want)
		}
	}
	if n.Title != "$23.40 at DoorDash" {
		t.Errorf("habit title = %q", n.Title)
	}

	rising := Suggestion{Kind: KindRising, SubjectType: store.SubjectCategory, SubjectKey: "c1",
		Title: "Dining", CategoryName: "Dining", PaceCents: 473_00, BaselineCents: 305_00, SavingsCents: 2020_00}
	n = BuildNudge(rising, -48_00, "Sushi Zen", "Dining")
	if n.Body != "Dining is up 55% to $473 a month. Getting back to $305 a month would save about $2,020 a year." {
		t.Errorf("rising body = %q", n.Body)
	}
}
