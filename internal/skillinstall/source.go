package skillinstall

import (
	"fmt"
	"net/url"
	"strings"
)

type GitHubSource struct {
	Owner    string
	Repo     string
	Ref      string
	Path     string
	TreePath string
}

func ParseGitHubSource(raw string, refOverride string, pathOverride string) (GitHubSource, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return GitHubSource{}, fmt.Errorf("github source is required")
	}

	source, err := parseGitHubSource(raw)
	if err != nil {
		return GitHubSource{}, err
	}
	if strings.TrimSpace(refOverride) != "" {
		source.Ref = strings.TrimSpace(refOverride)
	}
	if strings.TrimSpace(pathOverride) != "" {
		source.Path = cleanSlashPath(pathOverride)
	}
	if source.Owner == "" || source.Repo == "" {
		return GitHubSource{}, fmt.Errorf("github source must include owner and repo")
	}
	return source, nil
}

func parseGitHubSource(raw string) (GitHubSource, error) {
	if strings.HasPrefix(raw, "git@github.com:") {
		raw = strings.TrimPrefix(raw, "git@github.com:")
		raw = strings.TrimSuffix(raw, ".git")
		return parseOwnerRepoPath(raw)
	}
	if !strings.Contains(raw, "://") {
		raw = strings.TrimPrefix(raw, "github.com/")
		raw = strings.TrimSuffix(raw, ".git")
		return parseOwnerRepoPath(raw)
	}

	u, err := url.Parse(raw)
	if err != nil {
		return GitHubSource{}, err
	}
	if u.Host != "github.com" && u.Host != "www.github.com" {
		return GitHubSource{}, fmt.Errorf("unsupported github host %q", u.Host)
	}
	return parseOwnerRepoPath(strings.TrimPrefix(u.Path, "/"))
}

func parseOwnerRepoPath(path string) (GitHubSource, error) {
	parts := strings.Split(cleanSlashPath(path), "/")
	if len(parts) < 2 {
		return GitHubSource{}, fmt.Errorf("github source must look like owner/repo or https://github.com/owner/repo")
	}
	source := GitHubSource{
		Owner: parts[0],
		Repo:  strings.TrimSuffix(parts[1], ".git"),
	}
	if len(parts) >= 4 && parts[2] == "tree" {
		source.TreePath = strings.Join(parts[3:], "/")
		return source, nil
	}
	if len(parts) > 2 {
		source.Path = strings.Join(parts[2:], "/")
	}
	return source, nil
}

func cleanSlashPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, "/")
	cleaned := make([]string, 0)
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." {
			continue
		}
		cleaned = append(cleaned, part)
	}
	return strings.Join(cleaned, "/")
}
