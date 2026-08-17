package auth

import (
	"context"
	"net/http"
	"strings"
	"time"

	crew "github.com/bemeek-io/go-crew"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bemeek-io/crewmate/internal/crewfamily"
	"github.com/bemeek-io/crewmate/internal/crypto"
	"github.com/bemeek-io/crewmate/internal/httpx"
	"github.com/bemeek-io/crewmate/internal/store"
)

const (
	pendingTTL         = 10 * time.Minute
	maxVerifyAttempts  = 5
	smsPerIPPerHour    = 10
	smsPerPhonePerHour = 5
)

// Handlers implements the Crew-OTP-piggyback login flow. Completing the Crew
// OTP through crewmate IS the crewmate login. Replica-safe: pending state
// lives in Postgres, and each step rebuilds a throwaway crew client from the
// stored (encrypted) intermediate token.
//
// Privacy invariant: phone numbers and OTP codes pass through and are never
// persisted; only the rotating bearer token is stored, encrypted.
type Handlers struct {
	Store    *store.Store
	Box      *crypto.Box
	Sessions *Sessions
	Limiter  *Limiter
	Log      *zap.Logger
	// OnConnectionChanged nudges the lease scheduler after login (optional).
	OnConnectionChanged func()
}

func (h *Handlers) newClient(token string) *crew.Client {
	opts := []crew.Option{}
	if token != "" {
		opts = append(opts, crew.WithToken(token))
	}
	return crew.NewClient(opts...)
}

func (h *Handlers) decryptPendingToken(p *store.PendingLogin) (string, bool) {
	if len(p.TokenCiphertext) == 0 {
		return "", true
	}
	pt, err := h.Box.Decrypt(p.TokenCiphertext, p.ID[:])
	if err != nil {
		return "", false
	}
	return string(pt), true
}

// StartSMS handles POST /api/auth/sms.
func (h *Handlers) StartSMS(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Phone string `json:"phone"`
	}
	if !httpx.Decode(w, r, &req) {
		return
	}
	if req.Phone == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "phone is required")
		return
	}
	ctx := r.Context()
	if !h.Limiter.AllowIP(ctx, "sms", ClientIP(r), time.Hour, smsPerIPPerHour) ||
		!h.Limiter.AllowPhone(ctx, req.Phone, time.Hour, smsPerPhonePerHour) {
		httpx.Error(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, try again later")
		return
	}

	client := h.newClient("")
	phoneID, err := client.SendSMSOTP(ctx, req.Phone)
	if err != nil {
		h.Log.Warn("send sms otp failed", zap.Error(err))
		httpx.Error(w, http.StatusBadGateway, "crew_error", "could not send verification code")
		return
	}
	loginID := uuid.New()
	if err := h.Store.CreatePendingLogin(ctx, loginID, phoneID, pendingTTL); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not start login")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"login_id": loginID})
}

// VerifySMS handles POST /api/auth/sms/verify.
func (h *Handlers) VerifySMS(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LoginID uuid.UUID `json:"login_id"`
		Code    string    `json:"code"`
	}
	if !httpx.Decode(w, r, &req) {
		return
	}
	ctx := r.Context()
	p, err := h.loadPending(ctx, w, req.LoginID, "sms")
	if p == nil || err != nil {
		return
	}

	client := h.newClient("")
	res, err := client.AuthSMSOTP(ctx, p.PhoneID, req.Code)
	if err != nil {
		h.failAttempt(ctx, w, p, err)
		return
	}

	if res.SingleFactor {
		h.finalize(ctx, w, r, p, client)
		return
	}

	emailID, err := client.SendEmailOTP(ctx, res.Email)
	if err != nil {
		h.Log.Warn("send email otp failed", zap.Error(err))
		httpx.Error(w, http.StatusBadGateway, "crew_error", "could not send email verification code")
		return
	}
	ct, err := h.Box.Encrypt([]byte(client.Token()), p.ID[:])
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "encryption failed")
		return
	}
	if err := h.Store.AdvancePendingToEmail(ctx, p.ID, emailID, ct); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not advance login")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"needs_email": true, "email_masked": res.Email})
}

