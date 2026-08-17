package push

import (
	"net/http"

	"github.com/bemeek-io/crewmate/internal/auth"
	"github.com/bemeek-io/crewmate/internal/httpx"
)

type Handlers struct {
	Service *Service
}

// VAPIDPublicKey handles GET /api/push/vapid-public-key.
func (h *Handlers) VAPIDPublicKey(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]string{"public_key": h.Service.VAPIDPublicKey})
}

// Subscribe handles POST /api/push/subscriptions — called on first opt-in and
// on every app launch (iOS silently drops subscriptions).
func (h *Handlers) Subscribe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	if !httpx.Decode(w, r, &req) {
		return
	}
	if req.Endpoint == "" || req.Keys.P256dh == "" || req.Keys.Auth == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "endpoint and keys are required")
		return
	}
	err := h.Service.Store.UpsertPushSubscription(r.Context(), auth.UserID(r.Context()),
		req.Endpoint, req.Keys.P256dh, req.Keys.Auth, r.UserAgent())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not save subscription")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Unsubscribe handles DELETE /api/push/subscriptions.
func (h *Handlers) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if !httpx.Decode(w, r, &req) {
		return
	}
	if err := h.Service.Store.DeletePushSubscriptionForUser(r.Context(),
		auth.UserID(r.Context()), req.Endpoint); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not delete subscription")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Test handles POST /api/push/test — sends a self-test notification.
func (h *Handlers) Test(w http.ResponseWriter, r *http.Request) {
	h.Service.SendToUser(r.Context(), auth.UserID(r.Context()), Notification{
		Title: "Crewmate",
		Body:  "Notifications are working 🎉",
		URL:   "/",
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}
