package skillinstall

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	ScopeCurrent = "current"
	ScopeUser    = "user"
	TargetAgents = "agents"
	TargetClaude = "claude"
)

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type Fetcher struct {
	Client       HTTPClient
	APIBase      string
	CodeloadBase string
	Token        string
	Owner        string
	Repo         string
}

type Skill struct {
	Name  string
	Files []SkillFile
}

type SkillFile struct {
	Path string
	Data []byte
	Mode fs.FileMode
}

type InstallOptions struct {
	Scopes     []string
	Targets    []string
	CurrentDir string
	UserHome   string
	DryRun     bool
}

type InstallResult struct {
	Skill string
	Path  string
}

func (f Fetcher) Fetch(ctx context.Context, source GitHubSource) ([]Skill, error) {
	var err error
	source, err = f.resolveTreeSource(ctx, source)
	if err != nil {
		return nil, err
	}
	if source.Ref == "" {
		ref, err := f.defaultBranch(ctx, source)
		if err != nil {
			return nil, err
		}
		source.Ref = ref
	}
	data, err := f.downloadArchive(ctx, source)
	if err != nil {
		return nil, err
	}
	return skillsFromZip(data, source.Path)
}

func (f Fetcher) FetchRelease(ctx context.Context, version string) ([]Skill, error) {
	assetURL, err := f.releaseAssetURL(ctx, version)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return nil, err
	}
	f.decorate(req)
	resp, err := f.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("github skills asset download failed: %s", resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return skillsFromZip(data, "")
}

func Install(skills []Skill, opts InstallOptions) ([]InstallResult, error) {
	if len(skills) == 0 {
		return nil, fmt.Errorf("no skills selected")
	}
	roots, err := installRoots(opts)
	if err != nil {
		return nil, err
	}

	var results []InstallResult
	for _, skill := range skills {
		if !validSkillName(skill.Name) {
			return nil, fmt.Errorf("invalid skill name %q", skill.Name)
		}
		for _, root := range roots {
			dest := filepath.Join(root, skill.Name)
			results = append(results, InstallResult{Skill: skill.Name, Path: dest})
			if opts.DryRun {
				continue
			}
			if err := installSkill(skill, dest); err != nil {
				return nil, err
			}
		}
	}
	return results, nil
}

func ValidScope(value string) bool {
	return value == ScopeCurrent || value == ScopeUser
}

func ValidTarget(value string) bool {
	return value == TargetAgents || value == TargetClaude
}

