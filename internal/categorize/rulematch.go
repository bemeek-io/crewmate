package categorize

import (
	"strings"

	"github.com/bemeek-io/crewmate/internal/store"
)

// Candidate is the transaction data a rule is evaluated against.
type Candidate struct {
	Payee       string
	MerchantKey string
	MCC         string
	AmountCents int64
}

// MatchRule reports whether every condition a rule sets is satisfied. Unset
// conditions (empty strings, nil bounds, direction "any") always pass, so a
// rule is the AND of whatever the user actually specified.
func MatchRule(r store.CategoryRule, c Candidate) bool {
	if !r.Enabled {
		return false
	}
	if r.PayeeMatch != "" && !matchesPayee(r, c) {
		return false
	}
	if r.MCC != "" && !strings.EqualFold(strings.TrimSpace(r.MCC), c.MCC) {
		return false
	}
	switch r.Direction {
	case "spend":
		if c.AmountCents >= 0 {
			return false
		}
	case "income":
		if c.AmountCents <= 0 {
			return false
		}
	}
	// Bounds are expressed as people say them ("between $20 and $50"), so they
	// compare against the magnitude regardless of sign.
	amt := c.AmountCents
	if amt < 0 {
		amt = -amt
	}
	if r.MinAmountCents != nil && amt < *r.MinAmountCents {
		return false
	}
	if r.MaxAmountCents != nil && amt > *r.MaxAmountCents {
		return false
	}
	return true
}

func matchesPayee(r store.CategoryRule, c Candidate) bool {
	needle := strings.ToLower(strings.TrimSpace(r.PayeeMatch))
	if needle == "" {
		return true
	}
	// Compare against both the raw payee and the normalized merchant key, so a
	// rule written as "target" still matches "TARGET #1234".
	haystacks := []string{strings.ToLower(strings.TrimSpace(c.Payee)), c.MerchantKey}
	for _, h := range haystacks {
		if h == "" {
			continue
		}
		switch r.MatchType {
		case "equals":
			if h == needle {
				return true
			}
		case "prefix":
			if strings.HasPrefix(h, needle) {
				return true
			}
		default: // contains
			if strings.Contains(h, needle) {
				return true
			}
		}
	}
	return false
}

// FirstMatch returns the first rule matching a transaction. Rules arrive in
// evaluation order (priority, then age), so the first hit wins.
func FirstMatch(rules []store.CategoryRule, c Candidate) *store.CategoryRule {
	for i := range rules {
		if MatchRule(rules[i], c) {
			return &rules[i]
		}
	}
	return nil
}
