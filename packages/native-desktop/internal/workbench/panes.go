package workbench

import (
	"github.com/egoist/mygo/ui"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/desktopui"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/workbenchstate"
)

type conversationOwner struct {
	app     *App
	started bool
	seen    uint64
}

func (a *App) conversationOwner(id string) *conversationOwner {
	if a.desk.conversations == nil {
		a.desk.conversations = make(map[string]*conversationOwner)
	}
	key := workbenchstate.Scope(a.desk.prefs.ActiveHost, id)
	if owner := a.desk.conversations[key]; owner != nil {
		return owner
	}
	if a.desk.views == nil {
		a.desk.views = make(map[string]conversationSnapshot)
	}
	child := &App{win: a.win, owner: a, client: a.client, connected: a.connected, generation: a.generation, selected: id, desk: a.desk}
	child.restoreConversation(id)
	if a.win != nil {
		child.diagrams = NewDiagrams(a.win.Update)
	}
	owner := &conversationOwner{app: child}
	a.desk.conversations[key] = owner
	return owner
}

func (a *App) pauseConversation(owner *conversationOwner) {
	child := owner.app
	child.captureConversation()
	key := workbenchstate.Scope(child.desk.prefs.ActiveHost, child.selected)
	snapshot := a.desk.views[key]
	snapshot.items = nil
	snapshot.markdown = nil
	a.desk.views[key] = snapshot
	if child.cancelSelection != nil {
		child.cancelSelection()
		child.cancelSelection = nil
	}
	child.selection++
	child.selectionCtx = nil
	child.observerReady = false
	child.items = nil
	child.markdown = nil
	if child.diagrams != nil {
		child.diagrams.Reset()
	}
	owner.started = false
}

func (a *App) pauseConversationOwners() {
	for _, owner := range a.desk.conversations {
		a.pauseConversation(owner)
	}
}

func (a *App) closeConversationOwners() {
	a.pauseConversationOwners()
	for key, owner := range a.desk.conversations {
		if owner.app.diagrams != nil {
			owner.app.diagrams.Close()
		}
		delete(a.desk.conversations, key)
	}
}

func (a *App) conversationPane(c *ui.Context, id string) {
	owner := a.conversationOwner(id)
	owner.seen = a.desk.frame
	child := owner.app
	if !owner.started {
		child.client = a.client
		child.connected = a.connected
		child.generation = a.generation
		child.desk.workspace = a.desk.workspace
		child.workspaces = a.workspaces
		child.agents = a.agents
		child.forkAvailable = a.forkAvailable
		child.selectAgent(id)
		owner.started = true
	}
	child.conversationView(c)
	if child.desk.prefs.Draft(child.desk.prefs.ActiveHost, id) != child.draft {
		child.desk.prefs.SetDraft(child.desk.prefs.ActiveHost, id, child.draft)
		a.persistSoon()
	}
}

func (a *App) paneActions() desktopui.ShellActions {
	return desktopui.ShellActions{
		SelectTab: a.activateTab, CloseTab: a.closeTab, NewAgent: a.newAgent, OpenPanel: a.openPanel,
		ReorderTab: func(id, before string) {
			a.desk.prefs.Move(a.desk.prefs.ActiveHost, a.desk.workspace, id, before)
			a.persistSoon()
		},
		SplitTab: func(id, axis string) {
			if a.desk.prefs.Split(a.desk.prefs.ActiveHost, a.desk.workspace, id, workbenchstate.Axis(axis)) {
				a.persistSoon()
			}
		},
		MoveTabToPane: func(id, pane string) {
			if a.desk.prefs.MoveTabToPane(a.desk.prefs.ActiveHost, a.desk.workspace, id, pane) {
				a.persistSoon()
			}
		},
	}
}

func (a *App) paneTree(c *ui.Context, node *workbenchstate.SplitNode) {
	if node == nil {
		return
	}
	if node.Pane == nil {
		percent := node.Ratio * 100
		first := func() { a.paneTree(c, node.First) }
		second := func() { a.paneTree(c, node.Second) }
		var split ui.Element
		if node.Axis == workbenchstate.Vertical {
			split = ui.SplitVerticalPercent(c, &percent, first, second)
		} else {
			split = ui.SplitPercent(c, &percent, first, second)
		}
		split.Fill()
		if split.Changed() {
			node.Ratio = percent / 100
			a.persistSoon()
		}
		return
	}
	pane := node.Pane
	actions := a.paneActions()
	props := desktopui.ShellProps{PaneID: pane.ID}
	layout := a.activeLayout()
	for _, id := range pane.Tabs {
		for _, tab := range layout.Tabs {
			if tab.ID == id {
				dirty := false
				if p := a.currentSupporting(); p != nil {
					dirty = !p.CanClose(tab.Target)
				}
				props.Tabs = append(props.Tabs, desktopui.Tab{ID: id, Title: tab.Title, Kind: tab.Kind, Active: id == pane.Active, Dirty: dirty})
				break
			}
		}
	}
	c = c.Key(pane.ID)
	ui.Column(c).Fill().Children(func() {
		desktopui.TabStrip(c, &a.desk.shell, props, actions)
		desktopui.PaneDropTarget(c, pane.ID, actions.MoveTabToPane, func(c *ui.Context) {
			if pane.Active == "" {
				ui.Column(c).Fill().Padding(24).Gap(8).Children(func() {
					ui.Text(c, "Open a conversation or workspace panel.")
					if ui.Button(c, "New agent").Clicked() {
						a.desk.prefs.FocusPane(a.desk.prefs.ActiveHost, a.desk.workspace, pane.ID)
						a.newAgent()
					}
					if ui.Button(c, "Open terminal").Clicked() {
						a.desk.prefs.FocusPane(a.desk.prefs.ActiveHost, a.desk.workspace, pane.ID)
						a.openPanel("terminal")
					}
				})
				return
			}
			for _, tab := range layout.Tabs {
				if tab.ID != pane.Active {
					continue
				}
				switch tab.Kind {
				case "agent":
					a.conversationPane(c, tab.Target)
				case "terminal":
					a.terminalView(c, tab)
				default:
					if p := a.supportingPanels(); p != nil {
						p.View(c, tab.Kind, tab.Target)
					} else {
						ui.Text(c, "Workspace directory is unavailable.")
					}
				}
				break
			}
		})
	})
}

func paneContaining(node *workbenchstate.SplitNode, id string) string {
	if node == nil {
		return ""
	}
	if node.Pane != nil {
		for _, tab := range node.Pane.Tabs {
			if tab == id {
				return node.Pane.ID
			}
		}
		return ""
	}
	if pane := paneContaining(node.First, id); pane != "" {
		return pane
	}
	return paneContaining(node.Second, id)
}