func (f Fetcher) httpClient() HTTPClient {
	if f.Client != nil {
		return f.Client
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (f Fetcher) apiBase() string {
	if f.APIBase != "" {
		return strings.TrimRight(f.APIBase, "/")
	}
	return "https://api.github.com"
}

func (f Fetcher) codeloadBase() string {
	if f.CodeloadBase != "" {
		return strings.TrimRight(f.CodeloadBase, "/")
	}
	return "https://codeload.github.com"
}

func (f Fetcher) ownerRepo() (string, string) {
	owner := f.Owner
	if owner == "" {
		owner = "hxy91819"
	}
	repo := f.Repo
	if repo == "" {
		repo = "agent-session-manager"
	}
	return owner, repo
}

func (f Fetcher) releaseAssetURL(ctx context.Context, version string) (string, error) {
	owner, repo := f.ownerRepo()
	releasePath := "latest"
	if strings.TrimSpace(version) != "" {
		releasePath = "tags/" + strings.TrimSpace(version)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/%s/releases/%s", f.apiBase(), owner, repo, releasePath), nil)
	if err != nil {
		return "", err
	}
	f.decorate(req)
	resp, err := f.httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("github release lookup failed: %s", resp.Status)
	}
	var payload struct {
		Assets []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	for _, asset := range payload.Assets {
		if strings.HasPrefix(asset.Name, "asm_skills_") && strings.HasSuffix(asset.Name, ".zip") && asset.BrowserDownloadURL != "" {
			return asset.BrowserDownloadURL, nil
		}
	}
	return "", fmt.Errorf("release does not contain asm skills asset")
}

func (f Fetcher) defaultBranch(ctx context.Context, source GitHubSource) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/%s", f.apiBase(), source.Owner, source.Repo), nil)
	if err != nil {
		return "", err
	}
	f.decorate(req)
	resp, err := f.httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("github repo lookup failed: %s", resp.Status)
	}
	var payload struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.DefaultBranch == "" {
		return "", fmt.Errorf("github repo response missing default_branch")
	}
	return payload.DefaultBranch, nil
}

func (f Fetcher) resolveTreeSource(ctx context.Context, source GitHubSource) (GitHubSource, error) {
	treePath := cleanSlashPath(source.TreePath)
	if treePath == "" || source.Ref != "" {
		return source, nil
	}
	ref, repoPath, err := f.resolveTreeRef(ctx, source, treePath)
	if err != nil {
		return GitHubSource{}, err
	}
	source.Ref = ref
	if source.Path == "" {
		source.Path = repoPath
	}
	source.TreePath = ""
	return source, nil
}

func (f Fetcher) resolveTreeRef(ctx context.Context, source GitHubSource, treePath string) (string, string, error) {
	parts := strings.Split(cleanSlashPath(treePath), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", "", fmt.Errorf("github tree URL is missing a ref")
	}
	// GitHub tree URLs are ref/path ambiguous when branch or tag names contain
	// slashes. Resolve against real refs and choose the longest exact match so
	// copy-pasted URLs behave the same way GitHub renders them.
	refs, err := f.matchingRefs(ctx, source, parts[0])
	if err != nil {
		return "", "", err
	}
	for i := len(parts); i >= 1; i-- {
		candidate := strings.Join(parts[:i], "/")
		if refs[candidate] {
			return candidate, strings.Join(parts[i:], "/"), nil
		}
	}
	return "", "", fmt.Errorf("could not resolve github tree URL ref %q; pass --ref and --path explicitly", treePath)
}

func (f Fetcher) matchingRefs(ctx context.Context, source GitHubSource, prefix string) (map[string]bool, error) {
	out := make(map[string]bool)
	for _, namespace := range []string{"heads", "tags"} {
		names, err := f.matchingRefNames(ctx, source, namespace, prefix)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			out[name] = true
		}
	}
	return out, nil
}

