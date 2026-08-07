package codex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDynamicTitleCacheFallsBackForNonAppendMutations(t *testing.T) {
	tests := []struct {
		name   string
		layout func(string) string
		mutate func(*testing.T, string, time.Time)
		want   string
	}{
		{
			name: "truncate",
			layout: func(_ string) string {
				return dynamicSessionIndexRecord("sid", "old title")
			},
			mutate: func(t *testing.T, path string, _ time.Time) {
				writeFile(t, path, dynamicSessionIndexRecord("sid", "truncated title"))
			},
			want: "truncated title",
		},
		{
			name: "atomic replace with preserved timestamp",
			layout: func(_ string) string {
				return dynamicSessionIndexRecord("sid", "old-title")
			},
			mutate: func(t *testing.T, path string, modTime time.Time) {
				replacement := path + ".replacement"
				writeFile(t, replacement, dynamicSessionIndexRecord("sid", "new-title"))
				if err := os.Chtimes(replacement, modTime, modTime); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(replacement, path); err != nil {
					t.Fatal(err)
				}
			},
			want: "new-title",
		},
		{
			name: "middle rewrite with preserved timestamp",
			layout: func(_ string) string {
				return dynamicTitleFiller(dynamicTitleHashEdge) +
					dynamicSessionIndexRecord("sid", "old-title") +
					dynamicTitleFiller(dynamicTitleHashEdge)
			},
			mutate: func(t *testing.T, path string, modTime time.Time) {
				rewriteDynamicTitle(t, path, "old-title", "new-title")
				if err := os.Chtimes(path, modTime, modTime); err != nil {
					t.Fatal(err)
				}
			},
			want: "new-title",
		},
		{
			name: "middle rewrite before append",
			layout: func(_ string) string {
				return dynamicTitleFiller(dynamicTitleHashEdge) +
					dynamicSessionIndexRecord("sid", "old-title") +
					dynamicTitleFiller(dynamicTitleHashEdge)
			},
			mutate: func(t *testing.T, path string, _ time.Time) {
				rewriteDynamicTitle(t, path, "old-title", "new-title")
				appendFile(t, path, dynamicSessionIndexRecord("other", "appended"))
			},
			want: "new-title",
		},
		{
			name: "prefix rewrite before append",
			layout: func(filler string) string {
				return dynamicSessionIndexRecord("sid", "old-title") + filler
			},
			mutate: func(t *testing.T, path string, _ time.Time) {
				rewriteDynamicTitle(t, path, "old-title", "new-title")
				appendFile(t, path, dynamicSessionIndexRecord("other", "appended"))
			},
			want: "new-title",
		},
		{
			name: "append boundary rewrite",
			layout: func(filler string) string {
				return filler + dynamicSessionIndexRecord("sid", "old-title")
			},
			mutate: func(t *testing.T, path string, _ time.Time) {
				rewriteDynamicTitle(t, path, "old-title", "new-title")
				appendFile(t, path, dynamicSessionIndexRecord("other", "appended"))
			},
			want: "new-title",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session_index.jsonl")
			cachePath := filepath.Join(t.TempDir(), "dynamic.json")
			filler := dynamicTitleFiller(2 * dynamicTitleHashEdge)
			writeFile(t, path, tt.layout(filler))
			if got := readDynamicTitleForTest(t, cachePath, path, "sid"); got != "old title" && got != "old-title" {
				t.Fatalf("initial title = %q", got)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			tt.mutate(t, path, info.ModTime())
			if got := readDynamicTitleForTest(t, cachePath, path, "sid"); got != tt.want {
				t.Fatalf("changed title = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDynamicTitleCacheHandlesPartialCorruptAndOversizedRecords(t *testing.T) {
	t.Run("partial record completion rebuilds", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session_index.jsonl")
		cachePath := filepath.Join(t.TempDir(), "dynamic.json")
		writeFile(t, path, dynamicSessionIndexRecord("sid", "old title")+`{"id":"sid","thread_name":"new`)
		if got := readDynamicTitleForTest(t, cachePath, path, "sid"); got != "old title" {
			t.Fatalf("partial title = %q", got)
		}
		appendFile(t, path, ` title"}`+"\n")
		if got := readDynamicTitleForTest(t, cachePath, path, "sid"); got != "new title" {
			t.Fatalf("completed title = %q", got)
		}
	})

	t.Run("corrupt and empty records do not replace latest valid title", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session_index.jsonl")
		cachePath := filepath.Join(t.TempDir(), "dynamic.json")
		writeFile(t, path, dynamicSessionIndexRecord("sid", "old title"))
		if got := readDynamicTitleForTest(t, cachePath, path, "sid"); got != "old title" {
			t.Fatalf("initial title = %q", got)
		}
		appendFile(t, path, "not-json\n"+dynamicSessionIndexRecord("sid", "   ")+dynamicSessionIndexRecord("sid", "latest title"))
		if got := readDynamicTitleForTest(t, cachePath, path, "sid"); got != "latest title" {
			t.Fatalf("appended title = %q", got)
		}
	})

	t.Run("oversized record preserves full-parser boundary", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session_index.jsonl")
		cachePath := filepath.Join(t.TempDir(), "dynamic.json")
		writeFile(t, path, dynamicSessionIndexRecord("sid", "old title")+
			`{"id":"oversized","thread_name":"`+strings.Repeat("x", dynamicTitleMaxRecord)+`"}`+"\n"+
			dynamicSessionIndexRecord("sid", "hidden after oversized"))
		if got := readDynamicTitleForTest(t, cachePath, path, "sid"); got != "old title" {
			t.Fatalf("oversized title = %q", got)
		}
		appendFile(t, path, dynamicSessionIndexRecord("other", "appended"))
		if got := readDynamicTitleForTest(t, cachePath, path, "sid"); got != "old title" {
			t.Fatalf("oversized append title = %q", got)
		}
	})
}

func TestDynamicTitleCacheRebuildsFromMissingCorruptOrOldState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session_index.jsonl")
	cachePath := filepath.Join(t.TempDir(), "dynamic.json")
	writeFile(t, path, dynamicSessionIndexRecord("sid", "initial"))
	if got := readDynamicTitleForTest(t, cachePath, path, "sid"); got != "initial" {
		t.Fatalf("initial title = %q", got)
	}

	writeFile(t, cachePath, "{")
	writeFile(t, path, dynamicSessionIndexRecord("sid", "after corruption"))
	if got := readDynamicTitleForTest(t, cachePath, path, "sid"); got != "after corruption" {
		t.Fatalf("corrupt state title = %q", got)
	}

	writeFile(t, cachePath, `{"version":0,"entries":{}}`)
	writeFile(t, path, dynamicSessionIndexRecord("sid", "after old version"))
	if got := readDynamicTitleForTest(t, cachePath, path, "sid"); got != "after old version" {
		t.Fatalf("old state title = %q", got)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	cache := loadDynamicTitleCache(cachePath)
	if got := cache.read(path, dynamicTitleSessionIndex); got != nil {
		t.Fatalf("missing index titles = %#v", got)
	}
	if err := cache.save(cachePath); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, dynamicSessionIndexRecord("sid", "after recreate"))
	if got := readDynamicTitleForTest(t, cachePath, path, "sid"); got != "after recreate" {
		t.Fatalf("recreated title = %q", got)
	}
}

func TestDynamicTitleCacheRejectsValidJSONWithCorruptTitles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session_index.jsonl")
	cachePath := filepath.Join(t.TempDir(), "dynamic.json")
	writeFile(t, path, dynamicSessionIndexRecord("sid", "native title"))
	if got := readDynamicTitleForTest(t, cachePath, path, "sid"); got != "native title" {
		t.Fatalf("initial title = %q", got)
	}

	cache := loadDynamicTitleCache(cachePath)
	state := cache.Entries[path]
	state.Titles["sid"] = "corrupt cached title"
	cache.Entries[path] = state
	cache.dirty = true
	if err := cache.save(cachePath); err != nil {
		t.Fatal(err)
	}
	if got := readDynamicTitleForTest(t, cachePath, path, "sid"); got != "native title" {
		t.Fatalf("corrupt state title = %q, want native rebuild", got)
	}
}

