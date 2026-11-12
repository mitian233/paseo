package workbenchstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Host contains connection metadata only. Secrets belong to the platform vault.
type Host struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	HasSecret bool   `json:"hasSecret,omitempty"`
}
type Tab struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Target string `json:"target,omitempty"`
	Title  string `json:"title"`
}
type WorkspaceLayout struct {
	Tabs         []Tab      `json:"tabs"`
	Active       string     `json:"active,omitempty"`
	Root         *SplitNode `json:"root,omitempty"`
	FocusedPane  string     `json:"focusedPane,omitempty"`
	ExplorerKind string     `json:"explorerKind,omitempty"`
}
type State struct {
	Hosts                   []Host                      `json:"hosts"`
	ActiveHost              string                      `json:"activeHost,omitempty"`
	ActiveWorkspace         map[string]string           `json:"activeWorkspace,omitempty"`
	Workspaces              map[string]*WorkspaceLayout `json:"workspaces,omitempty"`
	Drafts                  map[string]string           `json:"drafts,omitempty"`
	Theme                   string                      `json:"theme,omitempty"`
	NavWidth                float32                     `json:"navWidth,omitempty"`
	ExplorerWidth           float32                     `json:"explorerWidth,omitempty"`
	ExplorerOpen            bool                        `json:"explorerOpen,omitempty"`
	ExpandedProjects        map[string]bool             `json:"expandedProjects,omitempty"`
	UsageRemaining          bool                        `json:"usageRemaining,omitempty"`
	UsageInSidebar          bool                        `json:"usageInSidebar,omitempty"`
	UsagePins               map[string]bool             `json:"usagePins,omitempty"`
	UsageHost               string                      `json:"usageHost,omitempty"`
	NavHidden               bool                        `json:"navHidden,omitempty"`
	SidebarNav              []SidebarNavPreference      `json:"sidebarNav,omitempty"`
	SidebarGroup            string                      `json:"sidebarGroup,omitempty"`
	SidebarProjectFilter    string                      `json:"sidebarProjectFilter,omitempty"`
	NewWorkspacePreferences map[string]string           `json:"newWorkspacePreferences,omitempty"`
	SidebarDirectory        json.RawMessage             `json:"sidebarDirectory,omitempty"`
}

func Scope(host, target string) string { return host + "\x00" + target }

type SidebarNavPreference struct {
	ID     string `json:"id"`
	Hidden bool   `json:"hidden,omitempty"`
}

func (s *State) Layout(host, workspace string) *WorkspaceLayout {
	if s.Workspaces == nil {
		s.Workspaces = make(map[string]*WorkspaceLayout)
	}
	key := Scope(host, workspace)
	p := s.Workspaces[key]
	if p == nil {
		p = &WorkspaceLayout{}
		s.Workspaces[key] = p
	}
	p.ensure(key)
	return p
}
func (s *State) Open(host, workspace string, tab Tab) string {
	layout := s.Layout(host, workspace)
	if tab.ID == "" {
		tab.ID = Scope(tab.Kind, tab.Target)
	}
	for i := range layout.Tabs {
		if layout.Tabs[i].ID == tab.ID {
			layout.Tabs[i] = tab
			p := paneForTab(layout.Root, tab.ID)
			if p != nil {
				p.Active = tab.ID
				layout.selectPane(p)
			}
			return tab.ID
		}
	}
	layout.Tabs = append(layout.Tabs, tab)
	p := layout.FindPane(layout.FocusedPane)
	p.Tabs = append(p.Tabs, tab.ID)
	p.Active = tab.ID
	layout.selectPane(p)
	return tab.ID
}
func (s *State) Close(host, workspace, id string) bool {
	layout := s.Layout(host, workspace)
	for i, tab := range layout.Tabs {
		if tab.ID != id {
			continue
		}
		copy(layout.Tabs[i:], layout.Tabs[i+1:])
		layout.Tabs[len(layout.Tabs)-1] = Tab{}
		layout.Tabs = layout.Tabs[:len(layout.Tabs)-1]
		p := paneForTab(layout.Root, id)
		if p != nil {
			removePaneTab(p, id)
			layout.collapseEmpty(p)
			if layout.FocusedPane == p.ID {
				layout.Active = p.Active
			}
		}
		return true
	}
	return false
}
func (s *State) Move(host, workspace, id, before string) {
	if id == before {
		return
	}
	layout := s.Layout(host, workspace)
	from, to := -1, -1
	source := paneForTab(layout.Root, id)
	target := paneForTab(layout.Root, before)
	if source == nil || source != target {
		return
	}
	for i, tab := range layout.Tabs {
		if tab.ID == id {
			from = i
		}
		if tab.ID == before {
			to = i
		}
	}
	if from < 0 || to < 0 {
		return
	}
	tab := layout.Tabs[from]
	if from < to {
		to--
		copy(layout.Tabs[from:to], layout.Tabs[from+1:to+1])
	} else {
		copy(layout.Tabs[to+1:from+1], layout.Tabs[to:from])
	}
	layout.Tabs[to] = tab
	from, to = -1, -1
	for i, v := range source.Tabs {
		if v == id {
			from = i
		}
		if v == before {
			to = i
		}
	}
	if from >= 0 && to >= 0 {
		v := source.Tabs[from]
		if from < to {
			to--
			copy(source.Tabs[from:to], source.Tabs[from+1:to+1])
		} else {
			copy(source.Tabs[to+1:from+1], source.Tabs[to:from])
		}
		source.Tabs[to] = v
	}
}
func (s *State) SetDraft(host, agent, text string) {
	if s.Drafts == nil {
		s.Drafts = make(map[string]string)
	}
	key := Scope(host, agent)
	if text == "" {
		delete(s.Drafts, key)
	} else {
		s.Drafts[key] = text
	}
}
func (s *State) Draft(host, agent string) string { return s.Drafts[Scope(host, agent)] }
func (s *State) Save(path string) error {
	for key, layout := range s.Workspaces {
		if layout != nil {
			layout.ensure(key)
		}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".settings-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
func Load(path string) (State, error) {
	var s State
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(data, &s); err != nil {
		return State{}, fmt.Errorf("read saved workbench: %w", err)
	}
	for key, layout := range s.Workspaces {
		if layout == nil {
			continue
		}
		seen := make(map[string]bool)
		tabs := layout.Tabs[:0]
		for _, tab := range layout.Tabs {
			if tab.ID == "" || tab.Kind == "" || seen[tab.ID] {
				continue
			}
			seen[tab.ID] = true
			tabs = append(tabs, tab)
		}
		layout.Tabs = tabs
		if !seen[layout.Active] {
			layout.Active = ""
			if len(tabs) > 0 {
				layout.Active = tabs[0].ID
			}
		}
		layout.normalizeTree(key)
	}
	return s, nil
}