func (f Fetcher) matchingRefNames(ctx context.Context, source GitHubSource, namespace string, prefix string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/%s/git/matching-refs/%s/%s",
		f.apiBase(), source.Owner, source.Repo, namespace, escapeSlashPath(prefix)), nil)
	if err != nil {
		return nil, err
	}
	f.decorate(req)
	resp, err := f.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("github ref lookup failed: %s", resp.Status)
	}
	var payload []struct {
		Ref string `json:"ref"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	prefixRef := "refs/" + namespace + "/"
	names := make([]string, 0, len(payload))
	for _, item := range payload {
		if name, ok := strings.CutPrefix(item.Ref, prefixRef); ok {
			names = append(names, name)
		}
	}
	return names, nil
}

func escapeSlashPath(value string) string {
	parts := strings.Split(value, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func (f Fetcher) downloadArchive(ctx context.Context, source GitHubSource) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/%s/%s/zip/%s", f.codeloadBase(), source.Owner, source.Repo, source.Ref), nil)
	if err != nil {
		return nil, err
	}
	f.decorate(req)
	resp, err := f.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("github archive download failed: %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func (f Fetcher) decorate(req *http.Request) {
	req.Header.Set("User-Agent", "asm")
	if f.Token != "" {
		req.Header.Set("Authorization", "Bearer "+f.Token)
	}
}

func skillsFromZip(data []byte, sourcePath string) ([]Skill, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	sourcePath = cleanSlashPath(sourcePath)

	entries := make(map[string][]*zip.File)
	for _, file := range zr.File {
		rel, ok := archiveRelativePath(file.Name)
		if !ok || file.FileInfo().IsDir() {
			continue
		}
		if sourcePath != "" && rel != sourcePath && !strings.HasPrefix(rel, sourcePath+"/") {
			continue
		}
		if path.Base(rel) == "SKILL.md" {
			dir := path.Dir(rel)
			if dir == "." {
				dir = ""
			}
			entries[dir] = nil
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no SKILL.md found in downloaded archive")
	}
	dirs := make([]string, 0, len(entries))
	for dir := range entries {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, file := range zr.File {
		rel, ok := archiveRelativePath(file.Name)
		if !ok || file.FileInfo().IsDir() {
			continue
		}
		if dir, ok := deepestMatchingDir(rel, dirs); ok {
			entries[dir] = append(entries[dir], file)
		}
	}

	skills := make([]Skill, 0, len(dirs))
	for _, dir := range dirs {
		skill, err := skillFromFiles(dir, entries[dir])
		if err != nil {
			return nil, err
		}
		skills = append(skills, skill)
	}
	return skills, nil
}

func archiveRelativePath(name string) (string, bool) {
	name = strings.TrimLeft(path.Clean(name), "/")
	if name == "." {
		return "", false
	}
	parts := strings.Split(name, "/")
	if len(parts) < 2 {
		return "", false
	}
	for _, part := range parts {
		if part == ".." {
			return "", false
		}
	}
	return strings.Join(parts[1:], "/"), true
}

func relInDir(rel string, dir string) bool {
	if dir == "" {
		return rel != ""
	}
	return rel == dir || strings.HasPrefix(rel, dir+"/")
}

func deepestMatchingDir(rel string, dirs []string) (string, bool) {
	best := ""
	found := false
	for _, dir := range dirs {
		if !relInDir(rel, dir) {
			continue
		}
		if !found || len(dir) > len(best) {
			best = dir
			found = true
		}
	}
	return best, found
}

func skillFromFiles(dir string, files []*zip.File) (Skill, error) {
	skill := Skill{Name: path.Base(dir)}
	for _, file := range files {
		rel := strings.TrimPrefix(file.Name, strings.Split(file.Name, "/")[0]+"/")
		rel = strings.TrimPrefix(rel, dir)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			continue
		}
		data, err := readZipFile(file)
		if err != nil {
			return Skill{}, err
		}
		if rel == "SKILL.md" {
			if name := skillNameFromMarkdown(string(data)); name != "" {
				skill.Name = name
			}
		}
		mode := file.FileInfo().Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		skill.Files = append(skill.Files, SkillFile{Path: rel, Data: data, Mode: mode})
	}
	if len(skill.Files) == 0 {
		return Skill{}, fmt.Errorf("skill %q has no files", skill.Name)
	}
	return skill, nil
}

func readZipFile(file *zip.File) ([]byte, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

func skillNameFromMarkdown(data string) string {
	lines := strings.Split(data, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "---" {
			return ""
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "name" {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		return value
	}
	return ""
}

func installRoots(opts InstallOptions) ([]string, error) {
	currentDir := opts.CurrentDir
	if currentDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		currentDir = cwd
	}
	userHome := opts.UserHome
	if userHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		userHome = home
	}

	var roots []string
	for _, scope := range opts.Scopes {
		for _, target := range opts.Targets {
			switch {
			case scope == ScopeCurrent && target == TargetAgents:
				roots = append(roots, filepath.Join(currentDir, ".agents", "skills"))
			case scope == ScopeCurrent && target == TargetClaude:
				roots = append(roots, filepath.Join(currentDir, ".claude", "skills"))
			case scope == ScopeUser && target == TargetAgents:
				roots = append(roots, filepath.Join(userHome, ".agents", "skills"))
			case scope == ScopeUser && target == TargetClaude:
				roots = append(roots, filepath.Join(userHome, ".claude", "skills"))
			default:
				return nil, fmt.Errorf("unsupported install destination scope=%q target=%q", scope, target)
			}
		}
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("at least one install destination is required")
	}
	return roots, nil
}

func installSkill(skill Skill, dest string) error {
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, "."+filepath.Base(dest)+"-*")
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tmp)
		}
	}()
	for _, file := range skill.Files {
		target, err := safeJoin(tmp, file.Path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, file.Data, file.Mode); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func validSkillName(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' && i > 0 && i < len(name)-1:
		default:
			return false
		}
	}
	return true
}

func safeJoin(root string, rel string) (string, error) {
	rel = filepath.Clean(rel)
	if rel == "." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", fmt.Errorf("unsafe archive path %q", rel)
	}
	return filepath.Join(root, rel), nil
}
