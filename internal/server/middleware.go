package server

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
)

// securityHeaders applies a conservative header set. TLS/HSTS is the reverse
// proxy's job; merchant logos load from remote https origins.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' https: data:; connect-src 'self'; "+
				"manifest-src 'self'; worker-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

// zapRequestLogger logs one line per request. Bodies are never logged.
func zapRequestLogger(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			log.Info("http",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", ww.Status()),
				zap.Duration("dur", time.Since(start)),
				zap.String("request_id", middleware.GetReqID(r.Context())),
			)
		})
	}
}
