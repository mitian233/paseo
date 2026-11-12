package workbench

import (
	"testing"

	"github.com/egoist/mygo/ui/editor"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/workbenchstate"
)

func unsavedPanels() map[string]*supportingPanels {
	return map[string]*supportingPanels{
		"host-a": {documents: map[string]*panelDocument{
			"a.txt": {state: editor.State{Buffer: editor.NewBuffer("changed")}, saved: "original"},
		}},
	}
}

// Cancelling the unsaved-changes prompt must abandon the jump that asked for
// the host switch, so the session does not open later behind the user's back.
func TestCancellingHostSwitchAbandonsPendingJump(t *testing.T) {
	a := &App{}
	a.desk.prefs = workbenchstate.State{ActiveHost: "host-a", Hosts: []workbenchstate.Host{{ID: "host-a"}, {ID: "host-b"}}}
	a.desk.supporting = unsavedPanels()
	a.desk.pendingOpen = pendingNavigation{host: "host-b", workspace: "w1"}
	a.selectHost("host-b")
	if !a.desk.confirmationOpen {
		t.Fatal("host switch did not ask about unsaved work")
	}
	cancel := a.desk.confirmationCancel
	if cancel == nil {
		t.Fatal("host switch prompt cannot cancel the pending jump")
	}
	cancel()
	if a.desk.pendingOpen.host != "" {
		t.Fatalf("cancelled jump survived: %+v", a.desk.pendingOpen)
	}
}

// Switching hosts by hand abandons a jump aimed at a different host.
func TestManualHostSwitchAbandonsPendingJumpForAnotherHost(t *testing.T) {
	a := &App{}
	a.desk.prefs = workbenchstate.State{ActiveHost: "host-a", Hosts: []workbenchstate.Host{{ID: "host-a"}}}
	a.desk.supporting = map[string]*supportingPanels{}
	a.desk.pendingOpen = pendingNavigation{host: "host-b", workspace: "w1"}
	a.forceSelectHost("host-a")
	if a.desk.pendingOpen.host != "" {
		t.Fatalf("stale jump survived a manual switch: %+v", a.desk.pendingOpen)
	}
}

// A jump aimed at the host being connected must survive the switch.
func TestHostSwitchKeepsJumpForTheTargetHost(t *testing.T) {
	a := &App{}
	a.desk.prefs = workbenchstate.State{ActiveHost: "host-a", Hosts: []workbenchstate.Host{{ID: "host-a"}}}
	a.desk.supporting = map[string]*supportingPanels{}
	a.desk.pendingOpen = pendingNavigation{host: "host-a", workspace: "w1"}
	a.forceSelectHost("host-a")
	if a.desk.pendingOpen.workspace != "w1" {
		t.Fatalf("jump to the connected host was dropped: %+v", a.desk.pendingOpen)
	}
}
