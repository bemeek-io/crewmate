package config

import "testing"

// Plain http must never be accepted for a real host: it would put balances and
// session cookies on the wire in the clear. Loopback stays allowed because
// browsers treat it as a secure context, which is what local development needs.
func TestAppBaseURLRequiresHTTPS(t *testing.T) {
	cases := []struct {
		url  string
		want bool // allowed
	}{
		{"https://crew.example.com", true},
		{"https://10.0.0.22", true},
		{"http://localhost:8080", true},
		{"http://127.0.0.1:8080", true},
		{"http://[::1]:8080", true},
		{"http://crew.example.com", false},
		{"http://10.0.0.22", false},
		{"http://crew.internal", false},
	}
	for _, c := range cases {
		t.Setenv("APP_BASE_URL", c.url)
		t.Setenv("DATABASE_URL", "postgres://x/y")
		t.Setenv("CREW_TOKEN_ENC_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
		t.Setenv("VAPID_PUBLIC_KEY", "pub")
		t.Setenv("VAPID_PRIVATE_KEY", "priv")
		t.Setenv("VAPID_SUBJECT", "mailto:a@b.c")

		cfg, err := Load()
		switch {
		case c.want && err != nil:
			t.Errorf("APP_BASE_URL=%q: unexpected error %v", c.url, err)
		case !c.want && err == nil:
			t.Errorf("APP_BASE_URL=%q: expected rejection, got none", c.url)
		case c.want && err == nil:
			// HSTS must be on for https and off for loopback http.
			if want := c.url[:5] == "https"; cfg.RequireHTTPS != want {
				t.Errorf("APP_BASE_URL=%q: RequireHTTPS = %v, want %v", c.url, cfg.RequireHTTPS, want)
			}
		}
	}
}
