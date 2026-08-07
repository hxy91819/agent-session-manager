package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type asmTestEnv struct {
	CacheHome    string
	ProviderHome map[string]string
}

func newASMTestEnv(t testing.TB) asmTestEnv {
	t.Helper()
	providers := make(map[string]string)
	for _, name := range []string{
		"codex", "claude", "kimi", "kiro", "opencode", "codebuddy",
		"cursor", "openclaw", "zcode",
	} {
		providers[name] = t.TempDir()
	}
	return asmTestEnv{
		CacheHome:    t.TempDir(),
		ProviderHome: providers,
	}
}

func (e asmTestEnv) Run(t testing.TB, args ...string) (string, error) {
	t.Helper()
	cmdArgs := append([]string{"run", "./cmd/asm"}, args...)
	cmd := exec.Command("go", cmdArgs...)
	cmd.Dir = ".."
	cmd.Env = e.commandEnv(t)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (e asmTestEnv) Build(t testing.TB) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "asm")
	cmd := exec.Command("go", "build", "-o", binary, "./cmd/asm")
	cmd.Dir = ".."
	cmd.Env = e.commandEnv(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build asm: %v\n%s", err, out)
	}
	return binary
}

func (e asmTestEnv) RunBinary(t testing.TB, binary string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = ".."
	cmd.Env = e.commandEnv(t)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (e asmTestEnv) commandEnv(t testing.TB) []string {
	t.Helper()
	controlled := map[string]struct{}{
		"XDG_CACHE_HOME": {}, "CODEX_HOME": {}, "CLAUDE_HOME": {},
		"KIMI_CODE_HOME": {}, "KIMI_HOME": {}, "KIRO_HOME": {},
		"OPENCODE_HOME": {}, "OPENCODE_DATA_HOME": {}, "OPENCODE_DATA_DIR": {},
		"CODEBUDDY_HOME": {}, "CURSOR_HOME": {}, "OPENCLAW_STATE_DIR": {},
		"OPENCLAW_HOME": {}, "ZCODE_HOME": {}, "ASM_CODEX_EXTRA_HOMES": {},
		"ASM_CLAUDE_EXTRA_HOMES": {},
	}
	env := make([]string, 0, len(os.Environ())+len(controlled))
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if _, ok := controlled[key]; !ok {
			env = append(env, item)
		}
	}
	goCache := os.Getenv("GOCACHE")
	if goCache == "" {
		cacheDir, err := os.UserCacheDir()
		if err != nil {
			t.Fatal(err)
		}
		goCache = filepath.Join(cacheDir, "go-build")
	}
	return append(env,
		"GOCACHE="+goCache,
		"XDG_CACHE_HOME="+e.CacheHome,
		"CODEX_HOME="+e.ProviderHome["codex"],
		"CLAUDE_HOME="+e.ProviderHome["claude"],
		"KIMI_CODE_HOME="+e.ProviderHome["kimi"],
		"KIMI_HOME=",
		"KIRO_HOME="+e.ProviderHome["kiro"],
		"OPENCODE_HOME="+e.ProviderHome["opencode"],
		"OPENCODE_DATA_HOME=",
		"OPENCODE_DATA_DIR=",
		"CODEBUDDY_HOME="+e.ProviderHome["codebuddy"],
		"CURSOR_HOME="+e.ProviderHome["cursor"],
		"OPENCLAW_STATE_DIR="+e.ProviderHome["openclaw"],
		"OPENCLAW_HOME=",
		"ZCODE_HOME="+e.ProviderHome["zcode"],
		"ASM_CODEX_EXTRA_HOMES=",
		"ASM_CLAUDE_EXTRA_HOMES=",
	)
}
