package desktopui

import "github.com/egoist/mygo/ui"

// Shell composes the navigation, workspace and the independent Explorer dock.
func Shell(c *ui.Context, state *ShellState, props ShellProps, actions ShellActions, content func(*ui.Context), explorer func(*ui.Context)) {
	theme, colors := Theme(props.Theme, props.SystemDark)
	c.SetTheme(theme)
	layoutState := *state
	if props.Home {
		layoutState.ExplorerOpen = false
	}
	layout := layoutState.Layout(props.Width)
	ui.Row(c).Fill().AlignItems(ui.Stretch).Background(colors.Background).Children(func() {
		if layout.NavVisible {
			navWidth := layout.NavWidth
			if props.Home {
				navWidth--
			}
			ui.Column(c).Width(navWidth).Shrink(0).Background(colors.Sidebar).Children(func() {
				sidebar(c, state, props, actions, colors)
			})
			handle := resizeHandle(c, colors, func(dx float32) {
				state.NavWidth = max(200, layout.NavWidth+dx)
				if actions.ResizeNav != nil {
					actions.ResizeNav(state.NavWidth)
				}
			})
			if props.Home {
				handle.Width(1)
			}
		}
		ui.Column(c).Grow(1).Shrink(1).Children(func() {
			if !props.Home {
				workspaceHeader(c, props, actions, colors)
				if !props.HideTabStrip {
					TabStrip(c, state, props, actions)
				}
			}
			ui.Column(c).Grow(1).Shrink(1).Children(func() {
				if content != nil {
					content(c)
				}
			})
		})
		if layout.ExplorerVisible {
			resizeHandle(c, colors, func(dx float32) {
				state.ExplorerWidth = max(240, layout.ExplorerWidth-dx)
				if actions.ResizeExplorer != nil {
					actions.ResizeExplorer(state.ExplorerWidth)
				}
			})
			ui.Column(c).Width(layout.ExplorerWidth).Shrink(0).Background(colors.Surface).Children(func() {
				if explorer != nil {
					explorer(c)
				}
			})
		}
	})
	if !props.Home && state.ExplorerOpen && props.Width < 600 && explorer != nil {
		open := true
		ui.Modal(c, &open, func() { ui.Column(c).Width(max(240, props.Width-48)).Height(500).Children(func() { explorer(c) }) })
		if !open && actions.ToggleExplorer != nil {
			actions.ToggleExplorer()
		}
	}
	hostEditor(c, &state.HostEditor, actions, colors)
}

func resizeHandle(c *ui.Context, colors Tokens, resize func(float32)) ui.Element {
	h := ui.Box(c).Width(5).Shrink(0).Background(colors.Border).Cursor(ui.CursorResizeEW)
	if dx, _, held := h.Dragged(); held && dx != 0 {
		resize(dx)
	}
	return h
}

