package desktopui

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestWorkspaceContextMenuTargetsSelectedWorkspace(t *testing.T) {
	state := ShellState{NavWidth: 320}
	var renamed string
	props := ShellProps{Width: 1000, ActiveWorkspace: "w1", Workspaces: []Workspace{{ID: "w1", Project: "Project", Name: "Workspace one"}}}
	actions := ShellActions{RenameWorkspace: func(id string) { renamed = id }}
	tt := ui.NewTester(func(c *ui.Context) { Shell(c, &state, props, actions, nil, nil) }, 1000, 700)
	tt.Frame()
	if err := tt.RightClick("Workspace one"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Rename"); err != nil {
		t.Fatal(err)
	}
	if renamed != "w1" {
		t.Fatalf("renamed workspace = %q", renamed)
	}
}

func TestTabContextMenuSplitsSelectedTab(t *testing.T) {
	state := ShellState{}
	var tab, axis string
	props := ShellProps{Width: 1000, Tabs: []Tab{{ID: "a", Title: "Agent", Active: true}}}
	actions := ShellActions{SplitTab: func(id, direction string) { tab, axis = id, direction }}
	tt := ui.NewTester(func(c *ui.Context) { Shell(c, &state, props, actions, nil, nil) }, 1000, 700)
	tt.Frame()
	if err := tt.RightClick("Agent"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Split right"); err != nil {
		t.Fatal(err)
	}
	if tab != "a" || axis != "horizontal" {
		t.Fatalf("split = %q %q", tab, axis)
	}
}

func TestEmptyProjectCanCreateWorkspaceByStableID(t *testing.T) {
	state := ShellState{}
	var projectID string
	props := ShellProps{Width: 1000, Projects: []Project{{ID: "project-42", Name: "Empty project"}}}
	actions := ShellActions{CreateWorkspace: func(id string) { projectID = id }}
	tt := ui.NewTester(func(c *ui.Context) { Shell(c, &state, props, actions, nil, nil) }, 1000, 700)
	tt.Frame()
	if err := tt.RightClick("Empty project"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("New workspace"); err != nil {
		t.Fatal(err)
	}
	if projectID != "project-42" {
		t.Fatalf("project ID = %q", projectID)
	}
}

func TestPaneToolbarSplitsActiveTab(t *testing.T) {
	state := ShellState{}
	var selected, axis string
	props := ShellProps{PaneID: "pane", Tabs: []Tab{{ID: "agent", Title: "Agent", Active: true}}}
	actions := ShellActions{SplitTab: func(id, direction string) { selected, axis = id, direction }}
	tt := ui.NewTester(func(c *ui.Context) { TabStrip(c, &state, props, actions) }, 800, 500)
	if err := tt.Click("Pane actions"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Split right"); err != nil {
		t.Fatal(err)
	}
	if selected != "agent" || axis != "horizontal" {
		t.Fatalf("split=%q %q", selected, axis)
	}
}
