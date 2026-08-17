// Package crewcards adapts Crew debit cards into the shape the dashboard
// snapshot stores: one entry per real card, carrying the pocket it spends from.
package crewcards

import (
	"context"

	crew "github.com/bemeek-io/go-crew"
)

// selectedSpendQuery reads the pocket the member's physical card is currently
// set to swipe from.
//
// This is User.userSpendConfig.selectedSpendSubaccount — NOT
// Account.PrimarySubaccount, which is only the account's default and does not
// change when the spend pocket is repointed. It is also not the bare
// User.selectedSpendSubaccount in Crew's published docs, which the live schema
// rejects. The SDK doesn't model userSpendConfig yet, so this is the one
// remaining raw query.
const selectedSpendQuery = `query SelectedSpendPocket {
  currentUser {
    userSpendConfig {
      id
      selectedSpendSubaccount { id name }
    }
  }
}`

// selectedSpendPocket returns the currently selected spend pocket, or nil when
// the member hasn't chosen one (in which case the account default applies).
func selectedSpendPocket(ctx context.Context, client *crew.Client) *crew.Subaccount {
	var out struct {
		CurrentUser struct {
			UserSpendConfig *struct {
				SelectedSpendSubaccount *crew.Subaccount `json:"selectedSpendSubaccount"`
			} `json:"userSpendConfig"`
		} `json:"currentUser"`
	}
	if err := client.Execute(ctx, selectedSpendQuery, nil, &out); err != nil {
		return nil
	}
	if out.CurrentUser.UserSpendConfig == nil {
		return nil
	}
	return out.CurrentUser.UserSpendConfig.SelectedSpendSubaccount
}

// Card is a debit card and the pocket it spends from.
type Card struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	LastFour       string `json:"last_four"`
	Status         string `json:"status"`
	FormFactor     string `json:"form_factor"`
	FrozenStatus   string `json:"frozen_status"`
	SubaccountID   string `json:"subaccount_id"`
	SubaccountName string `json:"subaccount_name"`
}

// Fetch returns the member's real debit cards. Crew also issues a virtual card
// per merchant — a couple dozen on an active account — but those are managed in
// Crew itself, so only physical cards reach the snapshot.
//
// accounts comes from the CurrentUser call the caller already made; a physical
// card's pocket lives on its account (PrimarySubaccount), not on the card, and
// DebitCards doesn't fetch the account body.
//
// A nil error with no cards is normal; an error is returned so callers can
// leave the previous snapshot in place rather than blanking the list on a
// transient failure.
func Fetch(ctx context.Context, client *crew.Client, accounts []crew.Account) ([]Card, error) {
	cards, err := client.DebitCards(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*crew.Account, len(accounts))
	for i := range accounts {
		byID[accounts[i].ID] = &accounts[i]
	}
	// The member's explicit choice wins over the account default.
	selected := selectedSpendPocket(ctx, client)

	var out []Card
	seen := map[string]bool{}
	for _, c := range cards {
		if c.ID == "" || seen[c.ID] || c.FormFactor != crew.DebitCardFormFactorPhysical {
			continue
		}
		seen[c.ID] = true
		card := Card{
			ID:           c.ID,
			Name:         c.Name,
			LastFour:     c.LastFour,
			Status:       string(c.Status),
			FormFactor:   string(c.FormFactor),
			FrozenStatus: string(c.FrozenStatus),
		}
		var acct *crew.Account
		if c.Account != nil {
			acct = byID[c.Account.ID]
		}
		pocket := selected
		if pocket == nil {
			pocket = c.SpendSubaccount(acct)
		}
		if pocket != nil {
			card.SubaccountID, card.SubaccountName = pocket.ID, pocket.Name
		}
		out = append(out, card)
	}
	return out, nil
}

// MovePocket points the member's physical card at a different pocket.
//
// This is a user-level setting rather than a card-level one, which is why the
// card ID isn't used: Crew applies it to that user's card swipes, and the
// result surfaces as the account's PrimarySubaccount on the next fetch.
func MovePocket(ctx context.Context, client *crew.Client, subaccountID string) error {
	// SetSpendSubaccount is keyed on the Crew user, and card moves are rare
	// enough that resolving it per call is cheaper than threading it through
	// the write queue.
	u, err := client.CurrentUser(ctx)
	if err != nil {
		return err
	}
	_, err = client.SetSpendSubaccount(ctx, u.ID, subaccountID)
	return err
}
