package categorize

import (
	"testing"

	"github.com/bemeek-io/crewmate/internal/store"
)

func key(s string) *string { return &s }

// Subscription and Loan Payment carry built-in behaviour and are applied by
// labelling a recurring series, which also records a rule so later charges
// match silently and without a notification. If the model could pick them, a
// one-off purchase would be filed as a subscription and the recurring feature
// would be reporting things nobody labelled.
func TestLLMCannotSelectSystemCategories(t *testing.T) {
	cats := []store.Category{
		{Name: "Groceries"},
		{Name: "Subscription", SystemKey: key(store.SystemSubscription)},
		{Name: "Dining"},
		{Name: "Loan Payment", SystemKey: key(store.SystemLoanPayment)},
	}

	got := LLMSelectable(cats)
	want := []string{"Groceries", "Dining"}
	if len(got) != len(want) {
		t.Fatalf("LLMSelectable = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("LLMSelectable = %v, want %v", got, want)
		}
	}

	// The schema is the hard constraint on the model's answer, so the system
	// names must not appear in its enum either.
	schema := categorySchema(got)
	props := schema["properties"].(map[string]any)
	enum := props["category"].(map[string]any)["enum"].([]any)
	for _, v := range enum {
		switch v.(string) {
		case "Subscription", "Loan Payment":
			t.Errorf("system category %q is selectable in the schema enum", v)
		}
	}
	// "unknown" must survive — it's how the model declines, which routes the
	// transaction to a tap-to-categorize push instead of a wrong guess.
	var hasUnknown bool
	for _, v := range enum {
		if v.(string) == "unknown" {
			hasUnknown = true
		}
	}
	if !hasUnknown {
		t.Error(`schema enum lost "unknown"`)
	}
}

// A family that has only system categories leaves nothing to choose from, and
// the model must not be asked at all rather than be handed an empty enum.
func TestLLMSelectableEmptyWhenOnlySystem(t *testing.T) {
	cats := []store.Category{
		{Name: "Subscription", SystemKey: key(store.SystemSubscription)},
		{Name: "Loan Payment", SystemKey: key(store.SystemLoanPayment)},
	}
	if got := LLMSelectable(cats); len(got) != 0 {
		t.Errorf("LLMSelectable = %v, want empty", got)
	}
}
