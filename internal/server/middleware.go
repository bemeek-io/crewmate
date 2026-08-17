package server

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
)

// securityHeaders applies a conservative header set. Merchant logos load from
// remote https origins, which is why img-src allows https.
//
// hsts is set when the app is served over https, telling browsers to refuse
// plain http for this host from then on — so a stray http:// link can't put
// account data on the wire even once. TLS termination itself is the proxy's
// job; upgrade-insecure-requests covers any subresource that slips through.
func securityHeaders(hsts bool) func(http.Handler) http.Handler {
	csp := "default-src 'self'; img-src 'self' https: data:; connect-src 'self'; " +
		"manifest-src 'self'; worker-src 'self'; style-src 'self' 'unsafe-inline'; " +
		"frame-ancestors 'none'; base-uri 'self'; form-action 'self'"
	if hsts {
		csp += "; upgrade-insecure-requests"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", csp)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "same-origin")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			if hsts {
				// Two years, subdomains included, preload-eligible.
				h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
			}
			next.ServeHTTP(w, r)
		})
	}
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
