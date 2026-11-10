package workbenchstate

import (
	"path/filepath"
	"testing"
)

func TestTabsAndDraftsRemainScopedToHostAndWorkspace(t *testing.T) {
	var s State
	a := s.Open("host-a", "workspace", Tab{Kind: "agent", Target: "agent", Title: "First"})
	b := s.Open("host-b", "workspace", Tab{Kind: "agent", Target: "agent", Title: "Second"})
	s.SetDraft("host-a", "agent", "中文草稿")
	s.SetDraft("host-b", "agent", "other host")
	if s.Draft("host-a", "agent") != "中文草稿" || s.Draft("host-b", "agent") != "other host" {
		t.Fatal("drafts leaked across hosts")
	}
	if got := s.Open("host-a", "workspace", Tab{Kind: "agent", Target: "agent", Title: "Renamed"}); got != a || len(s.Layout("host-a", "workspace").Tabs) != 1 {
		t.Fatal("opening an existing agent duplicated its tab")
	}
	if !s.Close("host-a", "workspace", a) {
		t.Fatal("tab not closed")
	}
	if s.Layout("host-b", "workspace").Active != b || len(s.Layout("host-b", "workspace").Tabs) != 1 {
		t.Fatal("closing one host changed another host's layout")
	}
}

func TestClosingActiveTabSelectsAdjacentAndKeepsDraft(t *testing.T) {
	var s State
	a := s.Open("h", "w", Tab{Kind: "agent", Target: "a"})
	b := s.Open("h", "w", Tab{Kind: "agent", Target: "b"})
	c := s.Open("h", "w", Tab{Kind: "agent", Target: "c"})
	s.Layout("h", "w").Active = b
	s.SetDraft("h", "b", "unsent")
	if !s.Close("h", "w", b) || s.Layout("h", "w").Active != c {
		t.Fatal("closing active middle tab did not select its next neighbor")
	}
	if s.Draft("h", "b") != "unsent" {
		t.Fatal("closing tab discarded unsent draft")
	}
	s.Move("h", "w", c, a)
	tabs := s.Layout("h", "w").Tabs
	if len(tabs) != 2 || tabs[0].ID != c || tabs[1].ID != a {
		t.Fatal("tab reorder did not preserve target order")
	}
}

func TestLayoutRoundTripRestoresWorkspaceAndDraft(t *testing.T) {
	var s State
	s.Hosts = []Host{{ID: "h", Name: "Machine", URL: "ws://127.0.0.1:1/ws", HasSecret: true}}
	s.ActiveHost = "h"
	s.Open("h", "w", Tab{Kind: "file", Target: "/repo/main.go", Title: "main.go"})
	s.SetDraft("h", "a", "尚未发送")
	s.Theme = "dark"
	path := filepath.Join(t.TempDir(), "nested", "settings.json")
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	restored, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ActiveHost != "h" || restored.Theme != "dark" || restored.Draft("h", "a") != "尚未发送" || restored.Layout("h", "w").Tabs[0].Target != "/repo/main.go" {
		t.Fatal("restored settings lost active layout or draft")
	}
}

func TestMovingTabBeforeLaterTarget(t *testing.T) {
	var s State
	a := s.Open("h", "w", Tab{Kind: "agent", Target: "a"})
	b := s.Open("h", "w", Tab{Kind: "agent", Target: "b"})
	c := s.Open("h", "w", Tab{Kind: "agent", Target: "c"})
	s.Move("h", "w", a, c)
	tabs := s.Layout("h", "w").Tabs
	if tabs[0].ID != b || tabs[1].ID != a || tabs[2].ID != c {
		t.Fatal("forward drag placed tab after its target")
	}
}
