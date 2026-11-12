package desktopui

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestSyntheticSidebarGroupCannotOfferProjectActions(t *testing.T) {
	state := ShellState{}
	var created string
	props := ShellProps{Width: 900, Projects: []Project{{ID: "status:running", Name: "Working", Group: true}}}
	actions := ShellActions{CreateWorkspace: func(id string) { created = id }}
	tt := ui.NewTester(func(c *ui.Context) { Shell(c, &state, props, actions, nil, nil) }, 900, 700)
	tt.Frame()
	if err := tt.RightClick("Working"); err == nil {
		if err = tt.ChooseMenuItem("New workspace"); err == nil {
			t.Fatal("synthetic group exposed project creation")
		}
	}
	if created != "" {
		t.Fatalf("created workspace from group %q", created)
	}
}