func sidebar(c *ui.Context, state *ShellState, p ShellProps, a ShellActions, colors Tokens) {
	// 36px chrome row — matches TitlebarDragRegion height in the RN desktop sidebar.
	// On Linux/Windows this is a transparent drag affordance; macOS gets traffic lights.
	// It intentionally contains no interactive widgets so the area can be dragged.
	ui.Row(c).Height(36).Shrink(0)

	// Nav rows section — mirrors SidebarNavRows in the RN app:
	// paddingTop 8, gap 2, paddingBottom 6, horizontal 8, then a bottom border.
	type navItem struct {
		key, label string
		icon       *ui.SVG
		action     func()
	}
	navItems := [...]navItem{
		{"nav:new-workspace", "New workspace", plusIcon, a.NewWorkspace},
		{"nav:history", "History", historyIcon, a.OpenHistory},
		{"nav:search", "Search", searchIcon, a.OpenSearch},
		{"nav:schedules", "Schedules", schedulesIcon, a.OpenSchedules},
	}
	hasNavRows := false
	for _, it := range navItems {
		if it.action != nil {
			hasNavRows = true
			break
		}
	}
	if hasNavRows {
		ui.Column(c).Shrink(0).Padding(8, 8, 6, 8).Gap(2).BorderWidth(0, 0, 1, 0).BorderColor(colors.Border).Children(func() {
			for _, it := range navItems {
				if it.action == nil {
					continue
				}
				action := it.action
				navRow(c.Key(it.key), it.label, it.icon, action, colors)
			}
		})
	}

	// Projects scroll — existing list logic unchanged.
	ui.Scroll(c).Grow(1).Children(func() {
		projects := p.Projects
		if len(projects) == 0 {
			for _, w := range p.Workspaces {
				id := w.ProjectID
				if id == "" {
					id = w.Project
				}
				found := false
				for _, project := range projects {
					if project.ID == id {
						found = true
						break
					}
				}
				if !found {
					projects = append(projects, Project{ID: id, Name: w.Project})
				}
			}
		}
		for _, item := range projects {
			project := item.ID
			if state.ExpandedProjects == nil {
				state.ExpandedProjects = make(map[string]bool)
			}
			expanded, exists := state.ExpandedProjects[project]
			if !exists {
				expanded = true
			}
			arrow := "▾"
			if !expanded {
				arrow = "▸"
			}
			projectRow := row(c.Key("project:"+project), item.Name, arrow, 12, false, colors)
			if a.ProjectSettings != nil || a.OpenProjectFolder != nil || a.OpenProjectInNewWindow != nil || a.RemoveProject != nil || a.RenameProject != nil || a.CreateWorkspace != nil {
				projectRow.ContextMenu(func(m *ui.Menu) {
					if a.CreateWorkspace != nil && m.Item("New workspace").Chosen() {
						a.CreateWorkspace(project)
					}
					if a.RenameProject != nil && m.Item("Rename project").Chosen() {
						a.RenameProject(project)
					}
					if a.ProjectSettings != nil && m.Item("Project settings").Chosen() {
						a.ProjectSettings(project)
					}
					if a.OpenProjectInNewWindow != nil && m.Item("Open in new window").Chosen() {
						a.OpenProjectInNewWindow(project)
					}
					if a.OpenProjectFolder != nil && m.Item("Open folder").Chosen() {
						a.OpenProjectFolder(project)
					}
					if a.RemoveProject != nil && m.Item("Remove project").Chosen() {
						a.RemoveProject(project)
					}
				})
			}
			if projectRow.Clicked() {
				state.ExpandedProjects[project] = !expanded
			}
			if !expanded {
				continue
			}
			for _, workspace := range p.Workspaces {
				workspaceProject := workspace.ProjectID
				if workspaceProject == "" {
					workspaceProject = workspace.Project
				}
				if workspaceProject != project {
					continue
				}
				w := workspace
				workspaceRow := row(c.Key("workspace:"+w.ID), w.Name, "", 32, w.ID == p.ActiveWorkspace, colors)
				if a.CopyWorkspacePath != nil || a.CopyWorkspaceBranch != nil || a.RenameWorkspace != nil || a.MarkWorkspaceRead != nil || a.MarkWorkspaceUnread != nil || a.ToggleWorkspacePin != nil || a.OpenWorkspaceFolder != nil || a.ArchiveWorkspace != nil {
					workspaceRow.ContextMenu(func(m *ui.Menu) {
						if a.CopyWorkspacePath != nil && m.Item("Copy path").Chosen() {
							a.CopyWorkspacePath(w.ID)
						}
						if a.CopyWorkspaceBranch != nil && m.Item("Copy branch name").Chosen() {
							a.CopyWorkspaceBranch(w.ID)
						}
						if a.RenameWorkspace != nil && m.Item("Rename").Chosen() {
							a.RenameWorkspace(w.ID)
						}
						if a.MarkWorkspaceRead != nil && m.Item("Mark as read").Chosen() {
							a.MarkWorkspaceRead(w.ID)
						}
						if a.MarkWorkspaceUnread != nil && m.Item("Mark as unread").Chosen() {
							a.MarkWorkspaceUnread(w.ID)
						}
						if a.ToggleWorkspacePin != nil && m.Item("Toggle pin").Chosen() {
							a.ToggleWorkspacePin(w.ID)
						}
						if a.OpenWorkspaceFolder != nil && m.Item("Open folder").Chosen() {
							a.OpenWorkspaceFolder(w.ID)
						}
						if a.ArchiveWorkspace != nil && m.Item("Archive").Chosen() {
							a.ArchiveWorkspace(w.ID)
						}
					})
				}
				if workspaceRow.Clicked() && a.SelectWorkspace != nil {
					a.SelectWorkspace(w.ID)
				}
				if w.ID == p.ActiveWorkspace {
					for _, agent := range p.Agents {
						if agent.WorkspaceID != w.ID {
							continue
						}
						ag := agent
						label := ag.Title
						if ag.Status != "" {
							label += " · " + ag.Status
						}
						if row(c.Key("agent:"+ag.ID), label, "", 48, false, colors).Clicked() && a.OpenAgent != nil {
							a.OpenAgent(ag.ID)
						}
					}
				}
			}
		}
		if len(projects) == 0 {
			ui.Column(c).Padding(12, 16).Gap(4).Children(func() {
				ui.Text(c, "No projects yet").FontSize(14).TextColor(colors.Muted)
				ui.Text(c, "Add a project to get started").FontSize(13).TextColor(colors.Muted)
			})
		}
	})

	// Footer icon row — mirrors SidebarFooter's bottom icon line.
	// FolderPlus (add project) on the left, host picker + help + settings on the right.
	ui.Row(c).Shrink(0).BorderWidth(1, 0, 0, 0).BorderColor(colors.Border).Padding(6, 8).AlignItems(ui.Center).Children(func() {
		if a.AddProject != nil && iconButton(c.Key("sidebar-add-project"), "Add project", folderPlusIcon, colors).Clicked() {
			a.AddProject()
		}
		ui.Spacer(c)
		iconButton(c.Key("sidebar-hosts"), "Hosts", serverIcon, colors).PopoverMenuButton(func(m *ui.Menu) {
			for _, host := range p.Hosts {
				h := host
				if m.Item(h.Name).Chosen() && a.SelectHost != nil {
					a.SelectHost(h.ID)
				}
			}
			if a.AddHost != nil && m.Item("Add host").Chosen() {
				a.AddHost()
			}
		})
		iconButton(c.Key("sidebar-help"), "Help", helpIcon, colors)
		if a.OpenSettings != nil && iconButton(c.Key("sidebar-settings"), "Settings", settingsIcon, colors).Clicked() {
			a.OpenSettings()
		}
	})
}

