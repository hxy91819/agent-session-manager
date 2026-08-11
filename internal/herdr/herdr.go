package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/hxy91819/agent-session-manager/internal/launcher"
	"github.com/hxy91819/agent-session-manager/internal/session"
)

const (
	RuntimeName    = "herdr"
	commandTimeout = 2 * time.Second
)

var identitySources = map[string]string{
	"herdr:claude":   "claude",
	"herdr:codex":    "codex",
	"herdr:cursor":   "cursor",
	"herdr:kimi":     "kimi",
	"herdr:opencode": "opencode",
}

type Client struct {
	enabled bool
	binary  string
	timeout time.Duration
}

type Snapshot struct {
	bindings []binding
}

type binding struct {
	provider  string
	sessionID string
	location  session.RuntimeLocation
}

type agentListResponse struct {
	Result struct {
		Agents *[]agentInfo `json:"agents"`
	} `json:"result"`
}

type agentInfo struct {
	Agent        string            `json:"agent"`
	AgentStatus  string            `json:"agent_status"`
	WorkspaceID  string            `json:"workspace_id"`
	TabID        string            `json:"tab_id"`
	PaneID       string            `json:"pane_id"`
	AgentSession *agentSessionInfo `json:"agent_session"`
}

type agentSessionInfo struct {
	Source string `json:"source"`
	Agent  string `json:"agent"`
	Kind   string `json:"kind"`
	Value  string `json:"value"`
}

func NewFromEnv() Client {
	return Client{
		enabled: os.Getenv("HERDR_ENV") == "1",
		binary:  RuntimeName,
		timeout: commandTimeout,
	}
}

func (c Client) Enabled() bool {
	return c.enabled
}

func (c Client) Snapshot(ctx context.Context) (Snapshot, error) {
	if !c.enabled {
		return Snapshot{}, nil
	}
	timeout := c.timeout
	if timeout <= 0 {
		timeout = commandTimeout
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, c.binary, "agent", "list")
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
			return Snapshot{}, fmt.Errorf("herdr agent list timed out after %s", timeout)
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			message := strings.TrimSpace(string(exitErr.Stderr))
			if message != "" {
				return Snapshot{}, fmt.Errorf("herdr agent list failed: %s", message)
			}
		}
		return Snapshot{}, fmt.Errorf("herdr agent list failed: %w", err)
	}
	return parseSnapshot(out)
}

func (c Client) Focus(ctx context.Context, paneID string, printOnly bool) error {
	timeout := c.timeout
	if timeout <= 0 {
		timeout = commandTimeout
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return launcher.Run(commandCtx, session.ExecSpec{
		Args: []string{c.binary, "agent", "focus", paneID},
	}, printOnly)
}

func (s Snapshot) Decorate(items []session.Session) []session.Session {
	bySession := make(map[string][]session.RuntimeLocation)
	for _, item := range s.bindings {
		key := sessionKey(item.provider, item.sessionID)
		bySession[key] = append(bySession[key], item.location)
	}

	out := make([]session.Session, len(items))
	for i, item := range items {
		locations := make([]session.RuntimeLocation, 0, len(item.RuntimeLocations)+len(bySession[sessionKey(item.Provider, item.ID)]))
		for _, location := range item.RuntimeLocations {
			if location.Runtime != RuntimeName {
				locations = append(locations, location)
			}
		}
		locations = append(locations, bySession[sessionKey(item.Provider, item.ID)]...)
		if len(locations) == 0 {
			locations = nil
		}
		item.RuntimeLocations = locations
		out[i] = item
	}
	return out
}

func (s Snapshot) Locations(provider, sessionID string) []session.RuntimeLocation {
	var locations []session.RuntimeLocation
	for _, item := range s.bindings {
		if item.provider == provider && item.sessionID == sessionID {
			locations = append(locations, item.location)
		}
	}
	return locations
}

func parseSnapshot(data []byte) (Snapshot, error) {
	var response agentListResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return Snapshot{}, fmt.Errorf("parse herdr agent list: %w", err)
	}
	if response.Result.Agents == nil {
		return Snapshot{}, fmt.Errorf("parse herdr agent list: response missing result.agents")
	}

	bindings := make([]binding, 0, len(*response.Result.Agents))
	seen := make(map[string]struct{})
	for _, agent := range *response.Result.Agents {
		identity := agent.AgentSession
		if identity == nil || identity.Kind != "id" || identity.Value == "" {
			continue
		}
		provider, ok := identitySources[identity.Source]
		if !ok || identity.Agent != provider || agent.Agent != provider {
			continue
		}
		if agent.WorkspaceID == "" || agent.TabID == "" || agent.PaneID == "" {
			continue
		}
		key := sessionKey(provider, identity.Value) + "\x00" + agent.PaneID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		bindings = append(bindings, binding{
			provider:  provider,
			sessionID: identity.Value,
			location: session.RuntimeLocation{
				Runtime:     RuntimeName,
				WorkspaceID: agent.WorkspaceID,
				TabID:       agent.TabID,
				PaneID:      agent.PaneID,
				AgentStatus: agent.AgentStatus,
			},
		})
	}
	sort.Slice(bindings, func(i, j int) bool {
		left := bindings[i]
		right := bindings[j]
		if left.provider != right.provider {
			return left.provider < right.provider
		}
		if left.sessionID != right.sessionID {
			return left.sessionID < right.sessionID
		}
		return left.location.PaneID < right.location.PaneID
	})
	return Snapshot{bindings: bindings}, nil
}

func sessionKey(provider, sessionID string) string {
	return provider + "\x00" + sessionID
}
