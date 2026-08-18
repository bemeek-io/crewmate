package transactionsapi

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bemeek-io/crewmate/internal/categorize"
	"github.com/bemeek-io/crewmate/internal/family"
	"github.com/bemeek-io/crewmate/internal/httpx"
	"github.com/bemeek-io/crewmate/internal/store"
)

func vendorJSON(v store.VendorSpend) map[string]any {
	return map[string]any{
		"merchant_key": v.MerchantKey,
		"payee":        v.Payee,
		"cents":        v.Cents,
		"count":        v.Count,
	}
}

// SubscriptionSpend handles GET /api/recurring/spend?range=1m|3m|6m|1y.
//
// Answers "what do the subscriptions actually cost", which the category totals
// can't: a subscription filed under Tech is still a subscription, and a
// category called Subscription proves nothing recurs. This counts only series
// classified as subscriptions — same vendor, same amount, same point in the
// cycle — so merely recurring spending is left out.
func (h *Handlers) SubscriptionSpend(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("range")
	if key == "" {
		key = defaultRange
	}
	spec, ok := ranges[key]
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "range must be 1w, 1m, 3m, 6m or 1y")
		return
	}
	end := time.Now()
	start := spec.start(end)

	famID := family.FamilyID(r.Context())
	vendors, err := h.Store.SubscriptionSpend(r.Context(), famID, start, end)
	if err != nil {
		h.Log.Error("subscription spend", zap.Error(err))
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not total subscriptions")
		return
	}
	// What detection currently believes, so a total that looks short can be
	// told apart from a merchant that simply isn't classified as a
	// subscription — the difference between a bug here and a classification
	// question, which is otherwise invisible from the app.
	classified, err := h.Store.CountSubscriptionSeries(r.Context(), famID)
	if err != nil {
		h.Log.Warn("count subscription series", zap.Error(err))
	}
	out := make([]map[string]any, 0, len(vendors))
	var total int64
	for _, v := range vendors {
		out = append(out, vendorJSON(v))
		total += v.Cents
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"range":       key,
		"range_label": spec.label,
		"start":       start,
		"end":         end,
		"total_cents": total,
		"vendors":     out,
		// Series detection calls subscriptions, before loans and dismissals
		// are taken out. A gap against len(vendors) is explainable; a merchant
		// missing from both is a classification question, not a total bug.
		"classified_count": classified,
	})
}

// Reclassify handles POST /api/recurring/reclassify.
//
// Detection normally re-runs when a replica picks up a Crew connection, which
// ties it to restarts — so after a change to how things are classified, the
// stored kinds can stay stale with no way to refresh them from the app. This
// makes it something you can ask for.
//
// It rebuilds every merchant's series from stored history and is idempotent,
// so running it twice is harmless.
func (h *Handlers) Reclassify(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	famID := family.FamilyID(ctx)

	before, err := h.Store.CountSubscriptionSeries(ctx, famID)
	if err != nil {
		h.Log.Warn("count subscriptions before reclassify", zap.Error(err))
	}
	n, err := categorize.ReclassifyFamily(ctx, h.Store, famID)
	if err != nil {
		h.Log.Error("reclassify", zap.Error(err))
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not re-run detection")
		return
	}
	after, err := h.Store.CountSubscriptionSeries(ctx, famID)
	if err != nil {
		h.Log.Warn("count subscriptions after reclassify", zap.Error(err))
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"merchants":     n,
		"subscriptions": after,
		"changed":       after - before,
	})
}

// CashFlowVendors handles GET /api/cashflow/vendors — one cash flow line
// broken down by merchant, so a category can be read as "who was paid" before
// dropping to individual transactions.
func (h *Handlers) CashFlowVendors(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	key := q.Get("range")
	if key == "" {
		key = defaultRange
	}
	spec, ok := ranges[key]
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "range must be 1w, 1m, 3m, 6m or 1y")
		return
	}
	direction := q.Get("direction")
	if direction != store.DirectionIncome && direction != store.DirectionExpense {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "direction must be income or expense")
		return
	}

	var categoryID *uuid.UUID
	uncategorized := q.Get("uncategorized") == "1"
	if raw := q.Get("category"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid category id")
			return
		}
		categoryID = &id
	} else if !uncategorized {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "category or uncategorized is required")
		return
	}

	end := time.Now()
	start := spec.start(end)
	vendors, err := h.Store.CashFlowVendors(
		r.Context(), family.FamilyID(r.Context()), start, end, categoryID, uncategorized, direction)
	if err != nil {
		h.Log.Error("cash flow vendors", zap.Error(err))
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not break down that category")
		return
	}
	out := make([]map[string]any, 0, len(vendors))
	for _, v := range vendors {
		out = append(out, vendorJSON(v))
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"vendors": out, "start": start, "end": end})
}
