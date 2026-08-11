package herdr

import (
	"testing"

	"github.com/hxy91819/agent-session-manager/internal/session"
)

func TestParseSnapshotMapsOnlyOfficialIDIdentities(t *testing.T) {
	data := []byte(`{"result":{"agents":[
{"agent":"codex","agent_status":"working","workspace_id":"w1","tab_id":"w1:t1","pane_id":"w1:p1","agent_session":{"source":"herdr:codex","agent":"codex","kind":"id","value":"sid"}},
{"agent":"kiro","agent_status":"idle","workspace_id":"w2","tab_id":"w2:t1","pane_id":"w2:p1","agent_session":{"source":"herdr:kiro","agent":"kiro","kind":"id","value":"sid"}},
{"agent":"claude","agent_status":"idle","workspace_id":"w3","tab_id":"w3:t1","pane_id":"w3:p1","agent_session":{"source":"herdr:claude","agent":"claude","kind":"path","value":"/session.jsonl"}},
{"agent":"codex","agent_status":"idle","workspace_id":"w1","tab_id":"w1:t1","pane_id":"w1:p1","agent_session":{"source":"herdr:codex","agent":"codex","kind":"id","value":"sid"}}
]}}`)

	snapshot, err := parseSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	locations := snapshot.Locations("codex", "sid")
	if len(locations) != 1 || locations[0].PaneID != "w1:p1" || locations[0].AgentStatus != "working" {
		t.Fatalf("locations = %#v", locations)
	}
	if got := snapshot.Locations("kiro", "sid"); len(got) != 0 {
		t.Fatalf("unsupported Kiro locations = %#v", got)
	}
}

func TestSnapshotDecorateReplacesOnlyHerdrLocations(t *testing.T) {
	snapshot := Snapshot{bindings: []binding{{
		provider:  "codex",
		sessionID: "sid",
		location: session.RuntimeLocation{
			Runtime: "herdr", WorkspaceID: "w2", TabID: "w2:t1", PaneID: "w2:p1",
		},
	}}}
	items := []session.Session{{
		ID: "sid", Provider: "codex",
		RuntimeLocations: []session.RuntimeLocation{
			{Runtime: "herdr", PaneID: "stale"},
			{Runtime: "other", PaneID: "other:p1"},
		},
	}}

	got := snapshot.Decorate(items)
	if len(got[0].RuntimeLocations) != 2 || got[0].RuntimeLocations[0].Runtime != "other" || got[0].RuntimeLocations[1].PaneID != "w2:p1" {
		t.Fatalf("runtime locations = %#v", got[0].RuntimeLocations)
	}
	if items[0].RuntimeLocations[0].PaneID != "stale" {
		t.Fatalf("input mutated: %#v", items[0].RuntimeLocations)
	}
}

func TestParseSnapshotRejectsMissingAgentsEnvelope(t *testing.T) {
	if _, err := parseSnapshot([]byte(`{"result":{"type":"agent_list"}}`)); err == nil {
		t.Fatal("expected missing agents error")
	}
}
