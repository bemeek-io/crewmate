// Package transactionsapi serves the family-scoped transaction and recurring
// endpoints.
package transactionsapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bemeek-io/crewmate/internal/family"
	"github.com/bemeek-io/crewmate/internal/httpx"
	"github.com/bemeek-io/crewmate/internal/store"
)

type Handlers struct {
	Store *store.Store
	Log   *zap.Logger
}

func txnJSON(t *store.Transaction) map[string]any {
	return map[string]any{
		"id":              t.ID,
		"amount_cents":    t.AmountCents,
		"payee":           t.Payee,
		"title":           t.Title,
		"description":     t.Description,
		"status":          t.Status,
		"type":            t.TxnType,
		"mcc":             t.MCC,
		"image_url":       t.ImageURL,
		"subaccount_name": t.SubaccountName,
		"occurred_at":     t.OccurredAt,
		"cleared_at":      t.ClearedAt,
		"pending":         t.ClearedAt == nil,
		// note is Crew's field and the source of truth for the category;
		// category_* are derived from it by matching the family's list.
		"note":          t.Note,
		"category_id":   t.CategoryID,
		"category_name": t.CategoryName,
		// A note that names no category is the user's own annotation; it can
		// be promoted to a category unless they've chosen to ignore it.
		"has_user_note":    t.Note != "" && t.CategoryID == nil,
		"note_ignored":     t.NoteIgnored,
		"can_add_category": t.Note != "" && t.CategoryID == nil && !t.NoteIgnored,
		"recurring_id":     t.RecurringID,
	}
}

// List handles GET /api/transactions with keyset pagination:
// ?before=<RFC3339Nano>,<uuid>&limit=50&category=<uuid>&uncategorized=1
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	f := store.TxnFilter{
		Uncategorized: q.Get("uncategorized") == "1",
		Query:         q.Get("q"),
	}
	if v := q.Get("limit"); v != "" {
		f.Limit, _ = strconv.Atoi(v)
	}
	// `category` may repeat (multi-select) or arrive comma-separated.
	for _, raw := range q["category"] {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			id, err := uuid.Parse(part)
			if err != nil {
				httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid category id")
				return
			}
			f.CategoryIDs = append(f.CategoryIDs, id)
		}
	}
	// Window + direction, so a cash flow row can drill into its transactions.
	for _, p := range []struct {
		key string
		dst **time.Time
	}{{"since", &f.Since}, {"until", &f.Until}} {
		v := q.Get(p.key)
		if v == "" {
			continue
		}
		ts, err := time.Parse(time.RFC3339, v)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid "+p.key)
			return
		}
		*p.dst = &ts
	}
	switch d := q.Get("direction"); d {
	case "", store.DirectionIncome, store.DirectionExpense:
		f.Direction = d
	default:
		httpx.Error(w, http.StatusBadRequest, "bad_request", "direction must be income or expense")
		return
	}
	if v := q.Get("before"); v != "" {
		parts := strings.SplitN(v, ",", 2)
		if len(parts) != 2 {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid cursor")
			return
		}
		ts, err1 := time.Parse(time.RFC3339Nano, parts[0])
		id, err2 := uuid.Parse(parts[1])
		if err1 != nil || err2 != nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid cursor")
			return
		}
		f.BeforeTime, f.BeforeID = &ts, &id
	}

	txns, err := h.Store.ListTransactions(ctx, family.FamilyID(ctx), f)
	if err != nil {
		h.Log.Error("list transactions", zap.Error(err))
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load transactions")
		return
	}
	items := make([]map[string]any, 0, len(txns))
	for _, t := range txns {
		items = append(items, txnJSON(t))
	}
	var next string
	if len(txns) > 0 {
		last := txns[len(txns)-1]
		next = last.OccurredAt.Format(time.RFC3339Nano) + "," + last.ID.String()
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"transactions": items, "next_cursor": next})
}

// Get handles GET /api/transactions/{id} — the push deep-link target.
func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid transaction id")
		return
	}
	t, err := h.Store.GetTransaction(ctx, family.FamilyID(ctx), id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "transaction not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load transaction")
		return
	}
	out := txnJSON(t)
	out["merchant_key"] = t.MerchantKey
	httpx.JSON(w, http.StatusOK, out)
}

// backfillLimit caps how many past transactions one "apply to this merchant"
// action rewrites, since each is a separate Crew mutation.
const backfillLimit = 100

// SetCategory handles PATCH /api/transactions/{id}/category.
// {category_id: uuid|null, apply_to_merchant: bool}
//
// The category is stored in Crew's note field, so this enqueues a write that
// the replica holding this transaction's connection performs. The response is
// accepted-but-pending by design: the client shows the choice optimistically.
func (h *Handlers) SetCategory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	famID := family.FamilyID(ctx)
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid transaction id")
		return
	}
	var req struct {
		CategoryID      *uuid.UUID `json:"category_id"`
		ApplyToMerchant bool       `json:"apply_to_merchant"`
		// OverwriteExisting extends the merchant backfill to transactions
		// already categorized, for moving a merchant to a different category.
		OverwriteExisting bool `json:"overwrite_existing"`
	}
	if !httpx.Decode(w, r, &req) {
		return
	}

	t, err := h.Store.GetTransaction(ctx, famID, id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "transaction not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load transaction")
		return
	}

	note := "" // clearing the category clears the Crew note
	if req.CategoryID != nil {
		cat, err := h.Store.GetCategory(ctx, famID, *req.CategoryID)
		if errors.Is(err, store.ErrNotFound) {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "category not found in your family")
			return
		}
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", "could not verify category")
			return
		}
		note = cat.Name
	}

	if err := h.Store.EnqueueNoteWrite(ctx, t.ConnectionID, t.CrewTxnID, note); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not queue category update")
		return
	}

	queued := 1
	if req.ApplyToMerchant && note != "" && t.MerchantKey != "" {
		// Without overwrite only blank notes are filled. With it, categories
		// already set are replaced too — including Subscription and Loan
		// Payment, since moving a merchant off one of those is exactly what
		// this is for. A hand-written Crew note is never touched either way:
		// it isn't a category, and it's the one thing here a person typed.
		var others []*store.Transaction
		if req.OverwriteExisting {
			others, err = h.Store.AllForMerchant(ctx, famID, t.MerchantKey, backfillLimit)
		} else {
			others, err = h.Store.UncategorizedForMerchant(ctx, famID, t.MerchantKey, backfillLimit)
		}
		if err != nil {
			h.Log.Warn("merchant backfill lookup", zap.Error(err))
		}
		for _, o := range others {
			if o.ID == t.ID || !o.Replaceable() || o.Note == note {
				continue
			}
			if err := h.Store.EnqueueNoteWrite(ctx, o.ConnectionID, o.CrewTxnID, note); err != nil {
				h.Log.Warn("queue backfill note", zap.Error(err))
				continue
			}
			queued++
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "queued": queued, "note": note})
}

