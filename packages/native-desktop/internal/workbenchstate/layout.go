package workbenchstate

import "fmt"

// Axis describes the direction in which a split's children are arranged.
type Axis string

const (
	Horizontal Axis = "horizontal"
	Vertical   Axis = "vertical"
)

type Pane struct {
	ID     string   `json:"id"`
	Tabs   []string `json:"tabs,omitempty"`
	Active string   `json:"active,omitempty"`
}
type SplitNode struct {
	Pane   *Pane      `json:"pane,omitempty"`
	Axis   Axis       `json:"axis,omitempty"`
	Ratio  float32    `json:"ratio,omitempty"`
	First  *SplitNode `json:"first,omitempty"`
	Second *SplitNode `json:"second,omitempty"`
}

func (l *WorkspaceLayout) FindPane(id string) *Pane {
	var find func(*SplitNode) *Pane
	find = func(n *SplitNode) *Pane {
		if n == nil {
			return nil
		}
		if n.Pane != nil {
			if n.Pane.ID == id {
				return n.Pane
			}
			return nil
		}
		if p := find(n.First); p != nil {
			return p
		}
		return find(n.Second)
	}
	return find(l.Root)
}
func firstPane(n *SplitNode) *Pane {
	if n == nil {
		return nil
	}
	if n.Pane != nil {
		return n.Pane
	}
	if p := firstPane(n.First); p != nil {
		return p
	}
	return firstPane(n.Second)
}
func paneForTab(n *SplitNode, id string) *Pane {
	if n == nil {
		return nil
	}
	if n.Pane != nil {
		for _, tab := range n.Pane.Tabs {
			if tab == id {
				return n.Pane
			}
		}
		return nil
	}
	if p := paneForTab(n.First, id); p != nil {
		return p
	}
	return paneForTab(n.Second, id)
}
func hasTab(p *Pane, id string) bool {
	if p == nil || id == "" {
		return false
	}
	for _, v := range p.Tabs {
		if v == id {
			return true
		}
	}
	return false
}
func (l *WorkspaceLayout) nextPaneID(scope string) string {
	for i := 1; ; i++ {
		id := fmt.Sprintf("%s\x00pane-%d", scope, i)
		if l.FindPane(id) == nil {
			return id
		}
	}
}
func (l *WorkspaceLayout) ensure(scope string) {
	if l.Root == nil {
		p := &Pane{ID: l.nextPaneID(scope)}
		for _, tab := range l.Tabs {
			p.Tabs = append(p.Tabs, tab.ID)
		}
		p.Active = l.Active
		if !hasTab(p, p.Active) && len(p.Tabs) > 0 {
			p.Active = p.Tabs[0]
		}
		l.Root = &SplitNode{Pane: p}
		l.FocusedPane = p.ID
		l.Active = p.Active
		return
	}
	if p := l.FindPane(l.FocusedPane); p == nil {
		p = firstPane(l.Root)
		if p != nil {
			l.FocusedPane = p.ID
			l.Active = p.Active
		}
	} else if hasTab(p, l.Active) {
		p.Active = l.Active
	}
}
func (l *WorkspaceLayout) selectPane(p *Pane) { l.FocusedPane = p.ID; l.Active = p.Active }
func removePaneTab(p *Pane, id string) bool {
	for i, v := range p.Tabs {
		if v != id {
			continue
		}
		p.Tabs = append(p.Tabs[:i], p.Tabs[i+1:]...)
		if p.Active == id {
			p.Active = ""
			if len(p.Tabs) > 0 {
				if i >= len(p.Tabs) {
					i = len(p.Tabs) - 1
				}
				p.Active = p.Tabs[i]
			}
		}
		return true
	}
	return false
}
func collapsePane(n *SplitNode, id string) *SplitNode {
	if n == nil {
		return nil
	}
	if n.Pane != nil {
		if n.Pane.ID == id {
			return nil
		}
		return n
	}
	n.First = collapsePane(n.First, id)
	n.Second = collapsePane(n.Second, id)
	if n.First == nil {
		return n.Second
	}
	if n.Second == nil {
		return n.First
	}
	return n
}
func (l *WorkspaceLayout) collapseEmpty(p *Pane) {
	if len(p.Tabs) != 0 || l.Root.Pane != nil {
		return
	}
	l.Root = collapsePane(l.Root, p.ID)
	if l.FocusedPane == p.ID {
		l.selectPane(firstPane(l.Root))
	}
}

