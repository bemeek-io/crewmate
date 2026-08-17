package config

import (
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// A mismatched VAPID pair is the one misconfiguration that makes every push
// gateway answer 403 while looking entirely healthy from the client: both keys
// are valid, the subscription registers, and re-subscribing changes nothing.
// It has to be caught at boot rather than diagnosed from a gateway status.
func TestVAPIDPairMustMatch(t *testing.T) {
	privA, pubA, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	privB, pubB, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	if err := checkVAPIDPair(pubA, privA); err != nil {
		t.Errorf("matching pair rejected: %v", err)
	}
	if err := checkVAPIDPair(pubB, privB); err != nil {
		t.Errorf("matching pair rejected: %v", err)
	}
	// The failure this exists for: valid keys, wrong combination.
	if err := checkVAPIDPair(pubB, privA); err == nil {
		t.Error("mismatched pair accepted")
	}
	// Whitespace from a copy-pasted secret must not read as a mismatch.
	if err := checkVAPIDPair(" "+pubA+"\n", "\t"+privA+" "); err != nil {
		t.Errorf("padded pair rejected: %v", err)
	}
}

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
	// Real keys: Load now verifies they're a genuine pair.
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Setenv("APP_BASE_URL", c.url)
		t.Setenv("DATABASE_URL", "postgres://x/y")
		t.Setenv("CREW_TOKEN_ENC_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
		t.Setenv("VAPID_PUBLIC_KEY", pub)
		t.Setenv("VAPID_PRIVATE_KEY", priv)
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
