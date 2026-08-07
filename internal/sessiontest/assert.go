package sessiontest

import (
	"reflect"
	"testing"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func RequireEqual(t testing.TB, want, got []session.Session) {
	t.Helper()
	if !Equal(want, got) {
		t.Fatalf("sessions differ:\ngot:  %#v\nwant: %#v", got, want)
	}
}

func Equal(want, got []session.Session) bool {
	return reflect.DeepEqual(canonicalSessions(want), canonicalSessions(got))
}

func canonicalSessions(items []session.Session) []session.Session {
	out := make([]session.Session, len(items))
	for i, item := range items {
		item.CreatedAt = canonicalTime(item.CreatedAt)
		item.UpdatedAt = canonicalTime(item.UpdatedAt)
		item.Previews = canonicalPreviews(item.Previews)
		item.Evidence = canonicalPreviews(item.Evidence)
		out[i] = item
	}
	return out
}

func canonicalPreviews(items []session.MessagePreview) []session.MessagePreview {
	if items == nil {
		return nil
	}
	out := append([]session.MessagePreview(nil), items...)
	for i := range out {
		out[i].At = canonicalTime(out[i].At)
	}
	return out
}

func canonicalTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return value.UTC()
}
