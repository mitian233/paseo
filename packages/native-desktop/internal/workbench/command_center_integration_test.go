package workbench

import (
	"github.com/getpaseo/paseo/packages/native-desktop/internal/workbenchstate"
	"strings"
	"testing"
)

func TestCommandCenterListsHostQualifiedWorkspaces(t *testing.T) {
	a := &App{}
	a.desk.prefs = workbenchstate.State{ActiveHost: "host-b", Hosts: []workbenchstate.Host{{ID: "host-a"}, {ID: "host-b"}}}
	a.desk.commandCenter.directory = []commandDirectory{{Host: "host-a", Workspaces: []Workspace{{ID: "same-id", Name: "Remote"}}}, {Host: "host-b", Workspaces: []Workspace{{ID: "same-id", Name: "Local"}}}}
	a.desk.commandCenter.query = ""
	entries := a.commandEntries()
	seen := map[string]bool{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.id, "workspace:") {
			seen[entry.id] = true
		}
	}
	if !seen["workspace:host-a:same-id"] || !seen["workspace:host-b:same-id"] {
		t.Fatalf("host-scoped results missing: %+v", entries)
	}
}
