package workbench

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/desktopui"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/timeline"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/workbenchstate"
)

type credential struct {
	Password        string
	LocalCredential string
}
type conversationSnapshot struct {
	draft, status string
	items         []timeline.Item
	list          ui.ListState
	markdown      map[string]*markdown.Options
	permissions   permissions
	expanded      map[string]bool
	turns         map[string]bool
	running       bool
}
type terminalEntry struct {
	panel  *terminalPanel
	cancel context.CancelFunc
	err    string
}
type desktopState struct {
	prefs                                          workbenchstate.State
	shell                                          desktopui.ShellState
	path, workspace, editingHost, notice           string
	views                                          map[string]conversationSnapshot
	credentials                                    map[string]credential
	supporting                                     map[string]*supportingPanels
	terminals                                      map[string]*terminalEntry
	timer                                          *time.Timer
	settingsOpen                                   bool
	confirmationOpen                               bool
	confirmationText                               string
	confirmed                                      func()
	confirmationCancel                             func()
	newAgentOpen, newAgentBusy                     bool
	newAgentProvider, newAgentTitle, newAgentError string
	forkAttachment                                 json.RawMessage
	forkTarget, newAgentPrompt, newWorkspaceTitle  string
	creationPayload                                json.RawMessage
	creationType                                   string
	providers                                      []string
	navigation                                     navigationState
	homeImport                                     homeImportState
	homeProviders                                  homeProvidersState
	homePairing                                    homePairingState
	homePending                                    string
	conversations                                  map[string]*conversationOwner
	frame                                          uint64
	loadFailed                                     bool
	destination                                    string
	pendingOpen                                    pendingNavigation
	history                                        historyState
	schedules                                      schedulesState
	newWorkspace                                   newWorkspaceState
	usage                                          usageState
	hostSettings                                   hostSettingsState
	footerHelp                                     footerHelpState
	commandCenter                                  commandCenterState
	windowWidth                                    float32
	sidebarDirectory                               sidebarDirectoryState
}

func (a *App) initializeDesktop(rawURL string) {
	a.desk.path = os.Getenv("PASEO_NATIVE_CONFIG")
	if a.desk.path == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			a.desk.notice = err.Error()
		} else {
			a.desk.path = filepath.Join(dir, "paseo-native", "settings.json")
		}
	}
	if a.desk.path != "" {
		s, err := workbenchstate.Load(a.desk.path)
		if err != nil {
			a.desk.notice = "Settings could not be loaded; original file preserved: " + err.Error()
			a.desk.loadFailed = true
		} else {
			a.desk.prefs = s
		}
	}
	a.desk.views = make(map[string]conversationSnapshot)
	a.desk.credentials = make(map[string]credential)
	a.desk.supporting = make(map[string]*supportingPanels)
	a.desk.terminals = make(map[string]*terminalEntry)
	if a.desk.prefs.ActiveWorkspace == nil {
		a.desk.prefs.ActiveWorkspace = make(map[string]string)
	}
	a.desk.shell = desktopui.ShellState{NavWidth: a.desk.prefs.NavWidth, ExplorerWidth: a.desk.prefs.ExplorerWidth, ExplorerOpen: a.desk.prefs.ExplorerOpen, ExpandedProjects: a.desk.prefs.ExpandedProjects}
	a.desk.shell.NavHidden = a.desk.prefs.NavHidden
	a.desk.shell.ProjectFilter = a.desk.prefs.SidebarProjectFilter
	if rawURL != "" {
		found := false
		for _, h := range a.desk.prefs.Hosts {
			if h.URL == rawURL {
				a.desk.prefs.ActiveHost = h.ID
				if h.HasSecret {
					a.readCredential(h.ID)
				}
				found = true
				break
			}
		}
		if !found {
			u, _ := url.Parse(rawURL)
			name := rawURL
			if u != nil && u.Host != "" {
				name = u.Host
			}
			h := workbenchstate.Host{ID: rand.Text(), Name: name, URL: rawURL}
			a.desk.prefs.Hosts = append(a.desk.prefs.Hosts, h)
			a.desk.prefs.ActiveHost = h.ID
		}
	} else {
		for _, h := range a.desk.prefs.Hosts {
			if h.ID == a.desk.prefs.ActiveHost {
				a.URL = h.URL
				if h.HasSecret {
					a.readCredential(h.ID)
				}
				break
			}
		}
	}
}
func (a *App) readCredential(id string) {
	data, err := mygo.Secrets.Service("paseo-native").Get(id)
	if err != nil {
		a.desk.notice = "Enter credentials to reconnect: " + err.Error()
		return
	}
	var cr credential
	if err = json.Unmarshal(data, &cr); err != nil {
		a.desk.notice = err.Error()
		return
	}
	a.desk.credentials[id] = cr
	a.Password = cr.Password
	a.Credential = cr.LocalCredential
}
func (a *App) persistDesktop() {
	if a.desk.timer != nil {
		a.desk.timer.Stop()
	}
	if a.desk.path == "" || a.desk.loadFailed {
		return
	}
	a.desk.prefs.NavWidth = a.desk.shell.NavWidth
	a.desk.prefs.ExplorerWidth = a.desk.shell.ExplorerWidth
	a.desk.prefs.ExplorerOpen = a.desk.shell.ExplorerOpen
	a.desk.prefs.SidebarProjectFilter = a.desk.shell.ProjectFilter
	a.desk.prefs.NavHidden = a.desk.shell.NavHidden
	a.desk.prefs.ExpandedProjects = a.desk.shell.ExpandedProjects
	if err := a.desk.prefs.Save(a.desk.path); err != nil {
		a.desk.notice = "Settings could not be saved: " + err.Error()
	}
}

