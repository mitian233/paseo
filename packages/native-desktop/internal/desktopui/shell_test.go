package desktopui

import "testing"

func TestLayoutYieldsNavigationWithoutChangingPreferredWidths(t *testing.T) {
	state := ShellState{NavWidth: 320, ExplorerWidth: 280, ExplorerOpen: true}
	wide := state.Layout(1100)
	if !wide.NavVisible || !wide.ExplorerVisible || wide.NavWidth != 320 || wide.ExplorerWidth != 280 {
		t.Fatalf("wide layout = %+v", wide)
	}
	narrow := state.Layout(760)
	if narrow.NavVisible || !narrow.ExplorerVisible || narrow.ExplorerWidth != 280 {
		t.Fatalf("narrow layout = %+v", narrow)
	}
	if state.NavWidth != 320 || state.ExplorerWidth != 280 {
		t.Fatalf("saved widths changed: %+v", state)
	}
	restored := state.Layout(1100)
	if !restored.NavVisible || restored.NavWidth != 320 {
		t.Fatalf("navigation not restored: %+v", restored)
	}
}

func TestLayoutShrinksNavigationBeforeYielding(t *testing.T) {
	state := ShellState{NavWidth: 320}
	layout := state.Layout(650)
	if !layout.NavVisible || layout.NavWidth != 250 {
		t.Fatalf("layout = %+v, want 250-wide navigation", layout)
	}
	if state.Layout(590).NavVisible {
		t.Fatal("navigation should yield below available minimum")
	}
}

func TestHiddenNavigationPreservesPreferredWidth(t *testing.T) {
	state := ShellState{NavWidth: 320, NavHidden: true}
	if state.Layout(1100).NavVisible {
		t.Fatal("closed menu still consumes navigation width")
	}
	state.NavHidden = false
	if got := state.Layout(1100); !got.NavVisible || got.NavWidth != 320 {
		t.Fatalf("reopened menu = %+v", got)
	}
}

func TestNarrowWidthHidesNavigationAndKeepsOverlayAvailable(t *testing.T) {
	state := ShellState{NavWidth: 320}
	layout := state.Layout(480)
	if layout.NavVisible {
		t.Fatalf("narrow layout keeps navigation visible: %+v", layout)
	}
	if layout.ExplorerVisible {
		t.Fatalf("narrow layout keeps explorer docked: %+v", layout)
	}
	wide := state.Layout(1100)
	if !wide.NavVisible || wide.NavWidth != 320 {
		t.Fatalf("preferred navigation width changed: %+v", wide)
	}
}
