package index

import (
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

type SortMode string

const (
	SortActive  SortMode = "active"
	SortCreated SortMode = "created"
	SortProject SortMode = "project"
)

type Query struct {
	Search string
	Sort   SortMode
}

func FilterAndSort(sessions []session.Session, q Query) []session.Session {
	out := make([]session.Session, 0, len(sessions))
	needle := strings.ToLower(strings.TrimSpace(q.Search))
	for _, s := range sessions {
		if needle == "" || matches(s, needle) {
			out = append(out, s)
		}
	}

	switch q.Sort {
	case SortCreated:
		sort.SliceStable(out, func(i, j int) bool {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		})
	case SortProject:
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].CWD == out[j].CWD {
				return out[i].UpdatedAt.After(out[j].UpdatedAt)
			}
			return out[i].CWD < out[j].CWD
		})
	default:
		sort.SliceStable(out, func(i, j int) bool {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		})
	}

	return out
}

func GroupProjects(sessions []session.Session) []session.Project {
	byCWD := make(map[string][]session.Session)
	for _, s := range sessions {
		byCWD[s.CWD] = append(byCWD[s.CWD], s)
	}

	projects := make([]session.Project, 0, len(byCWD))
	for cwd, items := range byCWD {
		updated := time.Time{}
		for _, item := range items {
			if item.UpdatedAt.After(updated) {
				updated = item.UpdatedAt
			}
		}
		projects = append(projects, session.Project{
			CWD:      cwd,
			Count:    len(items),
			Updated:  updated,
			Sessions: items,
		})
	}

	sort.SliceStable(projects, func(i, j int) bool {
		return projects[i].Updated.After(projects[j].Updated)
	})
	return projects
}

func matches(s session.Session, needle string) bool {
	values := []string{s.ID, s.Provider, s.CWD, s.Title, s.Path}
	for _, v := range values {
		if strings.Contains(strings.ToLower(v), needle) {
			return true
		}
	}
	for k, v := range s.Metadata {
		if strings.Contains(strings.ToLower(k), needle) || strings.Contains(strings.ToLower(v), needle) {
			return true
		}
	}
	for _, message := range s.SearchMessages {
		if strings.Contains(strings.ToLower(message.Text), needle) {
			return true
		}
	}
	for _, location := range s.RuntimeLocations {
		for _, value := range []string{
			location.Runtime,
			location.WorkspaceID,
			location.TabID,
			location.PaneID,
			location.AgentStatus,
		} {
			if strings.Contains(strings.ToLower(value), needle) {
				return true
			}
		}
	}
	return false
}

// ContentMatch is one piece of search evidence: where a matched user message
// sits in its raw session file, when it was sent, and a bounded excerpt. The
// offset lets agents slice the original transcript without parsing
// provider-specific formats; the snippet spares them from reading it at all
// when a glance at context is enough.
type ContentMatch struct {
	Offset  int64     `json:"offset"`
	Snippet string    `json:"snippet"`
	At      time.Time `json:"at,omitempty"`
}

const (
	// MaxEvidencePerSession bounds evidence entries so one very chatty
	// session cannot flood machine consumers.
	MaxEvidencePerSession = 3
	snippetContextRunes   = 80
)

// SearchEvidence returns bounded excerpts for query matches inside the
// session's denoised user-message corpus. Field-level hits (title, cwd, id)
// are already present in every output payload, so they produce no evidence.
func SearchEvidence(s session.Session, query string) []ContentMatch {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return nil
	}
	var matches []ContentMatch
	for _, message := range s.SearchMessages {
		snippet := snippetAround(message.Text, needle)
		if snippet == "" {
			continue
		}
		matches = append(matches, ContentMatch{
			Offset:  message.Offset,
			Snippet: snippet,
			At:      message.At,
		})
		if len(matches) >= MaxEvidencePerSession {
			break
		}
	}
	return matches
}

// snippetAround returns the needle with up to snippetContextRunes runes of
// surrounding context on each side. Rune-based windows keep CJK text intact.
func snippetAround(text, needle string) string {
	index := strings.Index(strings.ToLower(text), needle)
	if index < 0 {
		return ""
	}
	runes := []rune(text)
	start := utf8.RuneCountInString(text[:index]) - snippetContextRunes
	if start < 0 {
		start = 0
	}
	end := start + utf8.RuneCountInString(needle) + 2*snippetContextRunes
	if end > len(runes) {
		end = len(runes)
	}
	snippet := strings.Join(strings.Fields(string(runes[start:end])), " ")
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(runes) {
		snippet += "…"
	}
	return snippet
}
