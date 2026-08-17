package push

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/bemeek-io/crewmate/internal/store"
)

// webpush-go assumes Subscriber is a bare e-mail and prepends "mailto:" unless
// it already starts with "https:". Handing it a "mailto:" address therefore
// signs sub as "mailto:mailto:you@example.com". Apple validates sub and
// rejects that with BadJwtToken; FCM ignores sub, so the bug only ever shows
// up on iOS. Config requires the mailto: prefix, so this hit every send.
//
// This decodes the JWT actually put on the wire and asserts sub is a single,
// well-formed mailto.
func TestVAPIDSubjectIsNotDoublePrefixed(t *testing.T) {
	for _, subject := range []string{
		"mailto:ben@example.com",
		"https://example.com/contact",
	} {
		t.Run(subject, func(t *testing.T) {
			var auth string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				auth = r.Header.Get("Authorization")
				w.WriteHeader(http.StatusCreated)
			}))
			defer srv.Close()

			priv, pub, err := webpush.GenerateVAPIDKeys()
			if err != nil {
				t.Fatal(err)
			}
			svc := &Service{
				Log:             testLogger(),
				VAPIDPublicKey:  pub,
				VAPIDPrivateKey: priv,
				Subject:         subject,
			}
			// Keys are a valid subscription's; only the header matters here.
			svc.sendOne(context.Background(), store.PushSubscription{
				Endpoint: srv.URL,
				P256dh:   "BNcRdreALRFXTkOOUHK1EtK2wtaz5Ry4YfYCA_0QTpQtUbVlUls0VJXg7A8u-Ts1XbjhazAkj7I99e8QcYP7DkM",
				Auth:     "tBHItJI5svbpez7KI4CCXg",
			}, []byte(`{"title":"t"}`))

			sub := subFromVAPIDHeader(t, auth)
			if strings.Count(sub, "mailto:") > 1 {
				t.Fatalf("sub is double-prefixed: %q", sub)
			}
			if sub != subject {
				t.Fatalf("sub = %q, want %q", sub, subject)
			}
		})
	}
}

// subFromVAPIDHeader pulls the sub claim out of an "vapid t=<jwt>, k=<key>".
func subFromVAPIDHeader(t *testing.T, header string) string {
	t.Helper()
	if header == "" {
		t.Fatal("no Authorization header was sent")
	}
	_, rest, ok := strings.Cut(header, "t=")
	if !ok {
		t.Fatalf("unexpected Authorization form: %q", header)
	}
	jwt, _, _ := strings.Cut(rest, ",")
	parts := strings.Split(strings.TrimSpace(jwt), ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %q", jwt)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var claims struct {
		Sub string `json:"sub"`
		Aud string `json:"aud"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("parse claims: %v", err)
	}
	return claims.Sub
}
