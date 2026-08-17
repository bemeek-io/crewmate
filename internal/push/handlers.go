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
//
// It reports how many devices were targeted. Zero is the interesting answer:
// it means this device never registered, which is the usual story on iOS when
// the app was added to the Home Screen from a browser other than Safari, or
// when permission was granted in a browser tab rather than the installed app.
// Without that count a silent phone is indistinguishable from a broken send.
func (h *Handlers) Test(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserID(r.Context())
	subs, err := h.Service.Store.ListPushSubscriptionsForUser(r.Context(), userID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load your devices")
		return
	}
	h.Service.SendToUser(r.Context(), userID, Notification{
		Title: "Crewmate",
		Body:  "Notifications are working 🎉",
		URL:   "/",
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "devices": len(subs)})
}