func row(c *ui.Context, text, icon string, indent float32, selected bool, colors Tokens) ui.Element {
	b := ui.ButtonBase(c).Height(32).Padding(0, 12, 0, indent).Gap(8).Radius(6).Justify(ui.Start).TextColor(colors.Foreground)
	if selected || b.Hovered() {
		b = b.Background(colors.Selected)
	}
	b.Children(func() {
		if icon != "" {
			ui.Text(c, icon).TextColor(colors.Muted)
		}
		ui.Text(c, text).FontSize(14)
	})
	return b
}

// navRow renders a compact nav row matching SidebarHeaderRow variant="compact" in the RN app:
// 28px min-height, 16px horizontal padding, 8px gap, muted foreground defaulting to full on hover.
func navRow(c *ui.Context, label string, icon *ui.SVG, action func(), colors Tokens) {
	b := ui.ButtonBase(c).MinHeight(28).Padding(4, 8).Gap(8).Radius(6).Justify(ui.Start).TextColor(colors.Muted)
	if b.Hovered() {
		b = b.Background(colors.Selected).TextColor(colors.Foreground)
	}
	b.Children(func() {
		ui.Icon(c, icon).Size(16, 16)
		ui.Text(c, label).FontSize(14)
	})
	if b.Clicked() && action != nil {
		action()
	}
}

func workspaceHeader(c *ui.Context, p ShellProps, a ShellActions, colors Tokens) {
	ui.Row(c).Height(52).Padding(0, 16).Gap(10).Children(func() {
		title := p.WorkspaceTitle
		if title == "" {
			title = "Workspace"
		}
		ui.Text(c, title).FontSize(14)
		if p.Branch != "" {
			ui.Text(c, p.Branch).FontSize(12).TextColor(colors.Muted)
		}
		if p.Status != "" {
			ui.Text(c, p.Status).FontSize(12).TextColor(colors.Muted)
		}
		ui.Spacer(c)
		if a.ToggleExplorer != nil && iconButton(c.Key("toggle-explorer"), "Explorer", explorerIcon, colors).Clicked() {
			a.ToggleExplorer()
		}
		if a.OpenPanel != nil {
			iconButton(c.Key("open-panel"), "Open panel", plusIcon, colors).PopoverMenuButton(func(m *ui.Menu) {
				for _, kind := range []string{"terminal", "files", "changes", "pull_request"} {
					k := kind
					label := k
					if k == "pull_request" {
						label = "Pull request"
					}
					if m.Item(label).Chosen() {
						a.OpenPanel(k)
					}
				}
			})
		}
	})
}

