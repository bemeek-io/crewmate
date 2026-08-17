package categoriesapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bemeek-io/crewmate/internal/family"
	"github.com/bemeek-io/crewmate/internal/httpx"
	"github.com/bemeek-io/crewmate/internal/store"
)

func ruleJSON(r store.CategoryRule) map[string]any {
	return map[string]any{
		"id":               r.ID,
		"category_id":      r.CategoryID,
		"category_name":    r.CategoryName,
		"category_color":   r.CategoryColor,
		"priority":         r.Priority,
		"payee_match":      r.PayeeMatch,
		"match_type":       r.MatchType,
		"mcc":              r.MCC,
		"min_amount_cents": r.MinAmountCents,
		"max_amount_cents": r.MaxAmountCents,
		"direction":        r.Direction,
		"enabled":          r.Enabled,
		"source":           r.Source,
	}
}

// ListRules handles GET /api/rules.
func (h *Handlers) ListRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.Store.ListRules(r.Context(), family.FamilyID(r.Context()))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load rules")
		return
	}
	out := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		out = append(out, ruleJSON(rule))
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"rules": out})
}

type ruleBody struct {
	CategoryID     uuid.UUID `json:"category_id"`
	Priority       *int      `json:"priority"`
	PayeeMatch     string    `json:"payee_match"`
	MatchType      string    `json:"match_type"`
	MCC            string    `json:"mcc"`
	MinAmountCents *int64    `json:"min_amount_cents"`
	MaxAmountCents *int64    `json:"max_amount_cents"`
	Direction      string    `json:"direction"`
	Enabled        *bool     `json:"enabled"`
	// ApplyToExisting backfills the rule over past transactions that were
	// never categorized. Create-only.
	ApplyToExisting bool `json:"apply_to_existing"`
}

// toInput validates and normalizes a rule body.
func (h *Handlers) toInput(w http.ResponseWriter, r *http.Request, b ruleBody) (store.RuleInput, bool) {
	ctx := r.Context()
	famID := family.FamilyID(ctx)

	if _, err := h.Store.GetCategory(ctx, famID, b.CategoryID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "category not found in your family")
		return store.RuleInput{}, false
	}
	in := store.RuleInput{
		CategoryID: b.CategoryID,
		Priority:   100,
		PayeeMatch: strings.TrimSpace(b.PayeeMatch),
		MatchType:  b.MatchType,
		MCC:        strings.TrimSpace(b.MCC),
		Direction:  b.Direction,
		Enabled:    true,
		Source:     "user",
	}
	if b.Priority != nil {
		in.Priority = *b.Priority
	}
	if b.Enabled != nil {
		in.Enabled = *b.Enabled
	}
	switch in.MatchType {
	case "", "contains":
		in.MatchType = "contains"
	case "equals", "prefix":
	default:
		httpx.Error(w, http.StatusBadRequest, "bad_request", "match_type must be contains, equals, or prefix")
		return store.RuleInput{}, false
	}
	switch in.Direction {
	case "", "any":
		in.Direction = "any"
	case "spend", "income":
	default:
		httpx.Error(w, http.StatusBadRequest, "bad_request", "direction must be any, spend, or income")
		return store.RuleInput{}, false
	}
	if b.MinAmountCents != nil && *b.MinAmountCents < 0 ||
		b.MaxAmountCents != nil && *b.MaxAmountCents < 0 {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "amount bounds are magnitudes and cannot be negative")
		return store.RuleInput{}, false
	}
	if b.MinAmountCents != nil && b.MaxAmountCents != nil && *b.MinAmountCents > *b.MaxAmountCents {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "minimum cannot exceed maximum")
		return store.RuleInput{}, false
	}
	in.MinAmountCents, in.MaxAmountCents = b.MinAmountCents, b.MaxAmountCents

	// A rule with no conditions would swallow every transaction.
	if in.PayeeMatch == "" && in.MCC == "" && in.MinAmountCents == nil &&
		in.MaxAmountCents == nil && in.Direction == "any" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "set at least one condition")
		return store.RuleInput{}, false
	}
	return in, true
}

// CreateRule handles POST /api/rules.
func (h *Handlers) CreateRule(w http.ResponseWriter, r *http.Request) {
	var b ruleBody
	if !httpx.Decode(w, r, &b) {
		return
	}
	in, ok := h.toInput(w, r, b)
	if !ok {
		return
	}
	rule, err := h.Store.CreateRule(r.Context(), family.FamilyID(r.Context()), in)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not create rule")
		return
	}
	out := ruleJSON(*rule)
	if b.ApplyToExisting && h.Pipeline != nil {
		n, err := h.Pipeline.ApplyRuleToHistory(r.Context(), family.FamilyID(r.Context()), *rule)
		if err != nil {
			// The rule itself is created; report that rather than failing it.
			h.Log.Warn("apply rule to history", zap.Error(err))
		}
		out["backfilling"] = n
	}
	httpx.JSON(w, http.StatusCreated, out)
}

// UpdateRule handles PATCH /api/rules/{id}.
func (h *Handlers) UpdateRule(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	var b ruleBody
	if !httpx.Decode(w, r, &b) {
		return
	}
	in, ok := h.toInput(w, r, b)
	if !ok {
		return
	}
	err = h.Store.UpdateRule(r.Context(), family.FamilyID(r.Context()), id, in)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "rule not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not update rule")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// DeleteRule handles DELETE /api/rules/{id}.
func (h *Handlers) DeleteRule(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	err = h.Store.DeleteRule(r.Context(), family.FamilyID(r.Context()), id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "rule not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not delete rule")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}
