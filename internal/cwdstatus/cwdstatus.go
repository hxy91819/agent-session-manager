package cwdstatus

import (
	"errors"
	"io/fs"
	"os"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

type Checker struct {
	cache map[string]status
}

type status struct {
	missing bool
	err     string
}

func NewChecker() *Checker {
	return &Checker{cache: make(map[string]status)}
}

func (c *Checker) Mark(s *session.Session) {
	if s.Metadata == nil {
		s.Metadata = make(map[string]string)
	}
	delete(s.Metadata, "cwd_missing")
	delete(s.Metadata, "cwd_error")

	status, ok := c.cache[s.CWD]
	if !ok {
		status = check(s.CWD)
		c.cache[s.CWD] = status
	}
	if status.err != "" {
		s.Metadata["cwd_error"] = status.err
		return
	}
	if status.missing {
		s.Metadata["cwd_missing"] = "true"
	}
}

func check(cwd string) status {
	info, err := os.Stat(cwd)
	if err == nil && info.IsDir() {
		return status{}
	}
	if errors.Is(err, fs.ErrNotExist) || err == nil {
		return status{missing: true}
	}
	return status{err: err.Error()}
}
