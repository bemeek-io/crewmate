package watcher

import (
	"testing"
	"time"

	crew "github.com/bemeek-io/go-crew"

	"github.com/bemeek-io/crewmate/internal/store"
)

func at(daysAgo int) time.Time {
	return time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC).AddDate(0, 0, -daysAgo)
}

func crewTxns(ids []string, days []int) []crew.CashTransaction {
	out := make([]crew.CashTransaction, len(ids))
	for i, id := range ids {
		out[i] = crew.CashTransaction{ID: id, OccurredAt: at(days[i])}
	}
	return out
}

func localTxn(crewID string, daysAgo int) *store.Transaction {
	return &store.Transaction{CrewTxnID: crewID, OccurredAt: at(daysAgo)}
}

func ids(txns []*store.Transaction) []string {
	out := make([]string, len(txns))
	for i, t := range txns {
		out[i] = t.CrewTxnID
	}
	return out
}

// A page with more behind it speaks only for its own window; the last page is
// the end of the list and so speaks for everything.
func TestPageFloor(t *testing.T) {
	page := crewTxns([]string{"a", "b", "c"}, []int{0, 5, 2})
	floor, ok := pageFloor(page, true)
	if !ok {
		t.Fatal("page vouches for nothing")
	}
	if !floor.Equal(at(5)) {
		t.Errorf("floor = %v, want oldest entry %v", floor, at(5))
	}

	if floor, ok := pageFloor(page, false); !ok || !floor.IsZero() {
		t.Errorf("last page: floor = %v, ok = %v, want zero time and ok", floor, ok)
	}

	if _, ok := pageFloor(nil, false); ok {
		t.Error("empty page vouches for something")
	}
}

// A short page is not the end of the list. Crew may return fewer rows than
// asked for and still report more behind them; reading that as the whole of
// history would sweep every stored pending transaction the page omits.
func TestPageFloorShortPageIsNotTheEnd(t *testing.T) {
	short := crewTxns([]string{"a", "b"}, []int{0, 4})
	floor, ok := pageFloor(short, true)
	if !ok {
		t.Fatal("short page vouches for nothing")
	}
	if floor.IsZero() {
		t.Fatal("short page claimed the whole of history")
	}
	if !floor.Equal(at(4)) {
		t.Errorf("floor = %v, want oldest entry %v", floor, at(4))
	}

	// The guarantee that matters: a pending charge older than the short page
	// survives, rather than being read as cancelled.
	local := []*store.Transaction{localTxn("a", 0), localTxn("buried", 30)}
	if got := vanished(crewIDs(short), local, floor); len(got) != 0 {
		t.Errorf("vanished() = %v, want none", ids(got))
	}
}

// The sweep must catch a cancelled pending charge and nothing else — above all
// not a transaction that merely fell off the end of the walk.
func TestVanished(t *testing.T) {
	seen := crewIDs(crewTxns([]string{"still-here", "also-here"}, []int{0, 3}))
	floor := at(3)
	local := []*store.Transaction{
		localTxn("still-here", 0),
		localTxn("cancelled", 1),  // inside the window, gone from Crew
		localTxn("older-walk", 9), // before the floor: the walk just ran out
		localTxn("also-here", 3),
	}

	got := vanished(seen, local, floor)
	if len(got) != 1 || got[0].CrewTxnID != "cancelled" {
		t.Errorf("vanished() = %v, want [cancelled]", ids(got))
	}
}

// A walk that reached the end of the account has a zero floor, so age no longer
// shields a cancelled charge buried under newer activity — the case that leaves
// a stuck uncategorized row behind.
func TestVanishedWholeHistory(t *testing.T) {
	seen := crewIDs(crewTxns([]string{"kept"}, []int{0}))
	local := []*store.Transaction{localTxn("kept", 0), localTxn("cancelled", 400)}

	got := vanished(seen, local, time.Time{})
	if len(got) != 1 || got[0].CrewTxnID != "cancelled" {
		t.Errorf("vanished() = %v, want [cancelled]", ids(got))
	}
}
