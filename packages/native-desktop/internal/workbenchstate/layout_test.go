package workbenchstate

import (
	"path/filepath"
	"testing"
)

func TestNestedPaneSplitMoveCloseAndPersistence(t *testing.T) {
	var s State
	a := s.Open("h", "w", Tab{Kind: "file", Target: "a"})
	b := s.Open("h", "w", Tab{Kind: "file", Target: "b"})
	c := s.Open("h", "w", Tab{Kind: "file", Target: "c"})
	original := s.Layout("h", "w").Root.Pane.ID
	if !s.Split("h", "w", b, Horizontal) {
		t.Fatal("split failed")
	}
	l := s.Layout("h", "w")
	if l.Root.Axis != Horizontal || l.Root.First.Pane.ID != original || l.Root.Second.Pane.Active != b || l.FocusedPane != l.Root.Second.Pane.ID {
		t.Fatal("first split lost pane membership or focus")
	}
	second := l.FocusedPane
	if !s.Split("h", "w", c, Vertical) {
		t.Fatal("nested split failed")
	}
	l = s.Layout("h", "w")
	if l.Root.First.Axis != Vertical || l.Root.First.Second.Pane.Active != c {
		t.Fatal("split did not nest at source pane")
	}
	third := l.FocusedPane
	if !s.MoveTabToPane("h", "w", a, second) {
		t.Fatal("move failed")
	}
	l = s.Layout("h", "w")
	if l.Root.First.Pane == nil || l.Root.First.Pane.ID != third || l.Root.Second.Pane.ID != second || len(l.Root.Second.Pane.Tabs) != 2 {
		t.Fatal("empty source pane did not collapse")
	}
	if !s.FocusPane("h", "w", third) || l.Active != c {
		t.Fatal("focus did not select pane active tab")
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	l = loaded.Layout("h", "w")
	if l.FocusedPane != third || l.Root.Axis != Horizontal || l.Root.First.Pane.Active != c || l.Root.Second.Pane.Active != a {
		t.Fatal("round trip lost pane tree or active tabs")
	}
	if !loaded.Close("h", "w", c) || loaded.Layout("h", "w").Root.Pane.ID != second {
		t.Fatal("closing last tab did not collapse pane")
	}
	loaded.Close("h", "w", a)
	loaded.Close("h", "w", b)
	l = loaded.Layout("h", "w")
	if l.Root.Pane == nil || len(l.Root.Pane.Tabs) != 0 || l.FocusedPane != l.Root.Pane.ID || l.Active != "" {
		t.Fatal("last pane did not remain as launcher")
	}
}

func TestPaneOperationsStayWithinWorkspaceAndHost(t *testing.T) {
	var s State
	a := s.Open("h", "w", Tab{Kind: "file", Target: "a"})
	b := s.Open("h", "w", Tab{Kind: "file", Target: "b"})
	s.Open("other", "w", Tab{Kind: "file", Target: "b"})
	s.Open("h", "other", Tab{Kind: "file", Target: "b"})
	if !s.Split("h", "w", b, Horizontal) {
		t.Fatal("split failed")
	}
	l := s.Layout("h", "w")
	target := l.Root.First.Pane.ID
	if s.MoveTabToPane("other", "w", b, target) || s.FocusPane("h", "other", target) {
		t.Fatal("pane identity leaked across scopes")
	}
	if !s.MoveTabToPane("h", "w", b, target) {
		t.Fatal("move failed")
	}
	if l.Root.Pane == nil || len(l.Root.Pane.Tabs) != 2 || l.Root.Pane.Tabs[0] != a || l.Root.Pane.Tabs[1] != b {
		t.Fatal("move duplicated or lost tab")
	}
	if len(s.Layout("other", "w").Tabs) != 1 || len(s.Layout("h", "other").Tabs) != 1 {
		t.Fatal("other scope changed")
	}
}

func TestSplittingOnlyTabKeepsEmptyLauncherPane(t *testing.T) {
	var s State
	id := s.Open("h", "w", Tab{Kind: "file", Target: "one"})
	if !s.Split("h", "w", id, Vertical) {
		t.Fatal("single tab split failed")
	}
	l := s.Layout("h", "w")
	if l.Root.First.Pane == nil || len(l.Root.First.Pane.Tabs) != 0 || l.Root.Second.Pane.Active != id || len(l.Tabs) != 1 {
		t.Fatal("single tab split duplicated tab or lost launcher")
	}
	if !s.Close("h", "w", id) || l.Root.Pane == nil || len(l.Root.Pane.Tabs) != 0 {
		t.Fatal("closing split tab did not retain launcher")
	}
}

func TestLoadRepairsDuplicatePaneMembership(t *testing.T) {
	l := &WorkspaceLayout{Tabs: []Tab{{ID: "a", Kind: "file"}, {ID: "b", Kind: "file"}}, FocusedPane: "left", Root: &SplitNode{Axis: Horizontal, Ratio: .5, First: &SplitNode{Pane: &Pane{ID: "left", Tabs: []string{"a", "missing"}, Active: "missing"}}, Second: &SplitNode{Pane: &Pane{ID: "right", Tabs: []string{"a"}, Active: "a"}}}}
	s := State{Workspaces: map[string]*WorkspaceLayout{Scope("h", "w"): l}}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	restored, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got := restored.Layout("h", "w")
	if len(got.Root.First.Pane.Tabs) != 2 || got.Root.First.Pane.Tabs[0] != "a" || got.Root.First.Pane.Tabs[1] != "b" || len(got.Root.Second.Pane.Tabs) != 0 {
		t.Fatal("duplicate, unknown, or unassigned tabs were not repaired")
	}
	if got.Root.First.Pane.Active != "a" || got.Root.Second.Pane.Active != "" {
		t.Fatal("invalid pane active tab survived restore")
	}
}

func TestPaneLocalSelectionAndReordering(t *testing.T) {
	var s State
	a := s.Open("h", "w", Tab{Kind: "file", Target: "a"})
	b := s.Open("h", "w", Tab{Kind: "file", Target: "b"})
	c := s.Open("h", "w", Tab{Kind: "file", Target: "c"})
	s.Split("h", "w", c, Horizontal)
	l := s.Layout("h", "w")
	left := l.Root.First.Pane.ID
	right := l.Root.Second.Pane.ID
	s.FocusPane("h", "w", left)
	s.Move("h", "w", b, a)
	if l.Root.First.Pane.Tabs[0] != b || l.Root.First.Pane.Tabs[1] != a {
		t.Fatal("pane reorder lost ordering")
	}
	s.Open("h", "w", Tab{Kind: "file", Target: "a", Title: "updated"})
	s.FocusPane("h", "w", right)
	s.FocusPane("h", "w", left)
	if l.Active != a || l.Root.First.Pane.Active != a || l.Root.Second.Pane.Active != c {
		t.Fatal("pane focus lost its local active tab")
	}
	s.Move("h", "w", a, c)
	if l.Root.First.Pane.Tabs[0] != b || l.Root.Second.Pane.Tabs[0] != c {
		t.Fatal("reorder crossed pane boundaries")
	}
}
