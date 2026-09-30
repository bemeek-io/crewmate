package categorize

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"

	"github.com/bemeek-io/crewmate/internal/insights"
)

// insightsModel judges flagged spending. It's a different job from
// categorizing one charge: weighing a family's whole list of candidates
// against what they've said matters to them, and saying why in a sentence.
// It runs rarely — verdicts are cached for a month — so the stronger model
// costs little.
const insightsModel = "claude-opus-5-5"

// judgeBatch bounds one request; a family rarely has this many candidates.
const judgeBatch = 40

const judgeSystem = `You help a family find spending they could cut back on, so they can put the money toward things that matter more to them.

Code has already flagged some spending patterns and worked out every figure. For each one, decide whether it is worth suggesting they cut:

- "essential": a need rather than a want. Housing, utilities, insurance, groceries, fuel, medical and dental care, childcare, education, taxes, debt payments, and religious giving, tithing or charity. Also anything like what the family has told you is necessary to them.
- "discretionary": a want they could reasonably cut or reduce, such as streaming, dining out, delivery, coffee, entertainment, hobbies, shopping, or subscriptions that overlap each other.
- "unclear": you can't tell from the name what the spending is.

For each item, also write a note: one plain sentence, at most 25 words, that helps them decide. Say what the charge appears to be, point out overlap with another flagged item (two streaming services, say), or name the merchants behind a category's rise. For essential items, say briefly why it looks necessary.

Never state dollar amounts, percentages or counts in the note. The app shows exact figures beside it, and a number in your note could contradict them. Don't lecture or moralize; it's their money.

Return one entry for every item, using its id exactly as given.`

func judgeSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id": map[string]any{"type": "string"},
						"verdict": map[string]any{"type": "string", "enum": []any{
							insights.VerdictEssential, insights.VerdictDiscretionary, insights.VerdictUnclear,
						}},
						"note": map[string]any{"type": "string"},
					},
					"required":             []any{"id", "verdict", "note"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []any{"items"},
		"additionalProperties": false,
	}
}

// Available reports whether a model is configured.
func (l *LLM) Available() bool { return l != nil && l.Enabled }

// JudgeSpending asks the model which flagged spending looks necessary.
func (l *LLM) JudgeSpending(ctx context.Context, items []insights.JudgeItem, necessities []string) ([]insights.JudgeResult, error) {
	if !l.Available() {
		return nil, errors.New("no model configured")
	}
	var out []insights.JudgeResult
	for start := 0; start < len(items); start += judgeBatch {
		end := min(start+judgeBatch, len(items))
		res, err := l.judgeBatch(ctx, items[start:end], necessities)
		if err != nil {
			return out, err
		}
		out = append(out, res...)
	}
	return out, nil
}

func (l *LLM) judgeBatch(ctx context.Context, items []insights.JudgeItem, necessities []string) ([]insights.JudgeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	msg, err := l.Client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:     insightsModel,
		MaxTokens: 16000,
		System:    []anthropic.BetaTextBlockParam{{Text: judgeSystem}},
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(judgePrompt(items, necessities))),
		},
		OutputConfig: anthropic.BetaOutputConfigParam{
			// Judging a short list: medium is plenty.
			Effort: anthropic.BetaOutputConfigEffortMedium,
			Format: anthropic.BetaJSONOutputFormatParam{Schema: judgeSchema()},
		},
		// If a safety classifier declines, let the API re-serve the request
		// on a fallback model rather than return nothing.
		Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
		Fallbacks: anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()},
	})
	if err != nil {
		return nil, fmt.Errorf("judge spending: %w", err)
	}
	if msg.StopReason != anthropic.BetaStopReasonEndTurn {
		return nil, fmt.Errorf("judge spending: stopped with %q", msg.StopReason)
	}
	var text string
	for _, block := range msg.Content {
		if tb, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			text = tb.Text
			break
		}
	}
	var parsed struct {
		Items []struct {
			ID      string `json:"id"`
			Verdict string `json:"verdict"`
			Note    string `json:"note"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return nil, fmt.Errorf("judge spending: bad json: %w", err)
	}
	out := make([]insights.JudgeResult, 0, len(parsed.Items))
	for _, it := range parsed.Items {
		out = append(out, insights.JudgeResult{ID: it.ID, Verdict: it.Verdict, Note: strings.TrimSpace(it.Note)})
	}
	return out, nil
}

func dollars(cents int64) string {
	return fmt.Sprintf("$%.0f", math.Round(float64(cents)/100))
}

// judgePrompt lists the flagged items. The figures give the model a sense of
// scale; it's told not to repeat them.
func judgePrompt(items []insights.JudgeItem, necessities []string) string {
	var b strings.Builder
	if len(necessities) > 0 {
		b.WriteString("The family has told us these are necessary to them, and not to flag them:\n")
		for _, n := range necessities {
			fmt.Fprintf(&b, "- %s\n", n)
		}
		b.WriteString("\n")
	}
	b.WriteString("Flagged spending:\n")
	for _, it := range items {
		fmt.Fprintf(&b, "\nid: %s\n", it.ID)
		switch it.Kind {
		case insights.KindSubscription:
			fmt.Fprintf(&b, "type: active subscription, billed %s at %s\n", it.Cadence, dollars(it.PerCharge))
		case insights.KindHabit:
			fmt.Fprintf(&b, "type: merchant paid most months, lately about %s a month\n", dollars(it.MonthlyPace))
		case insights.KindRising:
			fmt.Fprintf(&b, "type: spending category that has risen from about %s to %s a month\n",
				dollars(it.Baseline), dollars(it.MonthlyPace))
		}
		fmt.Fprintf(&b, "name: %s\n", it.Name)
		if it.Category != "" && it.Category != it.Name {
			fmt.Fprintf(&b, "filed under: %s\n", it.Category)
		}
		if it.MCC != "" {
			if desc, ok := mccNames[it.MCC]; ok {
				fmt.Fprintf(&b, "merchant type: %s\n", desc)
			}
		}
		if len(it.Drivers) > 0 {
			fmt.Fprintf(&b, "most of the rise came from: %s\n", strings.Join(it.Drivers, ", "))
		}
		fmt.Fprintf(&b, "last 12 months: %s\n", dollars(it.Last12))
	}
	return b.String()
}
