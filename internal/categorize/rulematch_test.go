package categorize

import (
	"testing"

	"github.com/bemeek-io/crewmate/internal/store"
)

func cents(v int64) *int64 { return &v }

func rule(mod func(*store.CategoryRule)) store.CategoryRule {
	r := store.CategoryRule{Enabled: true, MatchType: "contains", Direction: "any"}
	mod(&r)
	return r
}

func TestMatchPayeeAndAmountRange(t *testing.T) {
	r := rule(func(r *store.CategoryRule) {
		r.PayeeMatch = "target"
		r.MinAmountCents = cents(2000)
		r.MaxAmountCents = cents(5000)
	})
	inRange := Candidate{Payee: "TARGET #1234", MerchantKey: "target", AmountCents: -3000}
	if !MatchRule(r, inRange) {
		t.Error("expected match inside the amount range")
	}
	if MatchRule(r, Candidate{Payee: "Target", MerchantKey: "target", AmountCents: -100}) {
		t.Error("below the minimum should not match")
	}
	if MatchRule(r, Candidate{Payee: "Target", MerchantKey: "target", AmountCents: -9000}) {
		t.Error("above the maximum should not match")
	}
	if MatchRule(r, Candidate{Payee: "Costco", MerchantKey: "costco", AmountCents: -3000}) {
		t.Error("different vendor should not match")
	}
}

// Bounds are magnitudes, so a spend of -$30 sits inside "between $20 and $50".
func TestMatchAmountUsesMagnitude(t *testing.T) {
	r := rule(func(r *store.CategoryRule) {
		r.MinAmountCents = cents(2000)
		r.MaxAmountCents = cents(5000)
	})
	if !MatchRule(r, Candidate{AmountCents: -3000}) {
		t.Error("negative spend should match a positive range")
	}
	if !MatchRule(r, Candidate{AmountCents: 3000}) {
		t.Error("positive amount should match too when direction is any")
	}
}

func TestMatchDirection(t *testing.T) {
	spend := rule(func(r *store.CategoryRule) { r.Direction = "spend" })
	if !MatchRule(spend, Candidate{AmountCents: -500}) {
		t.Error("spend rule should match a debit")
	}
	if MatchRule(spend, Candidate{AmountCents: 500}) {
		t.Error("spend rule should not match a credit")
	}
	income := rule(func(r *store.CategoryRule) { r.Direction = "income" })
	if !MatchRule(income, Candidate{AmountCents: 500}) {
		t.Error("income rule should match a credit")
	}
}

func TestMatchTypes(t *testing.T) {
	c := Candidate{Payee: "Sonic Drive-In", MerchantKey: "sonic drive-in"}
	exact := rule(func(r *store.CategoryRule) {
		r.PayeeMatch = "sonic drive-in"
		r.MatchType = "equals"
	})
	if !MatchRule(exact, c) {
		t.Error("equals should match the normalized merchant key")
	}
	prefix := rule(func(r *store.CategoryRule) {
		r.PayeeMatch = "sonic"
		r.MatchType = "prefix"
	})
	if !MatchRule(prefix, c) {
		t.Error("prefix should match")
	}
	if MatchRule(rule(func(r *store.CategoryRule) {
		r.PayeeMatch = "drive"
		r.MatchType = "prefix"
	}), c) {
		t.Error("prefix should not match mid-string")
	}
}

func TestUnsetConditionsAlwaysPass(t *testing.T) {
	// A rule with only a category set matches everything — useful as a
	// catch-all at the lowest priority.
	if !MatchRule(rule(func(*store.CategoryRule) {}), Candidate{Payee: "anything", AmountCents: -1}) {
		t.Error("empty rule should match")
	}
}

func TestDisabledRuleNeverMatches(t *testing.T) {
	r := rule(func(r *store.CategoryRule) { r.Enabled = false })
	if MatchRule(r, Candidate{AmountCents: -100}) {
		t.Error("disabled rule should not match")
	}
}

func TestFirstMatchRespectsOrder(t *testing.T) {
	specific := rule(func(r *store.CategoryRule) {
		r.PayeeMatch = "target"
		r.MaxAmountCents = cents(1000)
	})
	catchAll := rule(func(r *store.CategoryRule) { r.PayeeMatch = "target" })
	rules := []store.CategoryRule{specific, catchAll}

	small := Candidate{Payee: "Target", MerchantKey: "target", AmountCents: -500}
	if got := FirstMatch(rules, small); got == nil || got.MaxAmountCents == nil {
		t.Error("the more specific rule should win when it matches")
	}
	big := Candidate{Payee: "Target", MerchantKey: "target", AmountCents: -5000}
	if got := FirstMatch(rules, big); got == nil || got.MaxAmountCents != nil {
		t.Error("should fall through to the catch-all")
	}
	if FirstMatch(rules, Candidate{Payee: "Costco", MerchantKey: "costco"}) != nil {
		t.Error("no rule should match a different vendor")
	}
}
