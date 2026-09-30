package categorize

import (
	"strings"
	"testing"

	"github.com/bemeek-io/crewmate/internal/insights"
)

// The prompt carries what the model needs to judge — the ids to answer with,
// what the family has said is necessary, and what each item is.
func TestJudgePrompt(t *testing.T) {
	p := judgePrompt([]insights.JudgeItem{
		{ID: "merchant:netflix", Kind: insights.KindSubscription, Name: "Netflix", Cadence: "monthly",
			PerCharge: 15_49, MCC: "4899", Last12: 185_88},
		{ID: "category:abc", Kind: insights.KindRising, Name: "Dining", Category: "Dining",
			Baseline: 200_00, MonthlyPace: 300_00, Drivers: []string{"Sushi Place"}},
	}, []string{"Tithing"})

	for _, want := range []string{
		"id: merchant:netflix",
		"billed monthly at $15",
		"cable, satellite and streaming services",
		"id: category:abc",
		"from about $200 to $300 a month",
		"most of the rise came from: Sushi Place",
		"- Tithing",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt is missing %q:\n%s", want, p)
		}
	}
	// A category's own name isn't repeated as its filing.
	if strings.Contains(p, "filed under: Dining") {
		t.Errorf("prompt repeats the category as its own filing:\n%s", p)
	}
}
