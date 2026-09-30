package categorize

import (
	"testing"

	"github.com/bemeek-io/crewmate/internal/push"
)

// Each person hears about a transaction once: the nudge if there is one and
// they want nudges, otherwise the ordinary push, otherwise nothing.
func TestPickPush(t *testing.T) {
	regular := &push.Notification{Title: "regular"}
	nudge := &push.Notification{Title: "nudge"}
	cases := []struct {
		name           string
		regular, nudge *push.Notification
		wants          bool
		want           *push.Notification
	}{
		{"nudge for someone who wants them", regular, nudge, true, nudge},
		{"ordinary push for someone who doesn't", regular, nudge, false, regular},
		{"silent transaction, nudges wanted", nil, nudge, true, nudge},
		{"silent transaction, nudges off", nil, nudge, false, nil},
		{"no suggestion matched", regular, nil, true, regular},
	}
	for _, c := range cases {
		if got := pickPush(c.regular, c.nudge, c.wants); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
