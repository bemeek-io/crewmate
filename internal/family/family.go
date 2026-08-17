// Package family resolves which household a request belongs to and scopes it.
//
// Crewmate does not manage families: membership comes from the Crew account at
// sign-in, so there is nothing here to create, join, invite into, or remove
// from. This package only answers "whose data is this?" — the answer every
// store query is keyed on.
package family

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bemeek-io/crewmate/internal/auth"
	"github.com/bemeek-io/crewmate/internal/httpx"
	"github.com/bemeek-io/crewmate/internal/store"
)

type ctxKey int

const ctxFamilyID ctxKey = iota

// FamilyID returns the authenticated user's family from context. Handlers must
// take the family scope from here — never from the request.
func FamilyID(ctx context.Context) uuid.UUID {
	v, _ := ctx.Value(ctxFamilyID).(uuid.UUID)
	return v
}

type Handlers struct {
	Store *store.Store
	Log   *zap.Logger
}

// RequireMembership resolves the user's household and stashes it in context.
//
// A missing membership means the sign-in that should have established it never
// completed; signing in again repairs it, so this reports "sign in again"
// rather than offering any kind of setup.
func (h *Handlers) RequireMembership(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m, err := h.Store.GetMembership(r.Context(), auth.UserID(r.Context()))
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", "membership lookup failed")
			return
		}
		if m == nil {
			httpx.Error(w, http.StatusForbidden, "no_family", "sign in again to finish setup")
			return
		}
		ctx := context.WithValue(r.Context(), ctxFamilyID, m.FamilyID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
