// Package family provides family-scoped authorization middleware and the
// family/invite HTTP handlers.
package family

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bemeek-io/crewmate/internal/auth"
	"github.com/bemeek-io/crewmate/internal/httpx"
	"github.com/bemeek-io/crewmate/internal/store"
)

type ctxKey int

const (
	ctxFamilyID ctxKey = iota
	ctxRole
)

// FamilyID returns the authenticated user's family from context. Handlers must
// take the family scope from here — never from the request.
func FamilyID(ctx context.Context) uuid.UUID {
	v, _ := ctx.Value(ctxFamilyID).(uuid.UUID)
	return v
}

func Role(ctx context.Context) string {
	v, _ := ctx.Value(ctxRole).(string)
	return v
}

type Handlers struct {
	Store *store.Store
	Log   *zap.Logger
}

// RequireMembership resolves the user's family and stashes it in context.
func (h *Handlers) RequireMembership(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m, err := h.Store.GetMembership(r.Context(), auth.UserID(r.Context()))
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", "membership lookup failed")
			return
		}
		if m == nil {
			httpx.Error(w, http.StatusForbidden, "no_family", "join or create a family first")
			return
		}
		ctx := context.WithValue(r.Context(), ctxFamilyID, m.FamilyID)
		ctx = context.WithValue(ctx, ctxRole, m.Role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *Handlers) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if Role(r.Context()) != "admin" {
			httpx.Error(w, http.StatusForbidden, "admin_only", "family admin required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Create handles POST /api/family.
func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !httpx.Decode(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 64 {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "name must be 1-64 characters")
		return
	}
	f, err := h.Store.CreateFamily(r.Context(), req.Name, auth.UserID(r.Context()))
	if errors.Is(err, store.ErrAlreadyMember) {
		httpx.Error(w, http.StatusConflict, "already_member", "you already belong to a family")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not create family")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"id": f.ID, "name": f.Name})
}

// Get handles GET /api/family (family-scoped).
func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f, err := h.Store.GetFamily(ctx, FamilyID(ctx))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load family")
		return
	}
	members, err := h.Store.ListFamilyMembers(ctx, FamilyID(ctx))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not load members")
		return
	}
	out := make([]map[string]any, 0, len(members))
	for _, m := range members {
		out = append(out, map[string]any{
			"user_id":    m.UserID,
			"first_name": m.FirstName,
			"last_name":  m.LastName,
			"role":       m.Role,
			"joined_at":  m.JoinedAt,
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"id": f.ID, "name": f.Name, "members": out, "role": Role(ctx),
	})
}

// crockford32 excludes I, L, O, U to keep codes unambiguous when read aloud.
const crockford32 = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func newInviteCode() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, 8)
	for i, v := range b {
		out[i] = crockford32[int(v)%32]
	}
	return string(out), nil
}

// CreateInvite handles POST /api/family/invites (admin).
func (h *Handlers) CreateInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	code, err := newInviteCode()
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not generate code")
		return
	}
	inv, err := h.Store.CreateInvite(ctx, FamilyID(ctx), auth.UserID(ctx), code, 48*time.Hour, 1)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not create invite")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"code": inv.Code, "expires_at": inv.ExpiresAt})
}

// Join handles POST /api/family/join (session, not yet family-scoped).
func (h *Handlers) Join(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if !httpx.Decode(w, r, &req) {
		return
	}
	code := strings.ToUpper(strings.TrimSpace(req.Code))
	if code == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "code is required")
		return
	}
	familyID, err := h.Store.RedeemInvite(r.Context(), code, auth.UserID(r.Context()))
	switch {
	case errors.Is(err, store.ErrInviteInvalid):
		httpx.Error(w, http.StatusBadRequest, "invite_invalid", "code invalid, expired, or already used")
	case errors.Is(err, store.ErrAlreadyMember):
		httpx.Error(w, http.StatusConflict, "already_member", "you already belong to a family")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not join family")
	default:
		httpx.JSON(w, http.StatusOK, map[string]any{"family_id": familyID})
	}
}

// RemoveMember handles DELETE /api/family/members/{userID} (admin).
func (h *Handlers) RemoveMember(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	target, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid user id")
		return
	}
	if target == auth.UserID(ctx) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "cannot remove yourself")
		return
	}
	if err := h.Store.RemoveFamilyMember(ctx, FamilyID(ctx), target); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "not_found", "member not found")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not remove member")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}
