// Package crewcards reads debit cards together with the pocket (subaccount)
// their spend is drawn from. The SDK's DebitCard model doesn't query the
// subaccount, so this goes through the raw GraphQL escape hatch.
package crewcards

import (
	"context"

	crew "github.com/bemeek-io/go-crew"
)

// A physical card's pocket is NOT DebitCard.subaccount — that is always null
// for physical cards and is only set on per-merchant virtual cards. The pocket
// a physical card swipes from is the account's primarySubaccount.
//
// Crew's published docs also describe a User.selectedSpendSubaccount (the
// per-user override that setSpendSubaccount writes), but the live API rejects
// that field on User — the docs are ahead of the deployed schema. Until it
// lands, primarySubaccount is the source of truth for display, which matches
// what the card actually spent from in the transaction history.
const query = `query DebitCardsWithPocket {
  currentUser {
    id
    accounts { id primarySubaccount { id name } }
    debitCards { id name lastFour status formFactor frozenStatus account { id } subaccount { id name } }
    virtualDebitCards { id name lastFour status formFactor frozenStatus account { id } subaccount { id name } }
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

type pocketRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type rawCard struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	LastFour     string     `json:"lastFour"`
	Status       string     `json:"status"`
	FormFactor   string     `json:"formFactor"`
	FrozenStatus string     `json:"frozenStatus"`
	Subaccount   *pocketRef `json:"subaccount"`
	Account      *struct {
		ID string `json:"id"`
	} `json:"account"`
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
			ID       string `json:"id"`
			Accounts []struct {
				ID                string     `json:"id"`
				PrimarySubaccount *pocketRef `json:"primarySubaccount"`
			} `json:"accounts"`
			DebitCards        []rawCard `json:"debitCards"`
			VirtualDebitCards []rawCard `json:"virtualDebitCards"`
		} `json:"currentUser"`
	}
	if err := client.Execute(ctx, query, nil, &out); err != nil {
		return nil, err
	}
	// Default pocket per account, used when the user hasn't picked one.
	primary := map[string]*pocketRef{}
	for _, a := range out.CurrentUser.Accounts {
		primary[a.ID] = a.PrimarySubaccount
	}
	var cards []Card
	seen := map[string]bool{}
	for _, r := range append(out.CurrentUser.DebitCards, out.CurrentUser.VirtualDebitCards...) {
		if r.ID == "" || seen[r.ID] || r.FormFactor != formPhysical {
			continue
		}
		seen[r.ID] = true
		c := r.toCard()
		var pocket *pocketRef
		if r.Account != nil {
			pocket = primary[r.Account.ID]
		}
		if pocket != nil {
			c.SubaccountID, c.SubaccountName = pocket.ID, pocket.Name
		}
		cards = append(cards, c)
	}
	return cards, nil
}

const formPhysical = "PHYSICAL"

// The payload's result is a User. Only `id` is selected: the docs advertise
// User.selectedSpendSubaccount, but the live schema rejects it, so selecting it
// here would make every move fail validation.
const setSpendMutation = `mutation SetSpend($input: SetSpendSubaccountInput!) {
  setSpendSubaccount(input: $input) { result { id } }
}`

const currentUserIDQuery = `query CurrentUserID { currentUser { id } }`

// MovePocket points the member's physical card at a different pocket.
//
// This is a user-level setting, not a card-level one: setSpendSubaccount takes
// the user whose spend setting changes, and Crew applies it to their card
// swipes. UpdateVirtualDebitCard is the wrong mutation here — it only binds a
// per-merchant virtual card to a pocket and silently does nothing useful for a
// physical card.
func MovePocket(ctx context.Context, client *crew.Client, cardID, subaccountID string) error {
	var who struct {
		CurrentUser struct {
			ID string `json:"id"`
		} `json:"currentUser"`
	}
	if err := client.Execute(ctx, currentUserIDQuery, nil, &who); err != nil {
		return err
	}
	var out struct {
		SetSpendSubaccount struct {
			Result struct {
				ID string `json:"id"`
			} `json:"result"`
		} `json:"setSpendSubaccount"`
	}
	input := map[string]any{
		"userId":                    who.CurrentUser.ID,
		"selectedSpendSubaccountId": subaccountID,
	}
	return client.Execute(ctx, setSpendMutation, map[string]any{"input": input}, &out)
}
