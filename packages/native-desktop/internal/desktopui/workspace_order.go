package desktopui

import "sort"

func sidebarWorkspaces(workspaces []Workspace) []Workspace {
	out := append([]Workspace(nil), workspaces...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Pinned && !out[j].Pinned })
	return out
}