func TestDynamicTitleCacheAtomicSaveFailureKeepsPreviousState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dynamic.json")
	cache := &dynamicTitleCache{
		Version: dynamicTitleCacheVersion,
		Entries: map[string]dynamicTitleIndexState{
			"index": {Kind: dynamicTitleSessionIndex, Titles: map[string]string{"sid": "before"}},
		},
		dirty: true,
	}
	if err := cache.save(path); err != nil {
		t.Fatal(err)
	}

	cache.Entries["index"] = dynamicTitleIndexState{Kind: dynamicTitleSessionIndex, Titles: map[string]string{"sid": "after"}}
	cache.dirty = true
	cache.renameFile = func(string, string) error { return errors.New("rename failed") }
	if err := cache.save(path); err == nil {
		t.Fatal("save succeeded, want rename failure")
	}
	if got := loadDynamicTitleCache(path).Entries["index"].Titles["sid"]; got != "before" {
		t.Fatalf("persisted title = %q, want previous valid state", got)
	}
}

func readDynamicTitleForTest(t *testing.T, cachePath, path, id string) string {
	t.Helper()
	cache := loadDynamicTitleCache(cachePath)
	titles := cache.read(path, dynamicTitleSessionIndex)
	if err := cache.save(cachePath); err != nil {
		t.Fatal(err)
	}
	return titles[id]
}

func dynamicSessionIndexRecord(id, title string) string {
	return fmt.Sprintf(`{"id":%q,"thread_name":%q}`+"\n", id, title)
}

func dynamicTitleFiller(minBytes int) string {
	var filler strings.Builder
	for i := 0; filler.Len() < minBytes; i++ {
		filler.WriteString(dynamicSessionIndexRecord(fmt.Sprintf("filler-%05d", i), strings.Repeat("x", 128)))
	}
	return filler.String()
}

func rewriteDynamicTitle(t *testing.T, path, oldTitle, newTitle string) {
	t.Helper()
	if len(oldTitle) != len(newTitle) {
		t.Fatalf("rewrite lengths differ: %q, %q", oldTitle, newTitle)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), oldTitle, newTitle, 1)
	if updated == string(data) {
		t.Fatalf("title %q not found", oldTitle)
	}
	writeFile(t, path, updated)
}
