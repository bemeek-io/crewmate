// Package insightsapi serves savings suggestions and the family's
// "not applicable" list.
package insightsapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bemeek-io/crewmate/internal/auth"
	"github.com/bemeek-io/crewmate/internal/family"
	"github.com/bemeek-io/crewmate/internal/httpx"
	"github.com/bemeek-io/crewmate/internal/insights"
	"github.com/bemeek-io/crewmate/internal/store"
)

// judgeWait is how long a request waits for the model before answering with
// the rules instead; well inside the server's 60s write timeout. judgeBudget
// is how long the model gets in all, carrying on after the response if need be.
const (
	judgeWait   = 40 * time.Second
	judgeBudget = 3 * time.Minute
)

type Handlers struct {
	Store *store.Store
	Judge insights.Judge
	Log   *zap.Logger
}

// List handles GET /api/insights?tz=<IANA zone>.
//
// Suggestions are worked out fresh from the last twelve full months plus the
// current one, so they're never out of date with the ledger. Only the
// model's verdicts are cached.
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tz := r.URL.Query().Get("tz")
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "unknown time zone")
		return
	}
	now := time.Now().In(loc)
	famID := family.FamilyID(ctx)
	// Remember the zone, so nudges sent from the background cut months the
	// same way this page does.
	if err := h.Store.SetFamilyTimezone(ctx, famID, loc.String()); err != nil {
		h.Log.Warn("insights: save timezone", zap.Error(err))
	}
	rep, err := insights.Load(ctx, h.Store, famID, now)
	if err != nil {
		h.Log.Error("insights: load", zap.Error(err))
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not build suggestions")
		return
	}
	sugg, cached, necessities := rep.Suggestions, rep.Cached, rep.Necessities()
	months, start, cur, end, dismissed := rep.Months, rep.Start, rep.Current, rep.End, rep.Dismissed

	// Judging runs detached from the request, so a slow model can't hold the
	// response past the server's write timeout. If it isn't done in time the
	// rules answer for now, and the model's verdicts land in the cache for
	// the next visit.
	type outcome struct{ judged []insights.Judged }
	done := make(chan outcome, 1)
	go func() {
		jctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), judgeBudget)
		defer cancel()
		judged, fresh, err := insights.Verdicts(jctx, sugg, cached, h.Judge, necessities, now)
		if err != nil {
			// The suggestions still stand on their numbers; only the verdicts
			// fall back to rules.
			h.Log.Warn("insights: judge", zap.Error(err))
		}
		if len(fresh) > 0 {
			if err := h.Store.SaveInsightVerdicts(jctx, famID, fresh); err != nil {
				h.Log.Warn("insights: save verdicts", zap.Error(err))
			}
		}
		done <- outcome{judged}
	}()
	var judged []insights.Judged
	judging := false
	select {
	case o := <-done:
		judged = o.judged
	case <-time.After(judgeWait):
		judged, _, _ = insights.Verdicts(ctx, sugg, cached, nil, necessities, now)
		judging = true
	case <-ctx.Done():
		return
	}

	dis := make([]map[string]any, 0, len(dismissed))
	for _, d := range dismissed {
		dis = append(dis, map[string]any{
			"subject_type": d.SubjectType,
			"subject_key":  d.SubjectKey,
			"label":        d.Label,
			"created_at":   d.CreatedAt,
		})
	}
	if judged == nil {
		judged = []insights.Judged{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"months":        months,
		"tz":            loc.String(),
		"window_start":  start,
		"window_end":    cur,
		"days_elapsed":  now.Day(),
		"days_in_month": end.AddDate(0, 0, -1).Day(),
		"ai_enabled":    h.Judge != nil && h.Judge.Available(),
		"judging":       judging,
		"suggestions":   judged,
		"dismissed":     dis,
	})
}

type dismissalReq struct {
	SubjectType string `json:"subject_type"`
	SubjectKey  string `json:"subject_key"`
	Label       string `json:"label"`
}

func validSubject(w http.ResponseWriter, req dismissalReq) bool {
	switch req.SubjectType {
	case store.SubjectMerchant:
		if req.SubjectKey == "" {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "subject_key is required")
			return false
		}
	case store.SubjectCategory:
		if _, err := uuid.Parse(req.SubjectKey); err != nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "subject_key must be a category id")
			return false
		}
	default:
		httpx.Error(w, http.StatusBadRequest, "bad_request", "subject_type must be merchant or category")
		return false
	}
	return true
}

// Dismiss handles POST /api/insights/dismissals: "not applicable to us".
func (h *Handlers) Dismiss(w http.ResponseWriter, r *http.Request) {
	var req dismissalReq
	if !httpx.Decode(w, r, &req) || !validSubject(w, req) {
		return
	}
	if req.Label == "" || len(req.Label) > 200 {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "label is required")
		return
	}
	ctx := r.Context()
	if err := h.Store.DismissInsight(ctx, family.FamilyID(ctx), auth.UserID(ctx),
		req.SubjectType, req.SubjectKey, req.Label); err != nil {
		h.Log.Error("insights: dismiss", zap.Error(err))
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not save that")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Restore handles DELETE /api/insights/dismissals: flag it again.
func (h *Handlers) Restore(w http.ResponseWriter, r *http.Request) {
	var req dismissalReq
	if !httpx.Decode(w, r, &req) || !validSubject(w, req) {
		return
	}
	ctx := r.Context()
	err := h.Store.RestoreInsight(ctx, family.FamilyID(ctx), req.SubjectType, req.SubjectKey)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "that wasn't marked not applicable")
		return
	}
	if err != nil {
		h.Log.Error("insights: restore", zap.Error(err))
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not save that")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SetNudges handles PUT /api/insights/nudges: whether this person gets a push
// when a purchase matches a savings suggestion.
func (h *Handlers) SetNudges(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On *bool `json:"on"`
	}
	if !httpx.Decode(w, r, &req) {
		return
	}
	if req.On == nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "on is required")
		return
	}
	ctx := r.Context()
	if err := h.Store.SetSavingsNudges(ctx, auth.UserID(ctx), *req.On); err != nil {
		h.Log.Error("insights: set nudges", zap.Error(err))
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not save that")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
