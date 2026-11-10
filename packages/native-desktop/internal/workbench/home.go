package workbench

import (
	"github.com/egoist/mygo/ui"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/desktopui"
)

func (a *App) homeView(c *ui.Context) {
	width, _ := c.Size()
	shell := a.desk.shell
	shell.ExplorerOpen = false
	layout := shell.Layout(width)
	if layout.NavVisible {
		width -= layout.NavWidth
	}
	props := desktopui.ShellProps{Theme: a.desk.prefs.Theme, SystemDark: c.Theme().Dark, Width: width}
	if !a.connected {
		props.Status = a.status
	}
	if a.desk.notice != "" {
		props.Status = a.desk.notice
	}
	actions := desktopui.HomeActions{
		Menu:           func() { a.desk.shell.NavHidden = !a.desk.shell.NavHidden },
		AddProject:     func() { a.requestHomeAction("project") },
		ImportSession:  func() { a.requestHomeAction("import") },
		SetupProviders: func() { a.requestHomeAction("providers") },
	}
	if !a.connected && !a.connecting && a.desk.prefs.ActiveHost != "" {
		actions.Reconnect = func() { a.forceSelectHost(a.desk.prefs.ActiveHost) }
	}
	if a.homePairingAvailable() {
		actions.PairDevice = a.openHomePairing
	}
	desktopui.Home(c, props, actions)
}

func (a *App) requestHomeAction(action string) {
	a.desk.homePending = action
	if a.connected {
		a.resumeHomeAction()
	} else if a.desk.prefs.ActiveHost == "" {
		a.desk.editingHost = ""
		a.desk.shell.HostEditor = desktopui.HostEditorState{Open: true}
	} else if !a.connecting {
		a.forceSelectHost(a.desk.prefs.ActiveHost)
	}
}

func (a *App) resumeHomeAction() {
	action := a.desk.homePending
	a.desk.homePending = ""
	switch action {
	case "project":
		a.navigationOpen("add project", "", "", "")
	case "import":
		a.openHomeImport()
	case "providers":
		a.openHomeProviders()
	}
}
