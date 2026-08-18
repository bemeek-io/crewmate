package categorize

import "github.com/bemeek-io/crewmate/internal/store"

const (
	// merchantHistoryLimit bounds how much of a merchant's past is considered.
	merchantHistoryLimit = 12
	// consistentMinSamples is the fewest decisions that can establish a habit.
	// Below this, one categorization would dictate every future one.
	consistentMinSamples = 3
)

// ConsistentCategory reports the category a merchant is always filed under,
// if there is one.
//
// Applying a merchant's last category to its next transaction is right for a
// merchant that means one thing — Netflix is always Subscriptions — and wrong
// for one that doesn't. At Costco the amount decides between fuel, lunch and
// groceries, so copying last time's answer is a coin flip, and doing it before
// consulting the model means the one component that could read the amount
// never gets asked.
//
// So this only answers when the history actually agrees. A merchant with mixed
// history falls through to the model, which is given the same examples.
func ConsistentCategory(history []store.MerchantExample) (string, bool) {
	if len(history) < consistentMinSamples {
		return "", false
	}
	first := history[0].Category
	for _, e := range history {
		if e.Category != first {
			return "", false
		}
	}
	return first, true
}
