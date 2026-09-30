package insights

import (
	"context"
	"strings"
	"time"

	"github.com/bemeek-io/crewmate/internal/store"
)

// Verdicts on whether a flagged spend is worth suggesting cutting.
const (
	VerdictEssential     = "essential"
	VerdictDiscretionary = "discretionary"
	VerdictUnclear       = "unclear"
)

// Where a verdict came from.
const (
	SourceAI    = "ai"
	SourceRules = "rules"
)

// verdictTTL is how long a model's verdict is reused before being asked again.
const verdictTTL = 30 * 24 * time.Hour

// JudgeItem is one suggestion as the model sees it: what the spend is, never
// what to claim about it. The figures are context for the judgement; the
// model is told not to repeat them.
type JudgeItem struct {
	ID          string
	Kind        string
	Name        string
	Category    string
	MCC         string
	Cadence     string
	PerCharge   int64
	MonthlyPace int64
	Baseline    int64
	Last12      int64
	Drivers     []string
}

// JudgeResult is the model's call on one item.
type JudgeResult struct {
	ID      string
	Verdict string
	Note    string
}

// Judge decides which flagged spending looks necessary. necessities are the
// things this family has already said are necessary to them — the best
// evidence of what they'd say about something similar.
type Judge interface {
	Available() bool
	JudgeSpending(ctx context.Context, items []JudgeItem, necessities []string) ([]JudgeResult, error)
}

// Judged is a suggestion with the verdict on it.
type Judged struct {
	Suggestion
	Verdict       string `json:"verdict"`
	Note          string `json:"note"`
	VerdictSource string `json:"verdict_source"`
}

// Verdicts attaches a verdict to every suggestion: a cached one while it is
// fresh, otherwise the judge's, otherwise the rules'. It returns the judged
// suggestions and any fresh verdicts worth caching.
//
// A failing judge never fails the report. The suggestions stand on their own
// numbers; the verdict only decides which are shown first.
func Verdicts(ctx context.Context, sugg []Suggestion, cached map[string]store.InsightVerdict,
	judge Judge, necessities []string, now time.Time) ([]Judged, []store.InsightVerdict, error) {

	out := make([]Judged, len(sugg))
	var pending []int
	for i, s := range sugg {
		out[i] = Judged{Suggestion: s}
		if v, ok := cached[s.Key()]; ok && v.Fingerprint == Fingerprint(s) && now.Sub(v.CreatedAt) < verdictTTL {
			out[i].Verdict, out[i].Note, out[i].VerdictSource = v.Verdict, v.Note, SourceAI
			continue
		}
		pending = append(pending, i)
	}

	var fresh []store.InsightVerdict
	var judgeErr error
	if len(pending) > 0 && judge != nil && judge.Available() {
		items := make([]JudgeItem, 0, len(pending))
		for _, i := range pending {
			items = append(items, judgeItem(out[i].Suggestion))
		}
		results, err := judge.JudgeSpending(ctx, items, necessities)
		judgeErr = err
		byID := make(map[string]JudgeResult, len(results))
		for _, r := range results {
			byID[r.ID] = r
		}
		for _, i := range pending {
			r, ok := byID[out[i].Key()]
			if !ok || !validVerdict(r.Verdict) {
				continue
			}
			out[i].Verdict, out[i].Note, out[i].VerdictSource = r.Verdict, r.Note, SourceAI
			fresh = append(fresh, store.InsightVerdict{
				SubjectType: out[i].SubjectType,
				SubjectKey:  out[i].SubjectKey,
				Fingerprint: Fingerprint(out[i].Suggestion),
				Verdict:     r.Verdict,
				Note:        r.Note,
			})
		}
	}
	for i := range out {
		if out[i].Verdict == "" {
			out[i].Verdict = ruleVerdict(out[i].Suggestion)
			out[i].VerdictSource = SourceRules
		}
	}
	return out, fresh, judgeErr
}

func validVerdict(v string) bool {
	return v == VerdictEssential || v == VerdictDiscretionary || v == VerdictUnclear
}

func judgeItem(s Suggestion) JudgeItem {
	it := JudgeItem{
		ID:          s.Key(),
		Kind:        s.Kind,
		Name:        s.Title,
		Category:    s.CategoryName,
		MCC:         s.MCC,
		Cadence:     s.Cadence,
		PerCharge:   s.PerChargeCents,
		MonthlyPace: s.PaceCents,
		Baseline:    s.BaselineCents,
		Last12:      s.Last12Cents,
	}
	for _, d := range s.Drivers {
		it.Drivers = append(it.Drivers, d.Payee)
	}
	return it
}

// Merchant category codes for spending that is almost never optional.
var essentialMCC = map[string]bool{
	"4900": true, // utilities
	"4814": true, // telecom
	"5411": true, // groceries
	"5412": true,
	"5541": true, // fuel
	"5542": true,
	"5912": true, // pharmacy
	"6300": true, // insurance
	"8011": true, // doctors
	"8021": true, // dentists
	"8062": true, // hospitals
	"8099": true, // medical
	"8351": true, // child care
	"8398": true, // charitable giving
	"9311": true, // tax payments
	"9211": true, // court costs
	"9222": true, // fines
	"9399": true, // government services
}

// Words in a merchant or category name that mark it as a necessity. Matched
// against whole words, by prefix — "tith" catches tithe and tithing — so
// "tax" never matches taxi and "rent" never matches parent.
var essentialPrefixes = []string{
	"tith", "church", "offering", "donation", "charit", "ministr",
	"mortgage", "insur", "utilit", "medic", "doctor", "dental", "dentist",
	"pharma", "hospital", "clinic", "childcare", "daycare", "tuition", "grocer",
}

// Words that only count whole.
var essentialWords = map[string]bool{
	"tax": true, "taxes": true, "irs": true, "rent": true, "loan": true,
}

func essentialName(name string) bool {
	words := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !(r >= 'a' && r <= 'z')
	})
	for _, w := range words {
		if essentialWords[w] {
			return true
		}
		for _, p := range essentialPrefixes {
			if strings.HasPrefix(w, p) {
				return true
			}
		}
	}
	return false
}

// ruleVerdict is the judgement without a model: codes and names that mark a
// necessity, and everything else left for the family to decide.
func ruleVerdict(s Suggestion) string {
	if essentialMCC[s.MCC] {
		return VerdictEssential
	}
	if essentialName(s.Title) || essentialName(s.CategoryName) {
		return VerdictEssential
	}
	if s.Kind == KindSubscription {
		return VerdictDiscretionary
	}
	return VerdictUnclear
}
