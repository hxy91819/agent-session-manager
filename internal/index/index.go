package index

import (
	"sort"
	"strings"
	"time"

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
	return false
}