// seriesJSON exposes the classification along with the evidence behind it, so
// the UI can explain why something was called a subscription.
func seriesJSON(s store.RecurringSeries) map[string]any {
	return map[string]any{
		"id":                   s.ID,
		"merchant_key":         s.MerchantKey,
		"kind":                 s.Kind,
		"is_subscription":      s.Kind == "subscription",
		"typical_amount_cents": s.TypicalAmountCents,
		"min_amount_cents":     s.MinAmountCents,
		"max_amount_cents":     s.MaxAmountCents,
		"cadence":              s.Cadence,
		"period_days":          s.PeriodDays,
		"interval_spread_pct":  s.IntervalSpreadPct,
		"amount_spread_pct":    s.AmountSpreadPct,
		"day_spread_days":      s.DaySpreadDays,
		"first_seen_at":        s.FirstSeenAt,
		"last_seen_at":         s.LastSeenAt,
		"occurrence_count":     s.OccurrenceCount,
		"dismissed":            s.Dismissed,
		"label_system_key":     s.LabelSystemKey,
		"label_name":           s.LabelName,
	}
}

// LabelRecurring handles PUT /api/recurring/{id}/label:
// {system_key: "subscription"|"loan_payment"|null}
//
// Labeling records a rule for that merchant, so future charges are categorized
// automatically (and silently — a rule match is an expected outcome). Passing
// null clears the label and its rule.
func (h *Handlers) LabelRecurring(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	famID := family.FamilyID(ctx)
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	var req struct {
		SystemKey *string `json:"system_key"`
	}
	if !httpx.Decode(w, r, &req) {
		return
	}
	s, err := h.Store.GetRecurringSeries(ctx, famID, id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "series not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load series")
		return
	}

	if req.SystemKey == nil {
		if err := h.Store.DeleteSeriesRule(ctx, famID, s.MerchantKey); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", "could not clear label")
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "label": nil})
		return
	}

	key := *req.SystemKey
	if key != store.SystemSubscription && key != store.SystemLoanPayment {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "unknown label")
		return
	}
	cat, err := h.Store.GetSystemCategory(ctx, famID, key)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not resolve category")
		return
	}
	if err := h.Store.UpsertSeriesRule(ctx, famID, cat.ID, s.MerchantKey); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not save label")
		return
	}

	// Apply to the charges already on file that carry no note, so the label
	// takes effect on history and not just future charges.
	queued := 0
	existing, err := h.Store.UncategorizedForMerchant(ctx, famID, s.MerchantKey, backfillLimit)
	if err != nil {
		h.Log.Warn("label backfill lookup", zap.Error(err))
	}
	for _, t := range existing {
		if err := h.Store.EnqueueNoteWrite(ctx, t.ConnectionID, t.CrewTxnID, cat.Name); err != nil {
			h.Log.Warn("queue label note", zap.Error(err))
			continue
		}
		queued++
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"ok": true, "label": key, "category": cat.Name, "queued": queued,
	})
}

// ListRecurring handles GET /api/recurring.
func (h *Handlers) ListRecurring(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	series, err := h.Store.ListRecurringSeries(ctx, family.FamilyID(ctx))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load subscriptions")
		return
	}
	items := make([]map[string]any, 0, len(series))
	for _, s := range series {
		items = append(items, seriesJSON(s))
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"series": items})
}

// RecurringTransactions handles GET /api/recurring/{id}/transactions — the
// occurrences behind a detected series, so the detection can be inspected.
func (h *Handlers) RecurringTransactions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	famID := family.FamilyID(ctx)
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	s, err := h.Store.GetRecurringSeries(ctx, famID, id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "series not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load series")
		return
	}
	txns, err := h.Store.TransactionsInSeries(ctx, famID, s.MerchantKey, 100)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load transactions")
		return
	}
	items := make([]map[string]any, 0, len(txns))
	for _, t := range txns {
		items = append(items, txnJSON(t))
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"series":       seriesJSON(*s),
		"transactions": items,
	})
}

// PatchRecurring handles PATCH /api/recurring/{id}: {dismissed: bool}
func (h *Handlers) PatchRecurring(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	var req struct {
		Dismissed bool `json:"dismissed"`
	}
	if !httpx.Decode(w, r, &req) {
		return
	}
	if err := h.Store.SetRecurringDismissed(ctx, family.FamilyID(ctx), id, req.Dismissed); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "not_found", "series not found")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not update series")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}
