package workbench

import (
	"context"
	"encoding/json"
	"github.com/egoist/mygo/ui"
	"sort"
	"strings"
	"time"
)

type commandCenterState struct {
	open, focus, directoryLoading bool
	epoch                         uint64
	directory                     []commandDirectory
	query                         string
	selected                      int
	revision                      uint64
	files                         []string
	loading                       bool
	err                           string
}

type commandEntry struct {
	id, title, section, keywords string
	run                          func()
}

func (a *App) openCommandCenter() {
	s := &a.desk.commandCenter
	s.open, s.focus = true, true
	s.epoch++
	s.query, s.selected = "", 0
	s.revision++
	s.files, s.directory = nil, nil
	s.err = ""
	s.loading = false
	s.directoryLoading = a.win != nil
	if a.win != nil {
		a.loadCommandDirectory()
		a.commandQueryChanged()
	}
}

func (a *App) commandEntries() []commandEntry {
	entries := []commandEntry{
		{"new-workspace", "New workspace", "Actions", "new branch worktree", a.openNewWorkspace},
		{"import", "Import session", "Actions", "terminal", func() { a.requestHomeAction("import") }},
		{"history", "History", "Actions", "sessions recent", a.openHistory},
		{"schedules", "Schedules", "Actions", "scheduled automation recurring", a.openSchedules},
		{"settings", "Settings", "Actions", "preferences configuration", func() { a.showDestination("settings"); a.desk.settingsOpen = true }},
	}
	if strings.TrimSpace(a.desk.commandCenter.query) != "" {
		entries = append(entries,
			commandEntry{"project", "Add project", "Actions", "open folder repo", func() { a.requestHomeAction("project") }},
			commandEntry{"home", "Home", "Actions", "start providers pair device", func() { a.showDestination("home") }},
			commandEntry{"sidebar", "Toggle left sidebar", "Actions", "panel navigation", a.toggleNavigation})
	}
	activeHost := a.desk.prefs.ActiveHost
	localDirectoryLoaded := false
	for _, d := range a.desk.commandCenter.directory {
		if d.Host == activeHost && d.Error == "" {
			localDirectoryLoaded = true
		}
	}
	seen := map[string]bool{}
	add := func(entry commandEntry) {
		if seen[entry.id] {
			return
		}
		seen[entry.id] = true
		entries = append(entries, entry)
	}
	if a.desk.destination == "workspace" && a.connected && a.desk.workspace != "" {
		if !localDirectoryLoaded {
			for _, w := range a.workspaces {
				w := w
				add(commandEntry{"workspace:" + w.ID, w.Name, "Workspaces", w.Project, func() { a.selectWorkspace(w.ID); a.showDestination("workspace") }})
			}
		}
		for _, ag := range a.agents {
			ag := ag
			title := ag.Title
			if title == "" {
				title = ag.Provider
			}
			add(commandEntry{"agent:" + activeHost + ":" + ag.ID, title, "Agents", ag.Provider, func() { a.openAgent(ag.ID); a.showDestination("workspace") }})
		}
		for _, kind := range []string{"terminal", "files", "changes", "pull_request"} {
			kind := kind
			add(commandEntry{"panel:" + kind, "Open " + strings.ReplaceAll(kind, "_", " "), "Actions", kind, func() { a.openPanel(kind); a.showDestination("workspace") }})
		}
		for _, path := range a.desk.commandCenter.files {
			path := path
			add(commandEntry{"file:" + path, path, "Files", path, func() { a.openContentTab("file", path, path); a.showDestination("workspace") }})
		}
	}
	for _, d := range a.desk.commandCenter.directory {
		hostName := d.Name
		if hostName == "" {
			hostName = d.Host
		}
		for _, w := range d.Workspaces {
			host, id := d.Host, w.ID
			title := w.Name
			if title == "" {
				title = w.Project
			}
			if title == "" {
				title = id
			}
			add(commandEntry{"workspace:" + host + ":" + id, title, "Workspaces", strings.TrimSpace(host + " " + hostName + " " + w.Project + " " + w.Name), func() { a.openHistoryAgent(host, "", id) }})
		}
		for _, ag := range d.Agents {
			host, id := d.Host, ag.ID
			title := ag.Title
			if title == "" {
				title = ag.Provider
			}
			add(commandEntry{"agent:" + host + ":" + id, title, "Agents", strings.TrimSpace(host + " " + hostName + " " + ag.Provider), func() { a.openHistoryAgent(host, id, ag.WorkspaceID) }})
		}
	}
	return rankCommandEntries(entries, a.desk.commandCenter.query)
}
func rankCommandEntries(entries []commandEntry, query string) []commandEntry {
	words := strings.Fields(strings.ToLower(query))
	out := make([]commandEntry, 0, len(entries))
	for _, entry := range entries {
		hay := strings.ToLower(entry.title + " " + entry.keywords)
		match := true
		for _, word := range words {
			if !strings.Contains(hay, word) {
				match = false
				break
			}
		}
		if match {
			out = append(out, entry)
		}
	}
	if len(words) > 0 {
		prefix := strings.Join(words, " ")
		sort.SliceStable(out, func(i, j int) bool {
			return strings.HasPrefix(strings.ToLower(out[i].title), prefix) && !strings.HasPrefix(strings.ToLower(out[j].title), prefix)
		})
	}
	return out
}
func (a *App) commandQueryChanged() {
	s := &a.desk.commandCenter
	s.selected = 0
	s.revision++
	revision := s.revision
	s.files = nil
	s.err = ""
	if !a.connected || a.workspaceCWD() == "" {
		s.loading = false
		return
	}
	s.loading = true
	gen, c, cwd, host, workspace, query := a.generation, a.client, a.workspaceCWD(), a.desk.prefs.ActiveHost, a.desk.workspace, s.query
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		payload, _ := json.Marshal(map[string]any{"cwd": cwd, "query": query, "includeFiles": true, "includeDirectories": false, "limit": 50})
		raw, err := c.Call(ctx, "directory_suggestions_request", payload)
		var response struct {
			Entries []struct {
				Path string `json:"path"`
			} `json:"entries"`
			Error string `json:"error"`
		}
		if err == nil {
			err = json.Unmarshal(raw, &response)
		}
		a.update(gen, func() {
			s := &a.desk.commandCenter
			if !s.open || s.revision != revision || host != a.desk.prefs.ActiveHost || workspace != a.desk.workspace {
				return
			}
			s.loading = false
			if err != nil {
				s.err = err.Error()
				return
			}
			s.err = response.Error
			for _, e := range response.Entries {
				s.files = append(s.files, e.Path)
			}
		})
	}()
}
func (a *App) commandCenterDialog(c *ui.Context) {
	s := &a.desk.commandCenter
	if !s.open {
		return
	}
	ui.Modal(c, &s.open, func() {
		ui.Column(c).Width(620).MaxWidth(900).Padding(16).Gap(8).Children(func() {
			entries := a.commandEntries()
			if s.selected >= len(entries) {
				s.selected = max(0, len(entries)-1)
			}
			execute := func() {
				if s.selected >= 0 && s.selected < len(entries) {
					run := entries[s.selected].run
					s.open = false
					s.revision++
					run()
				}
			}
			input := ui.TextInput(c.Key("command-query"), &s.query).Label("Search commands, workspaces and files").Placeholder("Search commands, workspaces and files").OnChange(a.commandQueryChanged).OnSubmit(execute).OnTextKey(func(k ui.TextKey) bool {
				switch k.Key {
				case ui.KeyEscape:
					s.open = false
					s.revision++
					return true
				case ui.KeyDown:
					if len(entries) > 0 {
						s.selected = (s.selected + 1) % len(entries)
					}
					return true
				case ui.KeyUp:
					if len(entries) > 0 {
						s.selected = (s.selected + len(entries) - 1) % len(entries)
					}
					return true
				}
				return false
			})
			if s.focus {
				input.Focus()
				s.focus = false
			}
			if s.directoryLoading {
				ui.Text(c, "Loading workspaces…")
			}
			for _, d := range s.directory {
				if d.Error != "" {
					ui.Text(c, d.Name+": "+d.Error).TextColor(c.Theme().Danger)
				}
			}
			if s.loading {
				ui.Text(c, "Searching files…")
			}
			if s.err != "" {
				ui.Text(c, s.err).TextColor(c.Theme().Danger)
			}
			ui.Scroll(c).Height(400).Children(func() {
				section := ""
				for i, e := range entries {
					if e.section != section {
						section = e.section
						ui.Text(c, section).TextColor(c.Theme().TextMuted).FontSize(12)
					}
					b := ui.ButtonBase(c.Key(e.id)).Label(e.title).Height(32).Padding(4, 8).Justify(ui.Start)
					if i == s.selected || b.Hovered() {
						b.Background(c.Theme().Surface)
					}
					b.Children(func() { ui.Text(c, e.title) })
					if b.Clicked() {
						s.selected = i
						execute()
					}
				}
				if len(entries) == 0 {
					ui.Text(c, "No results")
				}
			})
			if ui.Button(c, "Close").Clicked() {
				s.open = false
				s.revision++
			}
		})
	})
}
