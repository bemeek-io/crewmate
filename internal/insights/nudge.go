package insights

import (
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/bemeek-io/crewmate/internal/store"
)

// NudgeCooldown is the least time between two nudges about one suggestion.
const NudgeCooldown = 24 * time.Hour

// Match finds the suggestion a new purchase belongs to: its merchant's own
// suggestion first, as the more specific, then a rising category it was
// filed under. category is the name the purchase was filed under, if any.
func Match(sugg []Suggestion, merchantKey, category string) *Suggestion {
	for i := range sugg {
		if sugg[i].SubjectType == store.SubjectMerchant && sugg[i].SubjectKey == merchantKey {
			return &sugg[i]
		}
	}
	if category == "" {
		return nil
	}
	for i := range sugg {
		if sugg[i].Kind == KindRising && strings.EqualFold(sugg[i].CategoryName, category) {
			return &sugg[i]
		}
	}
	return nil
}

// VerdictFor is what the page would say about s right now, without asking a
// model: a fresh cached verdict when there is one, the rules otherwise. The
// push path never waits on a model.
func VerdictFor(s Suggestion, cached map[string]store.InsightVerdict, now time.Time) string {
	if v, ok := cached[s.Key()]; ok && v.Fingerprint == Fingerprint(s) && now.Sub(v.CreatedAt) < verdictTTL {
		return v.Verdict
	}
	return ruleVerdict(s)
}

// Nudge is the push for a purchase matching a suggestion.
type Nudge struct {
	Title string
	Body  string
	URL   string
}

// SuggestionURL opens Cash flow at one suggestion.
func SuggestionURL(s Suggestion) string {
	return "/cashflow?insight=" + url.QueryEscape(s.Key())
}

func money(cents int64) string {
	d := math.Abs(float64(cents)) / 100
	if d >= 100 {
		return "$" + commas(int64(math.Round(d)))
	}
	return fmt.Sprintf("$%.2f", d)
}

func wholeDollars(cents int64) string {
	return "$" + commas(int64(math.Round(math.Abs(float64(cents))/100)))
}

func commas(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// BuildNudge words the push. Every figure in it comes from s, which is the
// same suggestion the link opens, so the page backs up what the push claims.
// filedUnder is the category the purchase was just auto-filed under, if any —
// the ordinary push would have said so, and this one replaces it.
func BuildNudge(s Suggestion, amountCents int64, payee, filedUnder string) Nudge {
	if payee == "" {
		payee = s.Title
	}
	n := Nudge{URL: SuggestionURL(s)}
	switch s.Kind {
	case KindSubscription:
		n.Title = fmt.Sprintf("%s charged %s", payee, money(amountCents))
		n.Body = fmt.Sprintf("That's %s over the last 12 months. Cancelling would save about %s over the next 12.",
			wholeDollars(s.Last12Cents), wholeDollars(s.SavingsCents))
	case KindHabit:
		n.Title = fmt.Sprintf("%s at %s", money(amountCents), payee)
		n.Body = fmt.Sprintf("%s has cost %s over the last 12 months, about %s a month lately. Cutting it in half would save about %s a year.",
			s.Title, wholeDollars(s.Last12Cents), wholeDollars(s.PaceCents), wholeDollars(s.SavingsCents/2))
	case KindRising:
		pct := 0
		if s.BaselineCents > 0 {
			pct = int(math.Round(float64(s.PaceCents-s.BaselineCents) / float64(s.BaselineCents) * 100))
		}
		n.Title = fmt.Sprintf("%s at %s", money(amountCents), payee)
		n.Body = fmt.Sprintf("%s is up %d%% to %s a month. Getting back to %s a month would save about %s a year.",
			s.Title, pct, wholeDollars(s.PaceCents), wholeDollars(s.BaselineCents), wholeDollars(s.SavingsCents))
	}
	if filedUnder != "" && !(s.Kind == KindRising && strings.EqualFold(filedUnder, s.Title)) {
		n.Body += fmt.Sprintf(" Filed under %s.", filedUnder)
	}
	return n
}
