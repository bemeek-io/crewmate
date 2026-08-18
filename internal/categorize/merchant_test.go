package categorize

import (
	"strings"
	"testing"

	"github.com/bemeek-io/crewmate/internal/store"
)

func ex(category string, cents int64) store.MerchantExample {
	return store.MerchantExample{Category: category, AmountCents: cents}
}

// Copying a merchant's last category onto its next transaction is right for a
// merchant that means one thing and wrong for one where the amount decides.
// Costco is the second kind: ~$100 is fuel, small is lunch, the rest is
// groceries. Answering from history there is a coin flip, and worse, it runs
// before the model — so the one component that could read the amount is never
// consulted.
func TestConsistentCategory(t *testing.T) {
	cases := []struct {
		name    string
		history []store.MerchantExample
		want    string
		wantOK  bool
	}{
		{
			"settled merchant",
			[]store.MerchantExample{ex("Subscriptions", -1599), ex("Subscriptions", -1599), ex("Subscriptions", -1599)},
			"Subscriptions", true,
		},
		{
			"amount decides — must reach the model",
			[]store.MerchantExample{ex("Gas", -10000), ex("Dining", -1240), ex("Groceries", -18732)},
			"", false,
		},
		{
			// One or two decisions shouldn't dictate every future one.
			"too few to be a habit",
			[]store.MerchantExample{ex("Groceries", -4210), ex("Groceries", -3990)},
			"", false,
		},
		{"no history", nil, "", false},
	}
	for _, c := range cases {
		got, ok := ConsistentCategory(c.history)
		if got != c.want || ok != c.wantOK {
			t.Errorf("%s: ConsistentCategory = (%q, %v), want (%q, %v)",
				c.name, got, ok, c.want, c.wantOK)
		}
	}
}

// The model holds nothing between calls, so past decisions only reach it by
// being in the prompt — with amounts, which is what makes the Costco pattern
// inferable at all.
func TestHistoryBlock(t *testing.T) {
	allowed := []string{"Gas", "Dining", "Groceries"}
	history := []store.MerchantExample{
		ex("Gas", -10000),
		ex("Dining", -1240),
		// Excluded from the model's choices, so showing it would be teaching
		// an answer it isn't allowed to give.
		ex("Misc", -5000),
	}
	block := historyBlock(history, allowed)

	for _, want := range []string{"$100.00: Gas", "$12.40: Dining"} {
		if !strings.Contains(block, want) {
			t.Errorf("history block missing %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, "Misc") {
		t.Errorf("history block offers a category the model can't pick:\n%s", block)
	}
	// Amounts are rendered positive; a leading minus reads as a category that
	// somehow cost negative money.
	if strings.Contains(block, "$-") {
		t.Errorf("amounts should be rendered as magnitudes:\n%s", block)
	}

	if historyBlock(nil, allowed) != "" {
		t.Error("no history should produce no block")
	}
	// Every example unusable is the same as having none.
	if historyBlock([]store.MerchantExample{ex("Misc", -1)}, allowed) != "" {
		t.Error("history with no permitted examples should produce no block")
	}
}
