// Package config loads and validates all environment configuration at boot.
package config

import (
	"crypto/ecdh"
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL string
	ListenAddr  string
	LogLevel    string
	AppBaseURL  string // e.g. https://crew.example.com — used for Origin checks and push URLs
	// RequireHTTPS is set when AppBaseURL is https, which is everywhere except
	// loopback development. It turns on HSTS.
	RequireHTTPS bool
	TrustProxy   bool

	// CrewTokenEncKey is the 32-byte AES-256-GCM key protecting Crew bearer tokens at rest.
	CrewTokenEncKey []byte

	AnthropicAPIKey string // optional; empty disables LLM categorization

	VAPIDPublicKey  string
	VAPIDPrivateKey string
	VAPIDSubject    string // mailto: or https:
	// VAPIDKeyError is set when the two keys aren't a matching pair, which
	// makes every push 403. Empty when they're fine.
	VAPIDKeyError string

	SessionTTL               time.Duration
	WatchInterval            time.Duration
	LeaseTTL                 time.Duration
	BackfillMonths           int
	MaxConnectionsPerReplica int // 0 = unlimited
}

func Load() (*Config, error) {
	c := &Config{
		DatabaseURL:              os.Getenv("DATABASE_URL"),
		ListenAddr:               getenv("LISTEN_ADDR", ":8080"),
		LogLevel:                 getenv("LOG_LEVEL", "info"),
		AppBaseURL:               strings.TrimRight(os.Getenv("APP_BASE_URL"), "/"),
		TrustProxy:               os.Getenv("TRUST_PROXY") == "true",
		AnthropicAPIKey:          os.Getenv("ANTHROPIC_API_KEY"),
		VAPIDPublicKey:           os.Getenv("VAPID_PUBLIC_KEY"),
		VAPIDPrivateKey:          os.Getenv("VAPID_PRIVATE_KEY"),
		VAPIDSubject:             os.Getenv("VAPID_SUBJECT"),
		SessionTTL:               getdur("SESSION_TTL", 180*24*time.Hour),
		WatchInterval:            getdur("WATCH_INTERVAL", 60*time.Second),
		LeaseTTL:                 getdur("LEASE_TTL", 60*time.Second),
		BackfillMonths:           getint("BACKFILL_MONTHS", 12),
		MaxConnectionsPerReplica: getint("MAX_CONNECTIONS_PER_REPLICA", 0),
	}

	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if c.AppBaseURL == "" {
		return nil, fmt.Errorf("APP_BASE_URL is required (e.g. https://crew.example.com)")
	}
	u, err := url.Parse(c.AppBaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("APP_BASE_URL must be an absolute URL")
	}
	// This is banking data: it never travels in the clear. Plain http is
	// refused outright rather than degraded, so a misconfigured deploy fails at
	// boot instead of quietly serving balances over HTTP. Loopback is the one
	// exception — browsers treat it as a secure context, which is what makes
	// local development possible without certificates.
	if u.Scheme != "https" && !isLoopback(u.Hostname()) {
		return nil, fmt.Errorf(
			"APP_BASE_URL must use https (got %q); plain http is only allowed on localhost", c.AppBaseURL)
	}
	c.RequireHTTPS = u.Scheme == "https"

	rawKey := os.Getenv("CREW_TOKEN_ENC_KEY")
	if rawKey == "" {
		return nil, fmt.Errorf("CREW_TOKEN_ENC_KEY is required (base64 of 32 random bytes)")
	}
	key, err := base64.StdEncoding.DecodeString(rawKey)
	if err != nil {
		key, err = base64.URLEncoding.DecodeString(rawKey)
	}
	if err != nil {
		return nil, fmt.Errorf("CREW_TOKEN_ENC_KEY is not valid base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("CREW_TOKEN_ENC_KEY must decode to exactly 32 bytes, got %d", len(key))
	}
	c.CrewTokenEncKey = key

	if c.VAPIDPublicKey == "" || c.VAPIDPrivateKey == "" || c.VAPIDSubject == "" {
		return nil, fmt.Errorf("VAPID_PUBLIC_KEY, VAPID_PRIVATE_KEY and VAPID_SUBJECT are required (run with -generate-vapid to create keys)")
	}
	if !strings.HasPrefix(c.VAPIDSubject, "mailto:") && !strings.HasPrefix(c.VAPIDSubject, "https:") {
		return nil, fmt.Errorf("VAPID_SUBJECT must start with mailto: or https:")
	}
	// Not fatal: a bad pair breaks push and nothing else, and killing the
	// process would take balances and transactions down with it — and, on an
	// automated deploy, crash-loop production. Recorded instead, logged loudly
	// at startup and reported by /api/push/test.
	if err := checkVAPIDPair(c.VAPIDPublicKey, c.VAPIDPrivateKey); err != nil {
		c.VAPIDKeyError = err.Error()
	}

	if c.LeaseTTL < 15*time.Second {
		return nil, fmt.Errorf("LEASE_TTL must be at least 15s")
	}
	return c, nil
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getdur(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func getint(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// checkVAPIDPair verifies the two VAPID keys are actually a pair.
//
// Browsers subscribe using the public key the server serves, and the server
// signs with the private one. If they come from different key generations,
// every push is rejected with 403 and nothing about it looks wrong: the keys
// are individually valid, the subscription registers fine, and re-subscribing
// changes nothing because the mismatch is server-side. Catching it at boot
// costs one scalar multiplication.
//
// Encoding follows webpush-go: the private key is the raw P-256 scalar and the
// public key is the uncompressed point, both unpadded base64url.
func checkVAPIDPair(pub, priv string) error {
	privBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(priv))
	if err != nil {
		return fmt.Errorf("VAPID_PRIVATE_KEY is not valid unpadded base64url: %w", err)
	}
	key, err := ecdh.P256().NewPrivateKey(privBytes)
	if err != nil {
		return fmt.Errorf("VAPID_PRIVATE_KEY is not a valid P-256 key: %w", err)
	}
	derived := base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
	if derived != strings.TrimSpace(pub) {
		return fmt.Errorf(
			"VAPID_PUBLIC_KEY does not match VAPID_PRIVATE_KEY — every push would be rejected "+
				"with 403. The public key for this private key is: %s", derived)
	}
	return nil
}

// isLoopback reports whether a hostname is the local machine. Browsers treat
// loopback as a secure context, so service workers and Web Push work over
// plain http there and nowhere else.
func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
