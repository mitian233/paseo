package desktopui

import "testing"

func TestNavigationPreferencesDeduplicateAndRetainVisibility(t *testing.T) {
	got := OrderedNavigation([]NavPreference{{ID: "search", Hidden: true}, {ID: "history"}, {ID: "history", Hidden: true}, {ID: "removed"}})
	if len(got) != 4 || got[0].ID != "search" || !got[0].Hidden || got[1].ID != "history" || got[1].Hidden || got[2].ID != "new-workspace" || got[3].ID != "schedules" {
		t.Fatalf("preferences %+v", got)
	}
}
func TestPinnedWorkspaceOrderingDoesNotMutateInput(t *testing.T) {
	in := []Workspace{{ID: "a"}, {ID: "b", Pinned: true}, {ID: "c", Pinned: true}, {ID: "d"}}
	got := sidebarWorkspaces(in)
	if got[0].ID != "b" || got[1].ID != "c" || got[2].ID != "a" || got[3].ID != "d" || in[0].ID != "a" {
		t.Fatalf("ordering %+v input %+v", got, in)
	}
}
