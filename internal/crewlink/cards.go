package crewlink

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bemeek-io/crewmate/internal/auth"
	"github.com/bemeek-io/crewmate/internal/crewcards"
	"github.com/bemeek-io/crewmate/internal/httpx"
	"github.com/bemeek-io/crewmate/internal/store"
)

// MoveCardPocket handles PUT /api/cards/{cardID}/pocket {subaccount_id}.
//
// A card belongs to one member's Crew account, so this only ever acts on the
// caller's own connection: the card ID and the destination pocket are both
// checked against the caller's own snapshot before anything is queued. Seeing
// a family member's card on the dashboard does not let you move it.
func (h *Handlers) MoveCardPocket(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cardID := chi.URLParam(r, "cardID")
	var req struct {
		SubaccountID string `json:"subaccount_id"`
	}
	if !httpx.Decode(w, r, &req) {
		return
	}
	if cardID == "" || req.SubaccountID == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "card and pocket are required")
		return
	}

	conn, err := h.Store.GetConnectionByUser(ctx, auth.UserID(ctx))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load connection")
		return
	}
	if conn == nil || conn.Status != store.ConnActive {
		httpx.Error(w, http.StatusConflict, "crew_relogin_required", "reconnect your Crew account first")
		return
	}
	snap, err := h.Store.GetSnapshot(ctx, conn.ID)
	if err != nil || snap == nil {
		httpx.Error(w, http.StatusConflict, "not_ready", "balances haven't loaded yet, try again shortly")
		return
	}

	var payload struct {
		Accounts []struct {
			Subaccounts []struct {
				ID string `json:"id"`
			} `json:"subaccounts"`
		} `json:"accounts"`
		Cards []crewcards.Card `json:"cards"`
	}
	if err := json.Unmarshal(snap.Payload, &payload); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not read balances")
		return
	}

	ownsCard := false
	for _, c := range payload.Cards {
		if c.ID == cardID {
			ownsCard = true
			break
		}
	}
	if !ownsCard {
		httpx.Error(w, http.StatusNotFound, "not_found", "that card isn't on your account")
		return
	}
	ownsPocket := false
	for _, a := range payload.Accounts {
		for _, sa := range a.Subaccounts {
			if sa.ID == req.SubaccountID {
				ownsPocket = true
				break
			}
		}
	}
	if !ownsPocket {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "that pocket isn't on your account")
		return
	}

	if err := h.Store.EnqueueWrite(ctx, conn.ID, store.WriteCardSubaccount, cardID, req.SubaccountID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not queue the move")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}
