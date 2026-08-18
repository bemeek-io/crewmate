package categorize

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"go.uber.org/zap"

	"github.com/bemeek-io/crewmate/internal/store"
)

// LLM categorizes unrecognized merchants with Claude Haiku using structured
// outputs. Every high-confidence result is cached into merchant_rules by the
// pipeline, so each merchant is classified at most once per family.
type LLM struct {
	Client  anthropic.Client
	Log     *zap.Logger
	Enabled bool
}

func NewLLM(apiKey string, log *zap.Logger) *LLM {
	if apiKey == "" {
		log.Warn("ANTHROPIC_API_KEY not set — LLM categorization disabled, unmatched merchants will prompt the user")
		return &LLM{Enabled: false, Log: log}
	}
	// The SDK reads ANTHROPIC_API_KEY from the environment.
	return &LLM{Client: anthropic.NewClient(), Log: log, Enabled: true}
}

type LLMResult struct {
	Category   string `json:"category"`
	Confidence string `json:"confidence"` // high | low
}

// A small map of common merchant category codes to human descriptions,
// enough context for the model; unknown MCCs pass through as-is.
var mccNames = map[string]string{
	"5411": "grocery stores and supermarkets",
	"5412": "grocery stores",
	"5541": "service stations (fuel)",
	"5542": "automated fuel dispensers",
	"5812": "eating places and restaurants",
	"5813": "bars and taverns",
	"5814": "fast food restaurants",
	"5912": "drug stores and pharmacies",
	"5921": "package stores (beer, wine, liquor)",
	"5311": "department stores",
	"5310": "discount stores",
	"5331": "variety stores",
	"5300": "wholesale clubs",
	"5732": "electronics stores",
	"5942": "book stores",
	"5651": "family clothing stores",
	"5661": "shoe stores",
	"5691": "clothing stores",
	"5945": "hobby, toy and game shops",
	"5947": "gift and novelty shops",
	"5999": "miscellaneous retail",
	"4111": "local commuter transport",
	"4121": "taxis and rideshares",
	"4131": "bus lines",
	"4511": "airlines",
	"4899": "cable, satellite and streaming services",
	"4900": "utilities (electric, gas, water)",
	"4814": "telecom services",
	"4816": "computer network / internet services",
	"5817": "digital goods: applications",
	"5818": "digital goods: large digital merchants",
	"7011": "hotels and lodging",
	"7230": "beauty and barber shops",
	"7298": "health and beauty spas",
	"7832": "movie theaters",
	"7841": "video rental / streaming",
	"7997": "gyms and membership clubs",
	"8011": "doctors",
	"8021": "dentists",
	"8062": "hospitals",
	"8099": "medical services",
	"8351": "child care services",
	"8398": "charitable organizations",
	"5943": "office and school supply stores",
	"5200": "home supply warehouse stores",
	"5211": "lumber and building materials",
	"5251": "hardware stores",
	"5261": "nurseries and garden supply",
	"5964": "direct marketing - catalog merchants",
	"5968": "direct marketing - subscription merchants",
	"5967": "direct marketing - other",
	"5192": "books, periodicals and newspapers",
	"7211": "laundry and cleaning services",
	"7523": "parking lots and garages",
	"7538": "auto service shops",
	"7542": "car washes",
	"5533": "auto parts and accessories",
	"6300": "insurance",
	"8299": "educational services",
	"5735": "record and streaming music stores",
	"5941": "sporting goods stores",
	"0780": "landscaping and horticultural services",
	"5462": "bakeries",
	"5499": "misc food stores and specialty markets",
	"5977": "cosmetic stores",
	"5995": "pet shops and pet supplies",
	"0742": "veterinary services",
}

func categorySchema(categoryNames []string) map[string]any {
	enum := make([]any, 0, len(categoryNames)+1)
	for _, n := range categoryNames {
		enum = append(enum, n)
	}
	enum = append(enum, "unknown")
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"category":   map[string]any{"type": "string", "enum": enum},
			"confidence": map[string]any{"type": "string", "enum": []any{"high", "low"}},
		},
		"required":             []any{"category", "confidence"},
		"additionalProperties": false,
	}
}

