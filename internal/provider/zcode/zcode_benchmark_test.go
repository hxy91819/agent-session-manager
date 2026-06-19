package zcode

import (
	"fmt"
	"testing"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func BenchmarkDiscoverCold(b *testing.B) {
	home := makeBenchmarkZCodeStore(b)
	provider := New(home)
	opts := session.DiscoverOptions{}

	b.ReportAllocs()
	for b.Loop() {
		got, err := provider.Discover(opts)
		if err != nil {
			b.Fatal(err)
		}
		if len(got) == 0 {
			b.Fatal("expected sessions")
		}
	}
}

func BenchmarkDiscoverWithPreviews(b *testing.B) {
	home := makeBenchmarkZCodeStore(b)
	provider := New(home)
	opts := session.DiscoverOptions{
		Preview: session.PreviewOptions{UserMessagesPerEdge: 2, MaxChars: 500},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		got, err := provider.Discover(opts)
		if err != nil {
			b.Fatal(err)
		}
		if len(got) == 0 {
			b.Fatal("expected sessions")
		}
	}
}

func makeBenchmarkZCodeStore(b *testing.B) string {
	b.Helper()
	home := b.TempDir()
	db := createZCodeDB(b, home)
	defer closeDB(b, db)

	for i := range 40 {
		sess := writeZCodeSession(b, db, zcodeSession{
			ID:          fmt.Sprintf("sess_%03d", i),
			Directory:   "/repo/bench",
			Title:       fmt.Sprintf("bench session %03d", i),
			TitleSource: "generated",
			TimeCreated: 1781000000000 + int64(i)*1000,
			TimeUpdated: 1781000000000 + int64(i)*1000,
		})
		for j := range 8 {
			addUserMessage(b, db, sess, fmt.Sprintf("msg_%03d_%02d", i, j),
				sess.TimeCreated+int64(j)*100,
				fmt.Sprintf("bench prompt %03d turn %02d with enough text to make sqlite parsing visible", i, j))
		}
	}
	return home
}
