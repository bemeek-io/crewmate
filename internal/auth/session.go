package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bemeek-io/crewmate/internal/httpx"
	"github.com/bemeek-io/crewmate/internal/store"
)

type ctxKey int

const (
	ctxUserID ctxKey = iota
	ctxSessionID
)

// Sessions issues and validates crewmate's own session cookies. Sessions are
// server-side rows; the cookie carries only a random token whose SHA-256 is
// stored. Crew tokens are never exposed to the browser.
type Sessions struct {
	Store  *store.Store
	TTL    time.Duration
	Secure bool // true when APP_BASE_URL is https
}

func (s *Sessions) cookieName() string {
	if s.Secure {
		return "__Host-crewmate_session"
	}
	return "crewmate_session"
}

func (s *Sessions) Issue(ctx context.Context, w http.ResponseWriter, userID uuid.UUID, userAgent string) error {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	if err := s.Store.CreateSession(ctx, hash[:], userID, s.TTL, userAgent); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName(),
		Value:    token,
		Path:     "/",
		MaxAge:   int(s.TTL.Seconds()),
		HttpOnly: true,
		Secure:   s.Secure,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (s *Sessions) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName(),
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.Secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// Lookup resolves the request's session, returning nil when unauthenticated.
func (s *Sessions) Lookup(r *http.Request) (*store.Session, error) {
	c, err := r.Cookie(s.cookieName())
	if err != nil || c.Value == "" {
		return nil, nil
	}
	hash := sha256.Sum256([]byte(c.Value))
	return s.Store.GetSessionByTokenHash(r.Context(), hash[:], s.TTL)
}

// RequireSession authenticates the request and stores user/session IDs in context.
func (s *Sessions) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.Lookup(r)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", "session lookup failed")
			return
		}
		if sess == nil {
			httpx.Error(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
			return
		}
		ctx := context.WithValue(r.Context(), ctxUserID, sess.UserID)
		ctx = context.WithValue(ctx, ctxSessionID, sess.ID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func UserID(ctx context.Context) uuid.UUID {
	v, _ := ctx.Value(ctxUserID).(uuid.UUID)
	return v
}

func SessionID(ctx context.Context) uuid.UUID {
	v, _ := ctx.Value(ctxSessionID).(uuid.UUID)
	return v
}

// ClientIP extracts the caller IP for rate limiting (RealIP middleware has
// already rewritten RemoteAddr when TRUST_PROXY is enabled).
func ClientIP(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}
