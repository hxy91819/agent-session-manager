package tests

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCLIHerdrIntegration(t *testing.T) {
	buildEnv := newASMTestEnv(t)
	asmBinary := buildEnv.Build(t)
	fakeBinary, fakeBinDir := buildHerdrFake(t)
	installFakeCommand(t, fakeBinary, fakeBinDir, "herdr")
	installFakeCommand(t, fakeBinary, fakeBinDir, "codex")

	t.Run("json exposes exact supported provider locations", func(t *testing.T) {
		cases := []struct {
			name     string
			provider string
			source   string
			setup    func(testing.TB, asmTestEnv, string, string)
		}{
			{
				name: "codex", provider: "codex", source: "herdr:codex",
				setup: func(t testing.TB, env asmTestEnv, id, cwd string) {
					writeSession(t, filepath.Join(env.ProviderHome["codex"], "sessions", "2026", "08", "11", id+".jsonl"), id, cwd)
				},
			},
			{
				name: "claude", provider: "claude", source: "herdr:claude",
				setup: func(t testing.TB, env asmTestEnv, id, cwd string) {
					writeClaudeSession(t, filepath.Join(env.ProviderHome["claude"], "projects", "repo", id+".jsonl"), id, cwd, "Claude Herdr session")
				},
			},
			{
				name: "kimi", provider: "kimi", source: "herdr:kimi",
				setup: func(t testing.TB, env asmTestEnv, id, cwd string) {
					writeKimiSession(t, env.ProviderHome["kimi"], filepath.Join(env.ProviderHome["kimi"], "sessions", id), id, cwd, "Kimi Herdr session")
				},
			},
			{
				name: "opencode", provider: "opencode", source: "herdr:opencode",
				setup: func(t testing.TB, env asmTestEnv, id, cwd string) {
					writeOpencodeSession(t, env.ProviderHome["opencode"], "project-herdr", id, cwd, "opencode Herdr session")
				},
			},
			{
				name: "cursor", provider: "cursor", source: "herdr:cursor",
				setup: func(t testing.TB, env asmTestEnv, id, cwd string) {
					writeCursorSession(t, env.ProviderHome["cursor"], id, cwd, "Cursor Herdr session")
				},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				env := newASMTestEnv(t)
				id := "herdr-" + tc.name
				cwd := t.TempDir()
				tc.setup(t, env, id, cwd)
				out, err := env.RunBinaryWithEnv(t, asmBinary, herdrEnv(fakeBinDir, agentListJSON(tc.source, tc.provider, id, "w7", "w7:t3", "w7:p9")), "--since-days", "0", "--json")
				if err != nil {
					t.Fatalf("asm --json: %v\n%s", err, out)
				}
				var payload struct {
					Sessions []struct {
						ID               string `json:"id"`
						RuntimeLocations []struct {
							Runtime     string `json:"runtime"`
							WorkspaceID string `json:"workspace_id"`
							TabID       string `json:"tab_id"`
							PaneID      string `json:"pane_id"`
							AgentStatus string `json:"agent_status"`
						} `json:"runtime_locations"`
					} `json:"sessions"`
				}
				if err := json.Unmarshal([]byte(out), &payload); err != nil {
					t.Fatalf("invalid JSON: %v\n%s", err, out)
				}
				var locations []struct {
					Runtime     string `json:"runtime"`
					WorkspaceID string `json:"workspace_id"`
					TabID       string `json:"tab_id"`
					PaneID      string `json:"pane_id"`
					AgentStatus string `json:"agent_status"`
				}
				for _, item := range payload.Sessions {
					if item.ID == id {
						locations = item.RuntimeLocations
						break
					}
				}
				if len(locations) != 1 || locations[0].Runtime != "herdr" || locations[0].WorkspaceID != "w7" || locations[0].TabID != "w7:t3" || locations[0].PaneID != "w7:p9" || locations[0].AgentStatus != "working" {
					t.Fatalf("runtime locations = %#v", locations)
				}
			})
		}
	})

	t.Run("resume focuses live pane without starting provider", func(t *testing.T) {
		env := newASMTestEnv(t)
		id := "focus-session"
		cwd := filepath.Join(t.TempDir(), "missing")
		writeSession(t, filepath.Join(env.ProviderHome["codex"], "sessions", "2026", "08", "11", id+".jsonl"), id, cwd)
		focusFile := filepath.Join(t.TempDir(), "focus")
		agentFile := filepath.Join(t.TempDir(), "agent")
		extra := herdrEnv(fakeBinDir, agentListJSON("herdr:codex", "codex", id, "w8", "w8:t2", "w8:p4"))
		extra["ASM_FAKE_HERDR_FOCUS_FILE"] = focusFile
		extra["ASM_FAKE_AGENT_FILE"] = agentFile
		out, err := env.RunBinaryWithEnv(t, asmBinary, extra, "resume", "--provider", "codex", "--since-days", "0", id)
		if err != nil {
			t.Fatalf("resume: %v\n%s", err, out)
		}
		focused, err := os.ReadFile(focusFile)
		if err != nil || string(focused) != "w8:p4" {
			t.Fatalf("focused pane = %q err=%v", focused, err)
		}
		if _, err := os.Stat(agentFile); !os.IsNotExist(err) {
			t.Fatalf("provider unexpectedly started: %v", err)
		}

		out, err = env.RunBinaryWithEnv(t, asmBinary, extra, "resume", "--provider", "codex", "--since-days", "0", "--print-exec", id)
		if err != nil {
			t.Fatalf("print focus: %v\n%s", err, out)
		}
		if !strings.Contains(out, "'agent' 'focus' 'w8:p4'") || strings.Contains(out, "cd '") {
			t.Fatalf("unexpected focus command: %s", out)
		}
	})

	t.Run("provider fallback inherits current Herdr pane environment", func(t *testing.T) {
		env := newASMTestEnv(t)
		id := "fallback-session"
		cwd := t.TempDir()
		writeSession(t, filepath.Join(env.ProviderHome["codex"], "sessions", "2026", "08", "11", id+".jsonl"), id, cwd)
		agentFile := filepath.Join(t.TempDir(), "agent")
		extra := herdrEnv(fakeBinDir, `{"id":"cli:agent:list","result":{"type":"agent_list","agents":[]}}`)
		extra["ASM_FAKE_AGENT_FILE"] = agentFile
		out, err := env.RunBinaryWithEnv(t, asmBinary, extra, "resume", "--provider", "codex", "--since-days", "0", id)
		if err != nil {
			t.Fatalf("fallback resume: %v\n%s", err, out)
		}
		data, err := os.ReadFile(agentFile)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Program string            `json:"program"`
			Env     map[string]string `json:"env"`
		}
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if got.Program != executableName("codex") {
			t.Fatalf("program = %q", got.Program)
		}
		for key, want := range map[string]string{
			"HERDR_ENV":          "1",
			"HERDR_SOCKET_PATH":  "/fake/herdr.sock",
			"HERDR_SESSION":      "named-session",
			"HERDR_WORKSPACE_ID": "w-current",
			"HERDR_TAB_ID":       "w-current:t2",
			"HERDR_PANE_ID":      "w-current:p3",
		} {
			if got.Env[key] != want {
				t.Fatalf("%s = %q, want %q", key, got.Env[key], want)
			}
		}
	})

	t.Run("Herdr discovery failure is visible and does not hide sessions", func(t *testing.T) {
		env := newASMTestEnv(t)
		id := "error-session"
		cwd := t.TempDir()
		writeSession(t, filepath.Join(env.ProviderHome["codex"], "sessions", "2026", "08", "11", id+".jsonl"), id, cwd)
		extra := herdrEnv(fakeBinDir, "")
		extra["ASM_FAKE_HERDR_LIST_ERROR"] = "socket unavailable"
		out, err := env.RunBinaryWithEnv(t, asmBinary, extra, "--since-days", "0", "--json")
		if err != nil {
			t.Fatalf("asm --json: %v\n%s", err, out)
		}
		var payload struct {
			Sessions []struct {
				ID string `json:"id"`
			} `json:"sessions"`
			RuntimeErrors []struct {
				Runtime string `json:"runtime"`
				Error   string `json:"error"`
			} `json:"runtime_errors"`
		}
		if err := json.Unmarshal([]byte(out), &payload); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, out)
		}
		if len(payload.Sessions) != 1 || payload.Sessions[0].ID != id {
			t.Fatalf("sessions = %#v", payload.Sessions)
		}
		if len(payload.RuntimeErrors) != 1 || payload.RuntimeErrors[0].Runtime != "herdr" || !strings.Contains(payload.RuntimeErrors[0].Error, "socket unavailable") {
			t.Fatalf("runtime errors = %#v", payload.RuntimeErrors)
		}
	})

	t.Run("resume discovery failure warns and falls back to provider", func(t *testing.T) {
		env := newASMTestEnv(t)
		id := "list-fallback"
		cwd := t.TempDir()
		writeSession(t, filepath.Join(env.ProviderHome["codex"], "sessions", "2026", "08", "11", id+".jsonl"), id, cwd)
		agentFile := filepath.Join(t.TempDir(), "agent")
		extra := herdrEnv(fakeBinDir, "")
		extra["ASM_FAKE_HERDR_LIST_ERROR"] = "socket unavailable"
		extra["ASM_FAKE_AGENT_FILE"] = agentFile
		out, err := env.RunBinaryWithEnv(t, asmBinary, extra, "resume", "--provider", "codex", "--since-days", "0", id)
		if err != nil {
			t.Fatalf("fallback resume: %v\n%s", err, out)
		}
		if !strings.Contains(out, "warning: Herdr discovery failed; falling back to provider resume") {
			t.Fatalf("missing fallback warning: %s", out)
		}
		data, err := os.ReadFile(agentFile)
		if err != nil {
			t.Fatalf("provider was not started: %v", err)
		}
		var got struct {
			Env map[string]string `json:"env"`
		}
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if got.Env["HERDR_PANE_ID"] != "w-current:p3" {
			t.Fatalf("HERDR_PANE_ID = %q", got.Env["HERDR_PANE_ID"])
		}
	})

	t.Run("focus failure warns and falls back to provider", func(t *testing.T) {
		env := newASMTestEnv(t)
		id := "focus-fallback"
		cwd := t.TempDir()
		writeSession(t, filepath.Join(env.ProviderHome["codex"], "sessions", "2026", "08", "11", id+".jsonl"), id, cwd)
		agentFile := filepath.Join(t.TempDir(), "agent")
		extra := herdrEnv(fakeBinDir, agentListJSON("herdr:codex", "codex", id, "w9", "w9:t1", "w9:p2"))
		extra["ASM_FAKE_HERDR_FOCUS_ERROR"] = "pane disappeared"
		extra["ASM_FAKE_AGENT_FILE"] = agentFile
		out, err := env.RunBinaryWithEnv(t, asmBinary, extra, "resume", "--provider", "codex", "--since-days", "0", id)
		if err != nil {
			t.Fatalf("fallback resume: %v\n%s", err, out)
		}
		if !strings.Contains(out, "warning: Herdr focus failed; falling back to provider resume") {
			t.Fatalf("missing fallback warning: %s", out)
		}
		if _, err := os.Stat(agentFile); err != nil {
			t.Fatalf("provider was not started: %v", err)
		}
	})

	t.Run("multiple exact locations are reported without guessing", func(t *testing.T) {
		env := newASMTestEnv(t)
		id := "duplicate-session"
		cwd := t.TempDir()
		writeSession(t, filepath.Join(env.ProviderHome["codex"], "sessions", "2026", "08", "11", id+".jsonl"), id, cwd)
		agentFile := filepath.Join(t.TempDir(), "agent")
		list := agentListJSONWithAgents(
			agentJSON("herdr:codex", "codex", id, "w1", "w1:t1", "w1:p1"),
			agentJSON("herdr:codex", "codex", id, "w2", "w2:t1", "w2:p1"),
		)
		extra := herdrEnv(fakeBinDir, list)
		extra["ASM_FAKE_AGENT_FILE"] = agentFile
		out, err := env.RunBinaryWithEnv(t, asmBinary, extra, "resume", "--provider", "codex", "--since-days", "0", id)
		if err == nil {
			t.Fatalf("expected ambiguity error: %s", out)
		}
		for _, want := range []string{"multiple live Herdr locations", "w1:p1", "w2:p1"} {
			if !strings.Contains(out, want) {
				t.Fatalf("output missing %q: %s", want, out)
			}
		}
		if _, err := os.Stat(agentFile); !os.IsNotExist(err) {
			t.Fatalf("provider unexpectedly started: %v", err)
		}
	})

	t.Run("outside Herdr does not invoke integration", func(t *testing.T) {
		env := newASMTestEnv(t)
		id := "outside-session"
		cwd := t.TempDir()
		writeSession(t, filepath.Join(env.ProviderHome["codex"], "sessions", "2026", "08", "11", id+".jsonl"), id, cwd)
		callFile := filepath.Join(t.TempDir(), "herdr-calls")
		out, err := env.RunBinaryWithEnv(t, asmBinary, map[string]string{
			"PATH":                     fakeBinDir + string(os.PathListSeparator) + os.Getenv("PATH"),
			"ASM_FAKE_HERDR_CALL_FILE": callFile,
		}, "--since-days", "0", "--json")
		if err != nil {
			t.Fatalf("asm --json: %v\n%s", err, out)
		}
		if _, err := os.Stat(callFile); !os.IsNotExist(err) {
			t.Fatalf("Herdr unexpectedly invoked: %v", err)
		}
		if strings.Contains(out, "runtime_locations") || strings.Contains(out, "runtime_errors") {
			t.Fatalf("outside-Herdr JSON changed: %s", out)
		}
	})

	t.Run("unsupported identity source is not guessed", func(t *testing.T) {
		env := newASMTestEnv(t)
		id := "kiro-session"
		cwd := t.TempDir()
		writeKiroSession(t, env.ProviderHome["kiro"], id, cwd, "Kiro session")
		extra := herdrEnv(fakeBinDir, agentListJSON("herdr:kiro", "kiro", id, "w3", "w3:t1", "w3:p1"))
		out, err := env.RunBinaryWithEnv(t, asmBinary, extra, "--since-days", "0", "--json", "--query", id)
		if err != nil {
			t.Fatalf("asm --json: %v\n%s", err, out)
		}
		var payload struct {
			Sessions []struct {
				ID               string `json:"id"`
				RuntimeLocations []any  `json:"runtime_locations"`
			} `json:"sessions"`
		}
		if err := json.Unmarshal([]byte(out), &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Sessions) != 1 || payload.Sessions[0].ID != id || len(payload.Sessions[0].RuntimeLocations) != 0 {
			t.Fatalf("sessions = %#v", payload.Sessions)
		}
	})

	t.Run("report JSON does not query or expose transient Herdr state", func(t *testing.T) {
		env := newASMTestEnv(t)
		callFile := filepath.Join(t.TempDir(), "herdr-calls")
		extra := herdrEnv(fakeBinDir, `{"id":"cli:agent:list","result":{"type":"agent_list","agents":[]}}`)
		extra["ASM_FAKE_HERDR_CALL_FILE"] = callFile
		out, err := env.RunBinaryWithEnv(t, asmBinary, extra, "report", "--start", "2026-06-13", "--end", "2026-06-14")
		if err != nil {
			t.Fatalf("asm report: %v\n%s", err, out)
		}
		if _, err := os.Stat(callFile); !os.IsNotExist(err) {
			t.Fatalf("Herdr unexpectedly invoked by report: %v", err)
		}
		if strings.Contains(out, "runtime_locations") || strings.Contains(out, "runtime_errors") {
			t.Fatalf("report contains transient Herdr state: %s", out)
		}
	})
}

