// Package crewcards reads debit cards together with the pocket (subaccount)
// their spend is drawn from. The SDK's DebitCard model doesn't query the
// subaccount, so this goes through the raw GraphQL escape hatch.
package crewcards

import (
	"context"

	crew "github.com/bemeek-io/go-crew"
)

const query = `query DebitCardsWithPocket {
  currentUser {
    debitCards { id name lastFour status formFactor frozenStatus subaccount { id name } }
    virtualDebitCards { id name lastFour status formFactor frozenStatus subaccount { id name } }
  }
}`

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

type rawCard struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	LastFour     string `json:"lastFour"`
	Status       string `json:"status"`
	FormFactor   string `json:"formFactor"`
	FrozenStatus string `json:"frozenStatus"`
	Subaccount   *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"subaccount"`
}

func (r rawCard) toCard() Card {
	c := Card{
		ID: r.ID, Name: r.Name, LastFour: r.LastFour,
		Status: r.Status, FormFactor: r.FormFactor, FrozenStatus: r.FrozenStatus,
	}
	if r.Subaccount != nil {
		c.SubaccountID, c.SubaccountName = r.Subaccount.ID, r.Subaccount.Name
	}
	return c
}

// Fetch returns the user's real debit cards — the one piece of plastic they
// carry. Crew also issues a virtual card per merchant (a couple dozen on an
// active account); those are managed in Crew itself and are dropped here so
// they never reach the snapshot or the home screen.
//
// Both card lists are queried because the physical card's list membership
// isn't guaranteed, and the form factor is the thing actually being selected on.
//
// A nil error with no cards is normal; an error is returned so callers can
// leave the previous snapshot in place rather than blanking the list on a
// transient failure.
func Fetch(ctx context.Context, client *crew.Client) ([]Card, error) {
	var out struct {
		CurrentUser struct {
			DebitCards        []rawCard `json:"debitCards"`
			VirtualDebitCards []rawCard `json:"virtualDebitCards"`
		} `json:"currentUser"`
	}
	if err := client.Execute(ctx, query, nil, &out); err != nil {
		return nil, err
	}
	var cards []Card
	seen := map[string]bool{}
	for _, r := range append(out.CurrentUser.DebitCards, out.CurrentUser.VirtualDebitCards...) {
		if r.ID == "" || seen[r.ID] || r.FormFactor != formPhysical {
			continue
		}
		seen[r.ID] = true
		cards = append(cards, r.toCard())
	}
	return cards, nil
}

const formPhysical = "PHYSICAL"

// MovePocket points a card's spend at a different pocket.
func MovePocket(ctx context.Context, client *crew.Client, cardID, subaccountID string) error {
	_, err := client.UpdateVirtualDebitCard(ctx, crew.UpdateVirtualDebitCardInput{
		DebitCardID:  cardID,
		SubaccountID: subaccountID,
	})
	return err
}
