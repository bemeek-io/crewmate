// Package crewfamily reads Crew's own household identity, used to link family
// members automatically so they share a category list without an invite code.
package crewfamily

import (
	"context"

	crew "github.com/bemeek-io/go-crew"
)

// FetchCrewFamilyID returns the Crew family ID for the authenticated user, or
// "" when it isn't available. Callers treat an empty result as "no automatic
// link" and fall back to invite codes — this must never block login, which is
// why the error is swallowed rather than returned.
func FetchCrewFamilyID(ctx context.Context, client *crew.Client) string {
	id, err := client.CurrentUserFamilyID(ctx)
	if err != nil {
		return ""
	}
	return id
}
