package workbench

import (
	"context"
	"github.com/egoist/mygo/ui"
	"testing"

	"github.com/getpaseo/paseo/packages/native-desktop/internal/workbenchstate"
)

func TestConversationOwnersIsolateHostDraftsAndReleaseSubscriptions(t *testing.T) {
	a := &App{}
	a.desk.prefs.ActiveHost = "first"
	a.desk.prefs.SetDraft("first", "agent", "first draft")
	first := a.conversationOwner("agent")
	if first.app.draft != "first draft" {
		t.Fatalf("first draft = %q", first.app.draft)
	}
	if a.conversationOwner("agent") != first {
		t.Fatal("same scope replaced its owner")
	}
	a.desk.prefs.ActiveHost = "second"
	a.desk.prefs.SetDraft("second", "agent", "second draft")
	second := a.conversationOwner("agent")
	if first == second || second.app.draft != "second draft" {
		t.Fatal("host conversation state crossed scopes")
	}
	ctx, cancel := context.WithCancel(context.Background())
	first.app.selectionCtx = ctx
	first.app.cancelSelection = cancel
	first.started = true
	first.app.draft = "edited first"
	a.pauseConversationOwners()
	if ctx.Err() == nil {
		t.Fatal("hidden owner retained subscription demand")
	}
	if got := a.desk.prefs.Draft("first", "agent"); got != "edited first" {
		t.Fatalf("paused draft = %q", got)
	}
	if second.app.draft != "second draft" {
		t.Fatal("pausing changed another host draft")
	}
	if a.desk.views[workbenchstate.Scope("first", "agent")].items != nil {
		t.Fatal("hidden conversation retained timeline views")
	}
}

func TestPaneDividerPersistsDraggedRatio(t *testing.T) {
	a := &App{}
	a.desk.prefs.ActiveHost = "host"
	a.desk.workspace = "workspace"
	id := a.desk.prefs.Open("host", "workspace", workbenchstate.Tab{Kind: "file", Target: "readme", Title: "Readme"})
	a.desk.prefs.Split("host", "workspace", id, workbenchstate.Horizontal)
	tt := ui.NewTester(func(c *ui.Context) { a.paneTree(c, a.activeLayout().Root) }, 1000, 500)
	tt.Frame()
	tt.Press(500, 200)
	tt.Move(650, 200)
	tt.Release(650, 200)
	tt.Frame()
	if ratio := a.activeLayout().Root.Ratio; ratio < .6 || ratio > .7 {
		t.Fatalf("dragged ratio=%v", ratio)
	}
}