// Categorize returns (categoryName, true) only for a confident classification.
// Every failure mode — disabled, API error, refusal, truncation, low
// confidence, "unknown" — degrades to (,"", false): the transaction stays
// uncategorized and the user gets the "tap to categorize" push instead.
// history is how the family has categorized this merchant before, with the
// amounts. It is the closest thing here to learning: the model is stateless
// between calls, so past decisions only inform it if they're in the prompt.
// Amounts matter because for some merchants they carry the whole signal — a
// $100 Costco charge is fuel, $12 is lunch, the rest is groceries.
func (l *LLM) Categorize(ctx context.Context, payee, mcc string, amountCents int64, categoryNames []string, history []store.MerchantExample) (string, bool) {
	if !l.Enabled || len(categoryNames) == 0 {
		return "", false
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	mccLine := "unknown"
	if mcc != "" {
		if desc, ok := mccNames[mcc]; ok {
			mccLine = fmt.Sprintf("%s (%s)", mcc, desc)
		} else {
			mccLine = mcc
		}
	}
	prompt := fmt.Sprintf(
		`Categorize this bank card transaction into one of the family's budget categories.

Merchant: %s
Merchant category code: %s
Amount: $%.2f (negative = money spent)

Available categories: %s
%s
Pick the single best category. If no category clearly fits, answer "unknown".
Use "high" confidence only when the merchant obviously belongs to the category.`,
		payee, mccLine, float64(amountCents)/100, strings.Join(categoryNames, ", "),
		historyBlock(history, categoryNames))

	msg, err := l.Client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     "claude-haiku-4-5",
		MaxTokens: 200,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
		OutputConfig: anthropic.OutputConfigParam{
			Format: anthropic.JSONOutputFormatParam{Schema: categorySchema(categoryNames)},
		},
	})
	if err != nil {
		l.Log.Warn("llm categorize failed", zap.String("merchant", payee), zap.Error(err))
		return "", false
	}
	if msg.StopReason != anthropic.StopReasonEndTurn {
		l.Log.Info("llm categorize non-end-turn", zap.String("stop_reason", string(msg.StopReason)))
		return "", false
	}
	var text string
	for _, block := range msg.Content {
		if tb, ok := block.AsAny().(anthropic.TextBlock); ok {
			text = tb.Text
			break
		}
	}
	var res LLMResult
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		l.Log.Warn("llm categorize bad json", zap.String("text", text))
		return "", false
	}
	if res.Category == "" || res.Category == "unknown" || res.Confidence != "high" {
		return "", false
	}
	return res.Category, true
}

// historyBlock renders how this merchant has been categorized before.
//
// This is the whole of the model's "memory": it holds nothing between calls,
// so the family's past decisions only reach it by being written into the
// prompt. Showing the amounts alongside is what lets it work out that a
// merchant splits by price rather than always meaning one thing.
//
// Examples naming a category the model can't pick — one excluded from
// auto-categorization, or a system category — are dropped, so it isn't shown
// answers it would be forbidden to give.
func historyBlock(history []store.MerchantExample, allowed []string) string {
	if len(history) == 0 {
		return ""
	}
	permitted := make(map[string]bool, len(allowed))
	for _, n := range allowed {
		permitted[n] = true
	}
	var b strings.Builder
	n := 0
	for _, e := range history {
		if !permitted[e.Category] {
			continue
		}
		if n == 0 {
			b.WriteString("\nHow this family has categorized this merchant before:\n")
		}
		fmt.Fprintf(&b, "- $%.2f: %s\n", math.Abs(float64(e.AmountCents))/100, e.Category)
		n++
	}
	if n == 0 {
		return ""
	}
	b.WriteString("Amounts often decide which category fits; weigh them.\n")
	return b.String()
}

// LLMSelectable returns the category names the model is allowed to choose from.
//
// System categories (Subscription, Loan Payment) are excluded: they carry
// built-in behaviour and are applied deliberately — by labelling a recurring
// series, which also records a rule so later charges match silently. Letting
// the model guess them would label one-off purchases as subscriptions and
// undercut the feature they belong to.
//
// Categories the family marked exclude_from_llm are withheld too. A catch-all
// like "Misc" is the case that matters: auto-filing something there would make
// it look handled while actually burying it, when what it needs is a person.
// Such categories stay fully usable by hand and by rules.
func LLMSelectable(cats []store.Category) []string {
	names := make([]string, 0, len(cats))
	for _, c := range cats {
		if c.SystemKey != nil || c.ExcludeFromLLM {
			continue
		}
		names = append(names, c.Name)
	}
	return names
}
