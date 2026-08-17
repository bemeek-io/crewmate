package auth

import (
	"net/http"
	"net/url"

	"github.com/bemeek-io/crewmate/internal/httpx"
)

// CSRF protection for a same-origin cookie SPA: SameSite=Lax cookies plus a
// strict Origin check and a required custom header (custom headers force a
// CORS preflight, which we never answer cross-origin). No token dance needed.
func CSRF(appBaseURL string) func(http.Handler) http.Handler {
	appOrigin := ""
	if u, err := url.Parse(appBaseURL); err == nil {
		appOrigin = u.Scheme + "://" + u.Host
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			if r.Header.Get("X-Crewmate") == "" {
				httpx.Error(w, http.StatusForbidden, "csrf", "missing X-Crewmate header")
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && origin != "null" && origin != appOrigin {
				httpx.Error(w, http.StatusForbidden, "csrf", "cross-origin request rejected")
				return
			}
			if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" && sfs != "same-origin" && sfs != "none" {
				httpx.Error(w, http.StatusForbidden, "csrf", "cross-site request rejected")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
