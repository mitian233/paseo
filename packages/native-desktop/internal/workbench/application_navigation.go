package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/getpaseo/paseo/packages/native-desktop/internal/client"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/workbenchstate"
)

type navigationSource struct {
	ID, Name string
	Client   caller
	Info     json.RawMessage
	Err      error
}
type navigationTarget struct {
	host       workbenchstate.Host
	credential credential
	current    *client.Client
}

// Sources snapshot UI-owned credentials before opening independent host connections.
// Temporary clients belong to the request, never to the active workspace.
func (a *App) navigationSources(ctx context.Context) ([]navigationSource, func()) {
	var targets []navigationTarget
	snapshot := func() {
		for _, h := range a.desk.prefs.Hosts {
			t := navigationTarget{host: h, credential: a.desk.credentials[h.ID]}
			if h.ID == a.desk.prefs.ActiveHost && a.connected {
				t.current = a.client
			}
			targets = append(targets, t)
		}
	}
	if a.win != nil {
		done := make(chan struct{})
		a.win.Update(func() { snapshot(); close(done) })
		select {
		case <-done:
		case <-ctx.Done():
			return nil, func() {}
		}
	} else {
		snapshot()
	}
	sources := make([]navigationSource, len(targets))
	clients := make([]*client.Client, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		sources[i] = navigationSource{ID: t.host.ID, Name: t.host.Name}
		if t.current != nil {
			sources[i].Client = t.current
			sources[i].Info = t.current.ServerInfo()
			continue
		}
		if t.host.HasSecret && t.credential.Password == "" && t.credential.LocalCredential == "" {
			sources[i].Err = errors.New("Connect to this host and enter its credentials first")
			continue
		}
		wg.Add(1)
		go func(i int, t navigationTarget) {
			defer wg.Done()
			c := &client.Client{}
			timeout, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if err := c.Connect(timeout, client.Target{URL: t.host.URL, Password: t.credential.Password, LocalCredential: t.credential.LocalCredential}); err != nil {
				sources[i].Err = err
				c.Close()
				return
			}
			clients[i] = c
			sources[i].Client = c
			sources[i].Info = c.ServerInfo()
		}(i, t)
	}
	wg.Wait()
	return sources, func() {
		for _, c := range clients {
			if c != nil {
				c.Close()
			}
		}
	}
}

func (a *App) showDestination(name string) {
	if a.desk.destination == name {
		return
	}
	switch a.desk.destination {
	case "history":
		a.closeHistory()
	case "schedules":
		a.closeSchedules()
	case "new-workspace":
		a.closeNewWorkspace()
	}
	a.pauseConversationOwners()
	a.desk.settingsOpen = false
	a.desk.destination = name
	a.desk.shell.NavOverlay = false
	a.desk.shell.NavHidden = false
}
func (a *App) toggleNavigation() {
	s := &a.desk.shell
	candidate := *s
	candidate.NavHidden = false
	if a.desk.windowWidth > 0 && !candidate.Layout(a.desk.windowWidth).NavVisible {
		s.NavOverlay = !s.NavOverlay
		return
	}
	s.NavHidden = !s.NavHidden
	a.persistSoon()
}
func (a *App) openCreatedWorkspace(hostID, workspaceID string) {
	a.openHistoryAgent(hostID, "", workspaceID)
}
func (a *App) openHistoryAgent(hostID, agentID, workspaceID string) {
	a.desk.pendingOpen = pendingNavigation{host: hostID, workspace: workspaceID, agent: agentID}
	if hostID != a.desk.prefs.ActiveHost || !a.connected {
		a.selectHost(hostID)
		return
	}
	a.completePendingNavigation()
}

type pendingNavigation struct{ host, workspace, agent, kind, target string }

func (a *App) completePendingNavigation() {
	p := a.desk.pendingOpen
	if p.host == "" || p.host != a.desk.prefs.ActiveHost || !a.connected {
		return
	}
	a.desk.pendingOpen = pendingNavigation{}
	if p.workspace != "" {
		a.selectWorkspace(p.workspace)
	}
	a.showDestination("workspace")
	if p.kind == "terminal" && p.target != "" {
		a.openContentTab("terminal", p.target, "Terminal")
		return
	}
	if p.agent != "" {
		for _, ag := range a.agents {
			if ag.ID == p.agent {
				a.openAgent(p.agent)
				return
			}
		}
		a.agents = append(a.agents, Agent{ID: p.agent, WorkspaceID: p.workspace, Title: "Session"})
		a.openAgent(p.agent)
	}
}

func (a *App) openCreatedWorkspaceTarget(hostID, workspaceID, kind, targetID string) {
	if kind == "agent" {
		a.openHistoryAgent(hostID, targetID, workspaceID)
		return
	}
	a.desk.pendingOpen = pendingNavigation{host: hostID, workspace: workspaceID, kind: kind, target: targetID}
	if hostID != a.desk.prefs.ActiveHost || !a.connected {
		a.selectHost(hostID)
		return
	}
	a.completePendingNavigation()
}
