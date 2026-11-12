package workbench

import (
	"strings"
	"testing"

	"github.com/getpaseo/paseo/packages/native-desktop/internal/workbenchstate"
)

// The directory and the local agent list describe the same host. Listing both
// makes one session appear twice, under two different entry IDs.
func TestCommandCenterDoesNotDuplicateCurrentHostSessions(t *testing.T) {
	a := &App{}
	a.connected = true
	a.desk.destination = "workspace"
	a.desk.workspace = "w1"
	a.desk.prefs = workbenchstate.State{ActiveHost: "host-b"}
	a.agents = []Agent{{ID: "a1", Title: "One", WorkspaceID: "w1"}}
	a.desk.commandCenter.directory = []commandDirectory{
		{Host: "host-b", Workspaces: []Workspace{{ID: "w1", Name: "Local"}}, Agents: []Agent{{ID: "a1", Title: "One", WorkspaceID: "w1"}}},
	}
	entries := a.commandEntries()
	seen := map[string]int{}
	for _, entry := range entries {
		seen[entry.id]++
	}
	if seen["agent:host-b:a1"] != 1 {
		t.Fatalf("current-host session listed %d times: %+v", seen["agent:host-b:a1"], entries)
	}
	if seen["workspace:host-b:w1"] != 1 {
		t.Fatalf("current-host workspace listed %d times: %+v", seen["workspace:host-b:w1"], entries)
	}
}

// A remote workspace must be titled by its own name, and the host name stays
// searchable so the entry can still be found by host.
func TestCommandCenterTitlesRemoteWorkspaceByName(t *testing.T) {
	a := &App{}
	a.desk.prefs = workbenchstate.State{ActiveHost: "host-b"}
	a.desk.commandCenter.directory = []commandDirectory{
		{Host: "host-a", Name: "Build box", Workspaces: []Workspace{{ID: "w1", Name: "Feature branch"}}},
	}
	entries := a.commandEntries()
	var found bool
	for _, entry := range entries {
		if entry.id != "workspace:host-a:w1" {
			continue
		}
		found = true
		if entry.title != "Feature branch" {
			t.Fatalf("remote workspace title = %q", entry.title)
		}
	}
	if !found {
		t.Fatal("remote workspace missing from the command center")
	}
	byHost := rankCommandEntries(entries, "Build box")
	if len(byHost) != 1 || byHost[0].id != "workspace:host-a:w1" {
		t.Fatalf("host search did not find the workspace: %+v", byHost)
	}
}

// When the directory for the current host fails, the local workspace list is
// still usable and must stay reachable.
func TestCommandCenterKeepsLocalWorkspacesWhenDirectoryFails(t *testing.T) {
	a := &App{}
	a.connected = true
	a.desk.destination = "workspace"
	a.desk.workspace = "w1"
	a.desk.prefs = workbenchstate.State{ActiveHost: "host-b"}
	a.workspaces = []Workspace{{ID: "w1", Name: "Local"}}
	a.desk.commandCenter.directory = []commandDirectory{{Host: "host-b", Error: "offline"}}
	entries := a.commandEntries()
	var found bool
	for _, entry := range entries {
		if entry.id == "workspace:w1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("local workspace hidden after a directory failure: %+v", entries)
	}
}

// A padded query must rank prefix matches the same way a trimmed one does.
func TestCommandCenterPrefixRankingIgnoresPadding(t *testing.T) {
	entries := []commandEntry{{id: "a", title: "Alpha", keywords: "repo"}, {id: "b", title: "Beta alpha", keywords: "repo"}}
	got := rankCommandEntries(entries, "  alpha  ")
	if len(got) != 2 || got[0].id != "a" {
		t.Fatalf("prefix ranking with padded query: %+v", got)
	}
	if !strings.HasPrefix(strings.ToLower(got[0].title), "alpha") {
		t.Fatalf("unexpected first entry %q", got[0].title)
	}
}
