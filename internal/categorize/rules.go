package categorize

import (
	"regexp"
	"strings"
)

var (
	wsRe          = regexp.MustCompile(`\s+`)
	storeNumberRe = regexp.MustCompile(`\s*(#\s*\d+|store\s+\d+|str\s+\d+)\s*$`)
)

// MerchantKey normalizes a payee string into the merchant lookup key:
// lowercase, trimmed, whitespace collapsed, trailing store numbers stripped.
// The computed key is stored on each transaction row at ingest, so SQL never
// re-implements this normalization.
func MerchantKey(payee string) string {
	k := strings.ToLower(strings.TrimSpace(payee))
	k = wsRe.ReplaceAllString(k, " ")
	k = storeNumberRe.ReplaceAllString(k, "")
	return strings.TrimSpace(k)
}