func buildHerdrFake(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, executableName("herdr-fake"))
	cmd := exec.Command("go", "build", "-o", binary, "./tests/testdata/herdrfake")
	cmd.Dir = ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake Herdr: %v\n%s", err, out)
	}
	return binary, dir
}

func installFakeCommand(t *testing.T, source, dir, name string) string {
	t.Helper()
	target := filepath.Join(dir, executableName(name))
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := in.Close(); err != nil {
			t.Errorf("close fake command source: %v", err)
		}
	}()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return target
}

func herdrEnv(fakeBinDir, listJSON string) map[string]string {
	return map[string]string{
		"PATH":                     fakeBinDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HERDR_ENV":                "1",
		"HERDR_SOCKET_PATH":        "/fake/herdr.sock",
		"HERDR_SESSION":            "named-session",
		"HERDR_WORKSPACE_ID":       "w-current",
		"HERDR_TAB_ID":             "w-current:t2",
		"HERDR_PANE_ID":            "w-current:p3",
		"ASM_FAKE_HERDR_LIST_JSON": listJSON,
	}
}

func agentListJSON(source, agent, id, workspaceID, tabID, paneID string) string {
	return agentListJSONWithAgents(agentJSON(source, agent, id, workspaceID, tabID, paneID))
}

func agentJSON(source, agent, id, workspaceID, tabID, paneID string) map[string]any {
	return map[string]any{
		"agent": agent, "agent_status": "working", "workspace_id": workspaceID,
		"tab_id": tabID, "pane_id": paneID,
		"agent_session": map[string]any{
			"source": source, "agent": agent, "kind": "id", "value": id,
		},
	}
}

func agentListJSONWithAgents(agents ...map[string]any) string {
	items := make([]any, 0, len(agents))
	for _, agent := range agents {
		items = append(items, agent)
	}
	payload := map[string]any{
		"id": "cli:agent:list",
		"result": map[string]any{
			"type":   "agent_list",
			"agents": items,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func executableName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