// VerifyEmail handles POST /api/auth/email/verify.
func (h *Handlers) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LoginID uuid.UUID `json:"login_id"`
		Code    string    `json:"code"`
	}
	if !httpx.Decode(w, r, &req) {
		return
	}
	ctx := r.Context()
	p, err := h.loadPending(ctx, w, req.LoginID, "email")
	if p == nil || err != nil {
		return
	}
	token, ok := h.decryptPendingToken(p)
	if !ok || token == "" {
		_ = h.Store.DeletePendingLogin(ctx, p.ID)
		httpx.Error(w, http.StatusBadRequest, "login_expired", "start over")
		return
	}

	client := h.newClient(token)
	if err := client.AuthEmailOTP(ctx, p.EmailID, req.Code); err != nil {
		// Persist any token rotation that happened despite the failure.
		if ct, encErr := h.Box.Encrypt([]byte(client.Token()), p.ID[:]); encErr == nil {
			_ = h.Store.UpdatePendingToken(ctx, p.ID, ct)
		}
		h.failAttempt(ctx, w, p, err)
		return
	}
	h.finalize(ctx, w, r, p, client)
}

// Logout handles POST /api/auth/logout.
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	if id := SessionID(r.Context()); id != uuid.Nil {
		_ = h.Store.DeleteSession(r.Context(), id)
	}
	h.Sessions.Clear(w)
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handlers) loadPending(ctx context.Context, w http.ResponseWriter, id uuid.UUID, wantStage string) (*store.PendingLogin, error) {
	if id == uuid.Nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "login_id is required")
		return nil, nil
	}
	p, err := h.Store.GetPendingLogin(ctx, id)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "lookup failed")
		return nil, err
	}
	if p == nil || p.Stage != wantStage {
		httpx.Error(w, http.StatusBadRequest, "login_expired", "login expired or invalid, start over")
		return nil, nil
	}
	return p, nil
}

func (h *Handlers) failAttempt(ctx context.Context, w http.ResponseWriter, p *store.PendingLogin, cause error) {
	n, err := h.Store.BumpPendingAttempts(ctx, p.ID)
	if err == nil && n >= maxVerifyAttempts {
		_ = h.Store.DeletePendingLogin(ctx, p.ID)
		httpx.Error(w, http.StatusBadRequest, "login_expired", "too many attempts, start over")
		return
	}
	h.Log.Info("otp verify failed", zap.Error(cause))
	httpx.Error(w, http.StatusBadRequest, "bad_code", "verification code incorrect")
}

// finalize completes login: resolve the Crew identity, persist the encrypted
// token, and issue a crewmate session.
func (h *Handlers) finalize(ctx context.Context, w http.ResponseWriter, r *http.Request, p *store.PendingLogin, client *crew.Client) {
	cu, err := client.CurrentUser(ctx)
	if err != nil {
		h.Log.Error("current user after otp failed", zap.Error(err))
		httpx.Error(w, http.StatusBadGateway, "crew_error", "could not load your Crew profile")
		return
	}
	// Crew's own household ID lets a second member land in the same crewmate
	// family automatically. Best effort — an empty result just means they'll
	// use an invite code instead.
	crewFamilyID := crewfamily.FetchCrewFamilyID(ctx, client)

	u, err := h.Store.UpsertUser(ctx, cu.ID, cu.FirstName, cu.LastName, crewFamilyID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not save user")
		return
	}
	// Signing in is the whole of setup: the household comes from Crew, so
	// there is no family to name and no invite code to type.
	famName := strings.TrimSpace(cu.LastName)
	if famName != "" {
		famName += " family"
	}
	if famID, err := h.Store.EnsureFamily(ctx, u.ID, crewFamilyID, famName); err != nil {
		h.Log.Error("ensure family", zap.Error(err))
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not set up your family")
		return
	} else {
		h.Log.Info("family ready", zap.String("family", famID.String()),
			zap.Bool("from_crew", crewFamilyID != ""))
	}
	_, err = h.Store.UpsertConnection(ctx, u.ID, func(connID uuid.UUID) ([]byte, error) {
		return h.Box.Encrypt([]byte(client.Token()), connID[:])
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not save connection")
		return
	}
	_ = h.Store.DeletePendingLogin(ctx, p.ID)

	if err := h.Sessions.Issue(ctx, w, u.ID, r.UserAgent()); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "could not create session")
		return
	}
	if h.OnConnectionChanged != nil {
		h.OnConnectionChanged()
	}
	m, _ := h.Store.GetMembership(ctx, u.ID)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"user": map[string]any{
			"id":         u.ID,
			"first_name": u.FirstName,
			"last_name":  u.LastName,
		},
		"has_family": m != nil,
	})
}