func (a *App) persistAvailable() bool { return a.desk.path != "" && !a.desk.loadFailed }

func (a *App) settingsFailureMessage() string {
	if !a.desk.loadFailed {
		return ""
	}
	return "Saved settings could not be loaded. Current-session changes cannot be saved; the original file is preserved. Fix or move the settings file, then restart Paseo."
}
func (a *App) persistSoon() {
	if a.win == nil {
		return
	}
	if a.desk.timer != nil {
		a.desk.timer.Stop()
	}
	a.desk.timer = time.AfterFunc(300*time.Millisecond, func() { a.win.Update(func() { a.persistDesktop() }) })
}
func (a *App) captureConversation() {
	if a.selected == "" {
		return
	}
	if a.desk.views == nil {
		a.desk.views = make(map[string]conversationSnapshot)
	}
	key := workbenchstate.Scope(a.desk.prefs.ActiveHost, a.selected)
	a.desk.views[key] = conversationSnapshot{draft: a.draft, status: a.status, items: a.items, list: a.list, markdown: a.markdown, permissions: a.permissions, expanded: a.toolExpanded, turns: a.turnExpanded, running: a.agentRunning}
	a.desk.prefs.SetDraft(a.desk.prefs.ActiveHost, a.selected, a.draft)
}
func (a *App) restoreConversation(id string) {
	v := a.desk.views[workbenchstate.Scope(a.desk.prefs.ActiveHost, id)]
	a.draft = a.desk.prefs.Draft(a.desk.prefs.ActiveHost, id)
	a.items = v.items
	a.list = v.list
	a.markdown = v.markdown
	a.permissions = v.permissions
	a.toolExpanded = v.expanded
	a.turnExpanded = v.turns
	a.agentRunning = v.running
	a.conversationActionBusy = false
	if a.toolExpanded == nil {
		a.toolExpanded = make(map[string]bool)
	}
	if a.turnExpanded == nil {
		a.turnExpanded = make(map[string]bool)
	}
}
func (a *App) activeLayout() *workbenchstate.WorkspaceLayout {
	return a.desk.prefs.Layout(a.desk.prefs.ActiveHost, a.desk.workspace)
}
func (a *App) activeTab() (workbenchstate.Tab, bool) {
	l := a.activeLayout()
	for _, t := range l.Tabs {
		if t.ID == l.Active {
			return t, true
		}
	}
	return workbenchstate.Tab{}, false
}
func (a *App) workspaceCWD() string {
	for _, w := range a.workspaces {
		if w.ID == a.desk.workspace {
			if w.Directory != "" {
				return w.Directory
			}
			if w.Root != "" {
				return w.Root
			}
		}
	}
	for _, ag := range a.agents {
		if ag.WorkspaceID == a.desk.workspace {
			return ag.CWD
		}
	}
	return ""
}
func (a *App) restoreWorkspace() {
	wanted := a.desk.prefs.ActiveWorkspace[a.desk.prefs.ActiveHost]
	for _, w := range a.workspaces {
		if w.ID == wanted {
			a.selectWorkspace(wanted)
			return
		}
	}
}
func (a *App) releaseSelection() {
	a.captureConversation()
	if a.cancelSelection != nil {
		a.cancelSelection()
		a.cancelSelection = nil
	}
	a.selection++
	a.selected = ""
	a.selectionCtx = nil
	a.clearContent()
	a.draft = ""
	a.agentRunning = false
}
func (a *App) selectWorkspace(id string) {
	a.desk.destination = "workspace"
	a.desk.shell.NavHidden = false
	if id == a.desk.workspace {
		return
	}
	a.pauseConversationOwners()
	a.releaseSelection()
	a.desk.workspace = id
	a.desk.prefs.ActiveWorkspace[a.desk.prefs.ActiveHost] = id
	if t, ok := a.activeTab(); ok {
		a.activateTab(t.ID)
	}
	a.persistSoon()
}
func (a *App) openAgent(id string) {
	a.desk.destination = "workspace"
	for _, ag := range a.agents {
		if ag.ID == id {
			if ag.WorkspaceID != "" && ag.WorkspaceID != a.desk.workspace {
				a.selectWorkspace(ag.WorkspaceID)
			}
			title := ag.Title
			if title == "" {
				title = ag.Provider
			}
			tab := workbenchstate.Tab{Kind: "agent", Target: id, Title: title}
			key := a.desk.prefs.Open(a.desk.prefs.ActiveHost, a.desk.workspace, tab)
			a.activateTab(key)
			a.persistSoon()
			return
		}
	}
}
func (a *App) activateTab(id string) {
	layout := a.activeLayout()
	for _, tab := range layout.Tabs {
		if tab.ID != id {
			continue
		}
		a.desk.prefs.MoveTabToPane(a.desk.prefs.ActiveHost, a.desk.workspace, id, paneContaining(layout.Root, id))
		a.desk.settingsOpen = false
		if tab.Kind != "agent" && tab.Kind != "terminal" {
			if p := a.supportingPanels(); p != nil {
				p.Open(tab.Kind, tab.Target)
			}
		}
		a.persistSoon()
		return
	}
}
func (a *App) closeTab(id string) {
	for _, t := range a.activeLayout().Tabs {
		if t.ID != id {
			continue
		}
		if p := a.currentSupporting(); p != nil && !p.CanClose(t.Target) {
			a.confirm("Discard unsaved changes to "+t.Title+"?", func() { a.forceCloseTab(id) })
			return
		}
		a.forceCloseTab(id)
		return
	}
}
func (a *App) forceCloseTab(id string) {
	key := workbenchstate.Scope(a.desk.prefs.ActiveHost, workbenchstate.Scope(a.desk.workspace, id))
	if entry := a.desk.terminals[key]; entry != nil {
		entry.cancel()
		if entry.panel != nil {
			go entry.panel.Close()
		}
		delete(a.desk.terminals, key)
	}
	a.captureConversation()
	a.desk.prefs.Close(a.desk.prefs.ActiveHost, a.desk.workspace, id)
	if t, ok := a.activeTab(); ok {
		a.activateTab(t.ID)
	} else {
		a.releaseSelection()
	}
	a.persistSoon()
}
func (a *App) currentSupporting() *supportingPanels {
	return a.desk.supporting[workbenchstate.Scope(a.desk.prefs.ActiveHost, a.desk.workspace)]
}
func (a *App) supportingPanels() *supportingPanels {
	if a.client == nil || !a.connected || a.workspaceCWD() == "" {
		return nil
	}
	key := workbenchstate.Scope(a.desk.prefs.ActiveHost, a.desk.workspace)
	if p := a.desk.supporting[key]; p != nil {
		return p
	}
	gen, workspace := a.generation, a.desk.workspace
	p := newSupportingPanels(context.Background(), a.client, workspace, a.workspaceCWD(), func() { a.win.Update(func() {}) })
	p.onOpenFile = func(path string) {
		a.update(gen, func() {
			if a.desk.workspace == workspace {
				a.openContentTab("file", path, filepath.Base(path))
			}
		})
	}
	p.onOpenDiff = func(path string) {
		a.update(gen, func() {
			if a.desk.workspace == workspace {
				a.openContentTab("diff", path, filepath.Base(path))
			}
		})
	}
	a.desk.supporting[key] = p
	return p
}
func (a *App) openContentTab(kind, target, title string) {
	if !a.connected {
		return
	}
	id := a.desk.prefs.Open(a.desk.prefs.ActiveHost, a.desk.workspace, workbenchstate.Tab{Kind: kind, Target: target, Title: title})
	a.activateTab(id)
}
func (a *App) openPanel(kind string) {
	if !a.connected || a.desk.workspace == "" {
		return
	}
	if kind == "files" || kind == "changes" {
		a.activeLayout().ExplorerKind = kind
		a.desk.shell.ExplorerOpen = true
		if p := a.supportingPanels(); p != nil {
			p.Open(kind, "")
		}
		a.persistSoon()
		return
	}
	if kind == "terminal" {
		a.openContentTab(kind, "new:"+rand.Text(), "Terminal")
		return
	}
	a.openContentTab(kind, "", "Pull request")
}
func (a *App) closeWorkspacePanels() {
	a.closeConversationOwners()
	for key, p := range a.desk.supporting {
		p.Close()
		delete(a.desk.supporting, key)
	}
	for key, entry := range a.desk.terminals {
		entry.cancel()
		if entry.panel != nil {
			go entry.panel.Close()
		}
		delete(a.desk.terminals, key)
	}
	for key := range a.desk.views {
		delete(a.desk.views, key)
	}
	a.desk.workspace = ""
}
func (a *App) selectHost(id string) {
	if id == a.desk.prefs.ActiveHost && a.connected {
		return
	}
	for _, p := range a.desk.supporting {
		if p.HasUnsavedWork() {
			pending := a.desk.pendingOpen
			a.confirmWithCancel("Switch host and discard unsaved file changes?", func() { a.forceSelectHost(id) }, func() {
				if a.desk.pendingOpen == pending {
					a.desk.pendingOpen = pendingNavigation{}
				}
			})
			return
		}
	}
	a.forceSelectHost(id)
}
func (a *App) forceSelectHost(id string) {
	// A pending jump belongs to the host it targets. Switching to any other
	// host by hand abandons it instead of opening it later.
	if a.desk.pendingOpen.host != "" && a.desk.pendingOpen.host != id {
		a.desk.pendingOpen = pendingNavigation{}
	}
	a.captureConversation()
	a.stop()
	if id != a.desk.prefs.ActiveHost {
		a.desk.homeImport = homeImportState{}
	}
	a.desk.prefs.ActiveHost = id
	a.Password = ""
	a.Credential = ""
	for _, h := range a.desk.prefs.Hosts {
		if h.ID != id {
			continue
		}
		a.URL = h.URL
		if cr, ok := a.desk.credentials[id]; ok {
			a.Password = cr.Password
			a.Credential = cr.LocalCredential
		} else if h.HasSecret {
			a.readCredential(id)
			if a.Password == "" && a.Credential == "" {
				a.desk.editingHost = id
				a.desk.shell.HostEditor = desktopui.HostEditorState{Open: true, Name: h.Name, URL: h.URL, Error: a.desk.notice}
				return
			}
		}
		a.connect()
		a.persistSoon()
		return
	}
}
func (a *App) saveHost(name, address, password, local string) {
	if !a.persistAvailable() {
		a.desk.shell.HostEditor.Error = a.settingsFailureMessage()
		return
	}
	address = strings.TrimSpace(address)
	if !strings.Contains(address, "://") {
		address = "ws://" + address
	}
	u, err := url.Parse(address)
	if err != nil || u.Host == "" || (u.Scheme != "ws" && u.Scheme != "wss") {
		a.desk.shell.HostEditor.Error = "Enter a ws:// or wss:// daemon address."
		return
	}
	if password != "" && local != "" {
		a.desk.shell.HostEditor.Error = "Choose password or local credential, not both."
		return
	}
	if strings.TrimSpace(name) == "" {
		name = u.Host
	}
	id := a.desk.editingHost
	if id == "" {
		id = rand.Text()
	}
	cr := credential{password, local}
	a.desk.credentials[id] = cr
	hasSecret := password != "" || local != ""
	if hasSecret {
		data, _ := json.Marshal(cr)
		if err := mygo.Secrets.Service("paseo-native").Set(id, data); err != nil {
			a.desk.notice = "Credentials are available for this session only; enter them again after restart. " + err.Error()
		}
	}
	h := workbenchstate.Host{ID: id, Name: name, URL: address, HasSecret: hasSecret}
	found := false
	for i, old := range a.desk.prefs.Hosts {
		if old.ID == id {
			a.desk.prefs.Hosts[i] = h
			found = true
			break
		}
	}
	if !found {
		a.desk.prefs.Hosts = append(a.desk.prefs.Hosts, h)
	}
	a.desk.shell.HostEditor = desktopui.HostEditorState{}
	a.desk.editingHost = ""
	a.forceSelectHost(id)
	a.resumeNewWorkspacePicker()
}
func (a *App) confirm(text string, fn func()) {
	a.desk.confirmationText = text
	a.desk.confirmed = fn
	a.desk.confirmationCancel = nil
	a.desk.confirmationOpen = true
}
func (a *App) confirmWithCancel(text string, fn func(), cancel func()) {
	a.confirm(text, fn)
	a.desk.confirmationCancel = cancel
}
func (a *App) shellView(c *ui.Context) {
	a.ensureSidebarDirectory()
	a.ensureUsageSummary()
	width, _ := c.Size()
	a.desk.windowWidth = width
	props := desktopui.ShellProps{Theme: a.desk.prefs.Theme, SystemDark: c.Theme().Dark, Width: width, ActiveHost: a.desk.prefs.ActiveHost, ActiveWorkspace: a.desk.workspace, Status: a.status}
	props.Home = a.desk.destination != "workspace" || !a.connected || a.desk.workspace == ""
	props.ActiveDestination = a.desk.destination
	if a.desk.prefs.UsageInSidebar {
		props.UsageSummary = usageSummary(a.desk.usage.reports, a.desk.prefs.UsagePins, a.desk.prefs.UsageRemaining)
	}
	for _, item := range a.footerHelpItems() {
		props.HelpItems = append(props.HelpItems, desktopui.MenuAction{Label: item.Label, Run: item.Action})
	}
	for _, pref := range a.desk.prefs.SidebarNav {
		props.NavItems = append(props.NavItems, desktopui.NavPreference{ID: pref.ID, Hidden: pref.Hidden})
	}
	if a.desk.notice != "" {
		props.Status = a.desk.notice
	}
	for _, h := range a.desk.prefs.Hosts {
		status := ""
		if h.ID == props.ActiveHost {
			if a.connected {
				status = "Connected"
			} else if a.connecting {
				status = "Connecting"
			} else {
				status = "Offline"
			}
		}
		props.Hosts = append(props.Hosts, desktopui.Host{ID: h.ID, Name: h.Name, Status: status})
	}
	for _, w := range a.workspaces {
		if w.ID == a.desk.workspace {
			props.WorkspaceTitle = w.Name
			props.Branch = w.Git.CurrentBranch
			break
		}
	}
	for _, ag := range a.agents {
		props.Agents = append(props.Agents, desktopui.Agent{ID: ag.ID, WorkspaceID: ag.WorkspaceID, Title: ag.Title, Status: ag.Status})
	}
	layout := a.activeLayout()
	for _, t := range layout.Tabs {
		dirty := false
		if p := a.currentSupporting(); p != nil {
			dirty = !p.CanClose(t.Target)
		}
		props.Tabs = append(props.Tabs, desktopui.Tab{ID: t.ID, Title: t.Title, Kind: t.Kind, Active: t.ID == layout.Active, Dirty: dirty})
	}
	actions := desktopui.ShellActions{SelectHost: a.selectHost, AddHost: func() { a.desk.editingHost = ""; a.desk.shell.HostEditor = desktopui.HostEditorState{Open: true} }, SelectWorkspace: a.selectWorkspace, OpenAgent: a.openAgent, SelectTab: a.activateTab, CloseTab: a.closeTab, ReorderTab: func(id, before string) {
		a.desk.prefs.Move(a.desk.prefs.ActiveHost, a.desk.workspace, id, before)
		a.persistSoon()
	}, NewAgent: a.newAgent, OpenSettings: func() { a.desk.settingsOpen = true }, ToggleExplorer: func() { a.desk.shell.ExplorerOpen = !a.desk.shell.ExplorerOpen; a.persistSoon() }, OpenPanel: a.openPanel, ResizeNav: func(float32) { a.persistSoon() }, ResizeExplorer: func(float32) { a.persistSoon() }, SaveHost: a.saveHost, CancelHost: func() { a.desk.shell.HostEditor = desktopui.HostEditorState{} }}
	actions.CancelHost = func() {
		a.desk.shell.HostEditor = desktopui.HostEditorState{}
		a.desk.homePending = ""
	}
	props.HideTabStrip = true
	a.installNavigationActions(&actions)
	a.applySidebarDirectory(&props, &actions)
	actions.ToggleExplorer = func() {
		a.desk.shell.ExplorerOpen = !a.desk.shell.ExplorerOpen
		if a.desk.shell.ExplorerOpen {
			kind := layout.ExplorerKind
			if kind == "" {
				kind = "files"
			}
			if p := a.supportingPanels(); p != nil {
				p.Open(kind, "")
			}
		}
		a.persistSoon()
	}
	desktopui.Shell(c, &a.desk.shell, props, actions, a.activeContent, func(c *ui.Context) {
		kind := layout.ExplorerKind
		if kind == "" {
			kind = "files"
		}
		ui.Row(c).Gap(6).Children(func() {
			for _, k := range []string{"files", "changes"} {
				if ui.Button(c, k).Clicked() {
					layout.ExplorerKind = k
					if p := a.supportingPanels(); p != nil {
						p.Open(k, "")
					}
				}
			}
		})
		if p := a.supportingPanels(); p != nil {
			p.View(c, kind, "")
		} else {
			ui.Text(c, "Connect to a workspace to browse files.")
		}
	})
	a.dialogs(c)
	a.homeImportDialog(c)
	a.homeProvidersDialog(c)
	a.homePairingDialog(c)
	a.commandCenterDialog(c)
	a.usageDialog(c)
	a.footerHelpDialog(c)
	if a.selected != "" && a.desk.prefs.Draft(a.desk.prefs.ActiveHost, a.selected) != a.draft {
		a.desk.prefs.SetDraft(a.desk.prefs.ActiveHost, a.selected, a.draft)
		a.persistSoon()
	}
}
func (a *App) activeContent(c *ui.Context) {
	switch a.desk.destination {
	case "home":
		a.homeView(c)
		return
	case "history":
		a.historyView(c)
		return
	case "schedules":
		a.schedulesView(c)
		return
	case "new-workspace":
		a.newWorkspaceView(c)
		return
	case "host-settings":
		a.hostSettingsView(c)
		return
	}
	if a.desk.settingsOpen {
		a.settingsView(c)
		return
	}
	if !a.connected || a.desk.workspace == "" {
		a.homeView(c)
		return
	}
	a.desk.frame++
	a.paneTree(c, a.activeLayout().Root)
	for _, owner := range a.desk.conversations {
		if owner.started && owner.seen != a.desk.frame {
			a.pauseConversation(owner)
		}
	}
}
func (a *App) terminalView(c *ui.Context, t workbenchstate.Tab) {
	if !a.terminalRestoreSupported {
		ui.Text(c, "This host does not support terminal restoration. Update the daemon to use terminal panels.")
		return
	}
	key := workbenchstate.Scope(a.desk.prefs.ActiveHost, workbenchstate.Scope(a.desk.workspace, t.ID))
	entry := a.desk.terminals[key]
	if entry == nil {
		ctx, cancel := context.WithCancel(context.Background())
		entry = &terminalEntry{cancel: cancel}
		a.desk.terminals[key] = entry
		gen, workspace := a.generation, a.desk.workspace
		target := t.Target
		if strings.HasPrefix(target, "new:") {
			target = ""
		}
		daemon := a.client
		cwd := a.workspaceCWD()
		go func() {
			panel, err := newTerminalPanel(ctx, daemon, workspace, cwd, target, func() { a.win.Update(func() {}) })
			a.update(gen, func() {
				if ctx.Err() != nil {
					if panel != nil {
						go panel.Close()
					}
					return
				}
				entry.panel = panel
				if err != nil {
					entry.err = err.Error()
					return
				}
				l := a.desk.prefs.Layout(a.desk.prefs.ActiveHost, workspace)
				for i := range l.Tabs {
					if l.Tabs[i].ID == t.ID {
						l.Tabs[i].Target = panel.ID()
					}
				}
				a.persistSoon()
			})
		}()
	}
	if entry.err != "" {
		ui.Text(c, entry.err)
		if ui.Button(c, "Retry").Clicked() {
			entry.cancel()
			delete(a.desk.terminals, key)
		}
		return
	}
	if entry.panel == nil {
		ui.Text(c, "Attaching terminal…")
		return
	}
	entry.panel.View(c)
}
func (a *App) settingsView(c *ui.Context) {
	ui.Column(c).Fill().Padding(24).Gap(16).Children(func() {
		ui.Text(c, "Settings").Bold()
		if message := a.settingsFailureMessage(); message != "" {
			ui.Text(c, message).TextColor(c.Theme().Danger)
		}
		ui.Text(c, "Appearance")
		ui.Row(c).Gap(8).Children(func() {
			for _, mode := range []string{"system", "light", "dark"} {
				if ui.Button(c, mode).Disabled(!a.persistAvailable()).Clicked() {
					a.desk.prefs.Theme = mode
					a.persistSoon()
				}
			}
		})
		ui.Text(c, "Sidebar navigation")
		prefs := make([]desktopui.NavPreference, 0, len(a.desk.prefs.SidebarNav))
		for _, pref := range a.desk.prefs.SidebarNav {
			prefs = append(prefs, desktopui.NavPreference{ID: pref.ID, Hidden: pref.Hidden})
		}
		ordered := desktopui.OrderedNavigation(prefs)
		for i, pref := range ordered {
			ui.Row(c.Key(pref.ID)).Gap(8).Children(func() {
				visible := !pref.Hidden
				if ui.Checkbox(c, &visible, pref.ID).Changed() {
					ordered[i].Hidden = !visible
					a.saveSidebarNavigation(ordered)
				}
				if ui.Button(c, "Move up").Disabled(i == 0).Clicked() {
					ordered[i-1], ordered[i] = ordered[i], ordered[i-1]
					a.saveSidebarNavigation(ordered)
				}
				if ui.Button(c, "Move down").Disabled(i == len(ordered)-1).Clicked() {
					ordered[i+1], ordered[i] = ordered[i], ordered[i+1]
					a.saveSidebarNavigation(ordered)
				}
			})
		}
		ui.Text(c, "Hosts")
		for _, h := range a.desk.prefs.Hosts {
			h := h
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, h.Name).Grow(1)
				if ui.Button(c, "Edit").Disabled(!a.persistAvailable()).Clicked() {
					a.desk.editingHost = h.ID
					a.desk.shell.HostEditor = desktopui.HostEditorState{Open: true, Name: h.Name, URL: h.URL}
				}
			})
		}
		if ui.Button(c, "Back to workspace").Clicked() {
			a.desk.settingsOpen = false
		}
	})
}
func (a *App) stopTurn() {
	if !a.canMutate() {
		return
	}
	id, c, gen, sel, ctx := a.selected, a.client, a.generation, a.selection, a.selectionCtx
	go func() {
		request, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		payload, _ := json.Marshal(map[string]string{"agentId": id})
		raw, err := c.Call(request, "cancel_agent_request", payload)
		a.selectionUpdate(gen, sel, func() {
			if err != nil {
				a.status = "Stop request: " + err.Error()
				return
			}
			var ack struct {
				Error *string `json:"error"`
			}
			if json.Unmarshal(raw, &ack) != nil {
				a.status = "Invalid stop acknowledgement"
				return
			}
			if ack.Error != nil {
				a.status = *ack.Error
				return
			}
			a.agentRunning = false
			a.status = "Turn stopped"
		})
	}()
}
func (a *App) newAgent() {
	if !a.connected || a.workspaceCWD() == "" {
		a.status = "Select a connected workspace first."
		return
	}
	a.desk.newAgentOpen = true
	a.desk.newAgentError = ""
	gen, c := a.generation, a.client
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		raw, err := c.Call(ctx, "list_available_providers_request", json.RawMessage(`{}`))
		a.update(gen, func() {
			if err != nil {
				a.desk.newAgentError = err.Error()
				return
			}
			var response struct {
				Providers []struct {
					Provider  string `json:"provider"`
					Available bool   `json:"available"`
				} `json:"providers"`
			}
			if err := json.Unmarshal(raw, &response); err != nil {
				a.desk.newAgentError = err.Error()
				return
			}
			a.desk.providers = nil
			for _, p := range response.Providers {
				if p.Available {
					a.desk.providers = append(a.desk.providers, p.Provider)
				}
			}
			if len(a.desk.providers) > 0 {
				a.desk.newAgentProvider = a.desk.providers[0]
			}
		})
	}()
}
func (a *App) dialogs(c *ui.Context) {
	if a.desk.confirmationOpen {
		choice := ui.AlertDialog(c, &a.desk.confirmationOpen, "Unsaved changes", a.desk.confirmationText, "Cancel", "Discard changes")
		if choice == 0 {
			if cancel := a.desk.confirmationCancel; cancel != nil {
				a.desk.confirmationCancel = nil
				a.desk.confirmed = nil
				cancel()
			}
		}
		if choice == 1 && a.desk.confirmed != nil {
			fn := a.desk.confirmed
			a.desk.confirmed = nil
			a.desk.confirmationCancel = nil
			fn()
		}
	}
	a.creationDialog(c)
	a.navigationDialogs(c)
}
