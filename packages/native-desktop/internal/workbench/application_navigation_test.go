package workbench

import (
	"github.com/egoist/mygo/ui"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/workbenchstate"
	"testing"
)

func TestCommandRankingMatchesEveryTermAndKeepsStableTies(t *testing.T) {
	entries := []commandEntry{{id: "a", title: "Project alpha", keywords: "repo"}, {id: "b", title: "Alpha", keywords: "repo"}, {id: "c", title: "Alpha", keywords: "else"}}
	got := rankCommandEntries(entries, "alpha repo")
	if len(got) != 2 || got[0].id != "a" || got[1].id != "b" {
		t.Fatalf("all-term stable match: %+v", got)
	}
	got = rankCommandEntries(entries, "alpha")
	if len(got) != 3 || got[0].id != "b" || got[1].id != "c" {
		t.Fatalf("prefix ordering: %+v", got)
	}
}
func TestDestinationSwitchPreservesWorkspaceAndDraft(t *testing.T) {
	a := &App{}
	a.desk.workspace = "workspace"
	a.desk.prefs = workbenchstate.State{ActiveHost: "host"}
	a.desk.prefs.SetDraft("host", "agent", "draft")
	a.showDestination("history")
	if a.desk.workspace != "workspace" || a.desk.prefs.Draft("host", "agent") != "draft" {
		t.Fatal("navigation discarded workspace state")
	}
}
func TestCommandCenterKeyboardExecutesSelectedResultAndEscapeCloses(t *testing.T) {
	a := &App{}
	a.openCommandCenter()
	a.desk.commandCenter.query = "settings"
	tt := ui.NewTester(func(c *ui.Context) { a.commandCenterDialog(c) }, 800, 600)
	if err := tt.Click("Search commands, workspaces and files"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyEnter)
	if a.desk.commandCenter.open || !a.desk.settingsOpen {
		t.Fatal("Enter did not execute settings")
	}
	a.openCommandCenter()
	tt.Frame()
	tt.Click("Search commands, workspaces and files")
	tt.Key(0, ui.KeyEscape)
	if a.desk.commandCenter.open {
		t.Fatal("Escape did not close overlay")
	}
}
