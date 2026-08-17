// Package crewfamily reads Crew's own household identity, which the SDK does
// not model yet.
package crewfamily

import (
	"context"

	crew "github.com/bemeek-io/go-crew"
)

// crewFamilyQuery reads the Crew family that owns the user's accounts. The SDK
// doesn't model families yet, so this goes through the raw GraphQL escape
// hatch. Every account in a Crew household points at the same family.
const crewFamilyQuery = `query CrewFamily {
  currentUser {
    accounts {
      family { id }
    }
  }
}`

type crewFamilyResult struct {
	CurrentUser struct {
		Accounts []struct {
			Family *struct {
				ID string `json:"id"`
			} `json:"family"`
		} `json:"accounts"`
	} `json:"currentUser"`
}

// FetchCrewFamilyID returns the Crew family ID for the authenticated user, or
// "" when the field isn't available. Callers treat an empty result as "no
// automatic link" and fall back to invite codes — this must never block login.
func FetchCrewFamilyID(ctx context.Context, client *crew.Client) string {
	var out crewFamilyResult
	if err := client.Execute(ctx, crewFamilyQuery, nil, &out); err != nil {
		return ""
	}
	for _, a := range out.CurrentUser.Accounts {
		if a.Family != nil && a.Family.ID != "" {
			return a.Family.ID
		}
	}
	return ""
}
