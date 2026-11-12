package workbench

import (
	"context"
	"github.com/egoist/mygo/ui/editor"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/workbenchstate"
	"testing"
)

func TestHostProjectActionWaitsForDirtyFileConfirmation(t *testing.T) {
	a := &App{connected: true}
	a.desk.prefs = workbenchstate.State{ActiveHost: "old"}
	p := newSupportingPanelsWithCaller(context.Background(), nil, "w", "/repo", nil)
	defer p.Close()
	d := &panelDocument{saved: "saved", state: editor.State{Buffer: editor.NewBuffer("edited")}}
	p.documents["a.txt"] = d
	a.desk.supporting = map[string]*supportingPanels{"w": p}
	a.requestHostHomeAction("selected", "project")
	if !a.desk.confirmationOpen || a.desk.confirmed == nil {
		t.Fatal("host switch bypassed unsaved file confirmation")
	}
	if a.desk.navigation.open || a.desk.homePending != "" || a.desk.prefs.ActiveHost != "old" {
		t.Fatal("project flow started on the previous host")
	}
}

func TestHostProjectActionUsesAlreadyConnectedHost(t *testing.T) {
	a := &App{connected: true}
	a.desk.prefs.ActiveHost = "selected"
	a.requestHostHomeAction("selected", "project")
	if !a.desk.navigation.open || a.desk.navigation.kind != "add project" || a.desk.confirmationOpen {
		t.Fatal("connected host did not open project form")
	}
}