// Split moves a tab into a new pane adjacent to its current pane.
func (s *State) Split(host, workspace, tabID string, axis Axis) bool {
	if axis != Horizontal && axis != Vertical {
		return false
	}
	l := s.Layout(host, workspace)
	source := paneForTab(l.Root, tabID)
	if source == nil {
		return false
	}
	var split func(*SplitNode) bool
	split = func(n *SplitNode) bool {
		if n.Pane == source {
			removePaneTab(source, tabID)
			p := &Pane{ID: l.nextPaneID(Scope(host, workspace)), Tabs: []string{tabID}, Active: tabID}
			n.Pane = nil
			n.Axis = axis
			n.Ratio = .5
			n.First = &SplitNode{Pane: source}
			n.Second = &SplitNode{Pane: p}
			l.selectPane(p)
			return true
		}
		if n.Pane != nil {
			return false
		}
		return split(n.First) || split(n.Second)
	}
	return split(l.Root)
}
func (s *State) FocusPane(host, workspace, paneID string) bool {
	l := s.Layout(host, workspace)
	p := l.FindPane(paneID)
	if p == nil {
		return false
	}
	l.selectPane(p)
	return true
}
func (s *State) MoveTabToPane(host, workspace, tabID, paneID string) bool {
	l := s.Layout(host, workspace)
	source := paneForTab(l.Root, tabID)
	target := l.FindPane(paneID)
	if source == nil || target == nil {
		return false
	}
	if source != target {
		removePaneTab(source, tabID)
		target.Tabs = append(target.Tabs, tabID)
		l.collapseEmpty(source)
	}
	target.Active = tabID
	l.selectPane(target)
	return true
}

// normalizeTree repairs saved pane membership against the authoritative tab list.
func (l *WorkspaceLayout) normalizeTree(scope string) {
	if l.Root == nil {
		l.ensure(scope)
		return
	}
	valid := make(map[string]bool, len(l.Tabs))
	for _, tab := range l.Tabs {
		valid[tab.ID] = true
	}
	usedTabs := make(map[string]bool)
	usedPanes := make(map[string]bool)
	var clean func(*SplitNode) *SplitNode
	clean = func(n *SplitNode) *SplitNode {
		if n == nil {
			return nil
		}
		if n.Pane != nil {
			p := n.Pane
			if p.ID == "" || usedPanes[p.ID] {
				return nil
			}
			usedPanes[p.ID] = true
			tabs := p.Tabs[:0]
			for _, id := range p.Tabs {
				if valid[id] && !usedTabs[id] {
					tabs = append(tabs, id)
					usedTabs[id] = true
				}
			}
			p.Tabs = tabs
			if !hasTab(p, p.Active) {
				p.Active = ""
				if len(tabs) > 0 {
					p.Active = tabs[0]
				}
			}
			return &SplitNode{Pane: p}
		}
		a, b := clean(n.First), clean(n.Second)
		if a == nil {
			return b
		}
		if b == nil {
			return a
		}
		axis := n.Axis
		if axis != Horizontal && axis != Vertical {
			axis = Horizontal
		}
		ratio := n.Ratio
		if ratio <= 0 || ratio >= 1 {
			ratio = .5
		}
		return &SplitNode{Axis: axis, Ratio: ratio, First: a, Second: b}
	}
	l.Root = clean(l.Root)
	if l.Root == nil {
		l.ensure(scope)
		return
	}
	p := l.FindPane(l.FocusedPane)
	if p == nil {
		p = firstPane(l.Root)
	}
	for _, tab := range l.Tabs {
		if !usedTabs[tab.ID] {
			p.Tabs = append(p.Tabs, tab.ID)
			if p.Active == "" {
				p.Active = tab.ID
			}
		}
	}
	l.selectPane(p)
}
