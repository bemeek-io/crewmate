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
	out := map[string]any{
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
		"category_id":     t.CategoryID,
		"category_name":   t.CategoryName,
		"category_source": t.CategorySource,
		"recurring_id":    t.RecurringID,
	}
	return out
}

// List handles GET /api/transactions with keyset pagination:
// ?before=<RFC3339Nano>,<uuid>&limit=50&category=<uuid>&uncategorized=1
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	f := store.TxnFilter{Uncategorized: q.Get("uncategorized") == "1"}
	if v := q.Get("limit"); v != "" {
		f.Limit, _ = strconv.Atoi(v)
	}
	if v := q.Get("category"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid category id")
			return
		}
		f.CategoryID = &id
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

// SetCategory handles PATCH /api/transactions/{id}/category.
// {category_id: uuid|null, apply_to_merchant: bool}
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
	if req.CategoryID != nil {
		// Verify the category belongs to this family before assigning it.
		cats, err := h.Store.ListCategories(ctx, famID)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", "could not verify category")
			return
		}
		found := false
		for _, c := range cats {
			if c.ID == *req.CategoryID {
				found = true
				break
			}
		}
		if !found {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "category not found in your family")
			return
		}
	}

	source := "user"
	if req.CategoryID == nil {
		source = "none"
	}
	if err := h.Store.SetTransactionCategory(ctx, famID, id, req.CategoryID, source); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not update category")
		return
	}

	backfilled := int64(0)
	if req.ApplyToMerchant && req.CategoryID != nil && t.MerchantKey != "" {
		if err := h.Store.UpsertMerchantRule(ctx, famID, t.MerchantKey, *req.CategoryID, "user", "high"); err != nil {
			h.Log.Warn("upsert user rule", zap.Error(err))
		}
		if n, err := h.Store.BackfillMerchantCategory(ctx, famID, t.MerchantKey, *req.CategoryID, "rule"); err == nil {
			backfilled = n
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "backfilled": backfilled})
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
		items = append(items, map[string]any{
			"id":               s.ID,
			"merchant_key":     s.MerchantKey,
			"amount_cents":     s.AmountCents,
			"cadence":          s.Cadence,
			"period_days":      s.PeriodDays,
			"first_seen_at":    s.FirstSeenAt,
			"last_seen_at":     s.LastSeenAt,
			"occurrence_count": s.OccurrenceCount,
			"is_subscription":  s.IsSubscription,
			"dismissed":        s.Dismissed,
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"series": items})
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
