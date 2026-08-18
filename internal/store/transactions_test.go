package store

import (
	"testing"

	"github.com/google/uuid"
)

// Recategorizing replaces categories, including the built-in ones — that's the
// point of splitting a new category out of an existing one. What it must never
// replace is a note a person typed into Crew, which isn't a category at all.
func TestReplaceable(t *testing.T) {
	catID := uuid.New()
	cases := []struct {
		name string
		txn  Transaction
		want bool
	}{
		{"no note at all", Transaction{Note: ""}, true},
		{
			"note naming a category",
			Transaction{Note: "Loan Payment", CategoryID: &catID},
			true,
		},
		{
			// e.g. "reimburse Dave" — bulk actions leave it alone.
			"hand-written note",
			Transaction{Note: "reimburse Dave"},
			false,
		},
	}
	for _, c := range cases {
		if got := c.txn.Replaceable(); got != c.want {
			t.Errorf("%s: Replaceable() = %v, want %v", c.name, got, c.want)
		}
	}
}