type tabDrag struct{ ID, PaneID string }

// TabStrip presents a pane's tabs, including split and drag actions.
func TabStrip(c *ui.Context, state *ShellState, p ShellProps, a ShellActions) {
	_, colors := Theme(p.Theme, p.SystemDark)
	ui.Row(c).Height(40).Children(func() {
		ui.ScrollHorizontal(c).Grow(1).Height(40).Children(func() {
			ui.Row(c).Height(40).Gap(2).Children(func() {
				for _, tab := range p.Tabs {
					t := tab
					label := t.Title
					if t.Dirty {
						label += " •"
					}
					ui.Row(c.Key("tab:" + t.ID)).Height(36).Gap(2).Background(func() ui.Color {
						if t.Active {
							return colors.Selected
						}
						return ui.Transparent
					}()).Children(func() {
						button := ui.ButtonBase(c.Key("select")).Padding(6, 10).Drag(tabDrag{ID: t.ID, PaneID: p.PaneID}).Tooltip(t.Title)
						button.Children(func() { ui.Text(c, label).FontSize(13) })
						if a.SplitTab != nil {
							button.ContextMenu(func(m *ui.Menu) {
								if m.Item("Split right").Chosen() {
									a.SplitTab(t.ID, "horizontal")
								}
								if m.Item("Split below").Chosen() {
									a.SplitTab(t.ID, "vertical")
								}
							})
						}
						if button.Clicked() && a.SelectTab != nil {
							a.SelectTab(t.ID)
						}
						if from, ok := ui.Drop[tabDrag](button); ok && from.ID != t.ID {
							if from.PaneID != p.PaneID && a.MoveTabToPane != nil {
								a.MoveTabToPane(from.ID, p.PaneID)
							} else if from.PaneID == p.PaneID && a.ReorderTab != nil {
								a.ReorderTab(from.ID, t.ID)
							}
						}
						if a.CloseTab != nil && iconButton(c.Key("close"), "Close "+t.Title, closeIcon, colors).Clicked() {
							a.CloseTab(t.ID)
						}
					})
				}
			})
		})
		ui.Spacer(c)
		if a.SplitTab != nil {
			for _, tab := range p.Tabs {
				if !tab.Active {
					continue
				}
				id := tab.ID
				iconButton(c.Key("pane-actions"), "Pane actions", moreIcon, colors).PopoverMenuButton(func(m *ui.Menu) {
					if m.Item("Split right").Chosen() {
						a.SplitTab(id, "horizontal")
					}
					if m.Item("Split below").Chosen() {
						a.SplitTab(id, "vertical")
					}
				})
				break
			}
		}
	})
}

// PaneDropTarget allows a tab to be dropped on a pane's content, including an empty pane.
func PaneDropTarget(c *ui.Context, paneID string, move func(tabID, paneID string), content func(*ui.Context)) {
	target := ui.Column(c).Grow(1).Shrink(1)
	target.Children(func() {
		if content != nil {
			content(c)
		}
	})
	if from, ok := ui.Drop[tabDrag](target); ok && from.PaneID != paneID && move != nil {
		move(from.ID, paneID)
	}
}

func hostEditor(c *ui.Context, state *HostEditorState, a ShellActions, colors Tokens) {
	if !state.Open {
		return
	}
	canceled := false
	modal := ui.Modal(c, &state.Open, func() {
		ui.Column(c).Width(420).Padding(24).Gap(12).Background(colors.Background).Children(func() {
			ui.Text(c, "Connect to host").Bold()
			ui.TextInput(c, &state.Name).Label("Name").Placeholder("Host name")
			ui.TextInput(c, &state.URL).Label("Daemon URL").Placeholder("ws://host:port/ws")
			ui.TextInput(c, &state.Password).Label("Password").Password()
			ui.TextInput(c, &state.Credential).Label("Local credential").Password()
			if state.Error != "" {
				ui.Text(c, state.Error).TextColor(c.Theme().Danger)
			}
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if ui.Button(c, "Cancel").Clicked() {
					state.Open = false
					canceled = true
				}
				if a.SaveHost != nil && ui.PrimaryButton(c, "Save and connect").Disabled(state.Busy).Clicked() {
					a.SaveHost(state.Name, state.URL, state.Password, state.Credential)
				}
			})
		})
	})
	if (canceled || modal.Dismissed()) && a.CancelHost != nil {
		a.CancelHost()
	}
}
