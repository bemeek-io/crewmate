package categoriesapi

import (
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/bemeek-io/crewmate/internal/family"
	"github.com/bemeek-io/crewmate/internal/httpx"
)

// reassessRanges are the windows offered, matching the cash flow report.
var reassessRanges = map[string]func(time.Time) time.Time{
	"1w": func(n time.Time) time.Time { return n.AddDate(0, 0, -7) },
	"1m": func(n time.Time) time.Time { return n.AddDate(0, -1, 0) },
	"3m": func(n time.Time) time.Time { return n.AddDate(0, -3, 0) },
	"6m": func(n time.Time) time.Time { return n.AddDate(0, -6, 0) },
	"1y": func(n time.Time) time.Time { return n.AddDate(-1, 0, 0) },
}

// Reassess handles POST /api/categorize/reassess {range}.
//
// Re-runs categorization over transactions in the window that were never
// categorized, which is how a newly added category reaches old spending
// without anyone scrolling through history.
//
// It cannot overwrite an existing answer: only transactions with an empty note
// are considered, so rule assignments, Subscription and Loan Payment labels,
// manual choices, and hand-written notes are all out of reach. Rules are still
// evaluated first for the ones it does touch, so a rule added later wins over
// the model rather than being second-guessed by it.
func (h *Handlers) Reassess(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Range string `json:"range"`
	}
	if !httpx.Decode(w, r, &req) {
		return
	}
	start, ok := reassessRanges[req.Range]
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "range must be 1w, 1m, 3m, 6m or 1y")
		return
	}
	if h.Pipeline == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "categorization is not running")
		return
	}

	n, err := h.Pipeline.Reassess(r.Context(), family.FamilyID(r.Context()), start(time.Now()))
	if err != nil {
		h.Log.Error("reassess", zap.Error(err))
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not start the re-assessment")
		return
	}
	// The work runs in the background — an LLM call per transaction is far
	// too slow to hold the request open for.
	httpx.JSON(w, http.StatusOK, map[string]any{"queued": n})
}
