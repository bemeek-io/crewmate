// Package crewlink exposes the Crew-connection HTTP surface. All reads come
// from Postgres (snapshots written by the lease holder) — HTTP replicas never
// talk to Crew directly, so any replica can serve any request.
package crewlink

import (
	"encoding/json"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/bemeek-io/crewmate/internal/auth"
	"github.com/bemeek-io/crewmate/internal/family"
	"github.com/bemeek-io/crewmate/internal/httpx"
	"github.com/bemeek-io/crewmate/internal/store"
)

// snapshotStaleAfter: reads older than this trigger a best-effort NOTIFY to
// the holder for an early refresh.
const snapshotStaleAfter = 90 * time.Second

type Handlers struct {
	Store *store.Store
	Log   *zap.Logger
}

// Me handles GET /api/me.
func (h *Handlers) Me(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := auth.UserID(ctx)
	u, err := h.Store.GetUser(ctx, userID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load user")
		return
	}
	m, err := h.Store.GetMembership(ctx, userID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load membership")
		return
	}
	conn, err := h.Store.GetConnectionByUser(ctx, userID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load connection")
		return
	}
	out := map[string]any{
		"user": map[string]any{
			"id":         u.ID,
			"first_name": u.FirstName,
			"last_name":  u.LastName,
		},
		"crew_status": "none",
	}
	if conn != nil {
		out["crew_status"] = string(conn.Status)
	}
	if m != nil {
		out["family_id"] = m.FamilyID
		out["role"] = m.Role
	}
	httpx.JSON(w, http.StatusOK, out)
}

// Status handles GET /api/crew/status.
func (h *Handlers) Status(w http.ResponseWriter, r *http.Request) {
	conn, err := h.Store.GetConnectionByUser(r.Context(), auth.UserID(r.Context()))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load connection")
		return
	}
	status := "none"
	var lastPolled *time.Time
	if conn != nil {
		status = string(conn.Status)
		lastPolled = conn.LastPolledAt
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"status": status, "last_polled_at": lastPolled})
}

// Disconnect handles DELETE /api/crew/connection.
func (h *Handlers) Disconnect(w http.ResponseWriter, r *http.Request) {
	if err := h.Store.DisableConnection(r.Context(), auth.UserID(r.Context())); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not disconnect")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Accounts handles GET /api/accounts — every family member's balances from
// the holder-maintained snapshots.
func (h *Handlers) Accounts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	snaps, err := h.Store.ListFamilySnapshots(ctx, family.FamilyID(ctx))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load balances")
		return
	}
	members := make([]map[string]any, 0, len(snaps))
	for _, s := range snaps {
		var payload map[string]any
		if err := json.Unmarshal(s.Payload, &payload); err != nil {
			continue
		}
		members = append(members, map[string]any{
			"user_id":    s.UserID,
			"first_name": s.FirstName,
			"fetched_at": s.FetchedAt,
			"accounts":   payload["accounts"],
		})
		if time.Since(s.FetchedAt) > snapshotStaleAfter {
			// Ask the holder for a refresh; served data stays as-is this call.
			if conn, err := h.Store.GetConnectionByUser(ctx, s.UserID); err == nil && conn != nil {
				_ = h.Store.NotifyRefresh(ctx, conn.ID)
			}
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"members": members})
}
