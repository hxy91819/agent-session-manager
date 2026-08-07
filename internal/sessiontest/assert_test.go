package sessiontest

import (
	"testing"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func TestEqualComparesSessionTimesByInstant(t *testing.T) {
	local := time.FixedZone("UTC+8", 8*60*60)
	want := []session.Session{{
		ID:        "sid",
		CreatedAt: time.Date(2026, 8, 7, 10, 0, 0, 0, local),
		UpdatedAt: time.Date(2026, 8, 7, 10, 5, 0, 0, local),
		Previews: []session.MessagePreview{{
			Text: "preview",
			At:   time.Date(2026, 8, 7, 10, 1, 0, 0, local),
		}},
		Evidence: []session.MessagePreview{{
			Text: "evidence",
			At:   time.Date(2026, 8, 7, 10, 2, 0, 0, local),
		}},
	}}
	got := []session.Session{{
		ID:        "sid",
		CreatedAt: time.Date(2026, 8, 7, 2, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 8, 7, 2, 5, 0, 0, time.UTC),
		Previews: []session.MessagePreview{{
			Text: "preview",
			At:   time.Date(2026, 8, 7, 2, 1, 0, 0, time.UTC),
		}},
		Evidence: []session.MessagePreview{{
			Text: "evidence",
			At:   time.Date(2026, 8, 7, 2, 2, 0, 0, time.UTC),
		}},
	}}

	if !Equal(want, got) {
		t.Fatalf("same instants compared unequal:\nwant=%#v\ngot=%#v", want, got)
	}
	got[0].UpdatedAt = got[0].UpdatedAt.Add(time.Second)
	if Equal(want, got) {
		t.Fatal("different instants compared equal")
	}
	got[0].UpdatedAt = want[0].UpdatedAt
	got[0].Title = "different title"
	if Equal(want, got) {
		t.Fatal("non-time fields compared equal")
	}
}
