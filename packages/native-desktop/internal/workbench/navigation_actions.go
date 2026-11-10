package workbench

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/client"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/desktopui"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/workbenchstate"
)

var errNavigationPending = errors.New("operation accepted and pending; refresh the directory to check its outcome")

type navigationState struct {
	open, busy                                      bool
	kind, id, value, original, root, err, clipboard string
	revision                                        json.RawMessage
	blocked                                         map[string]bool
}

// navigationRequest sends one operation and checks its documented acknowledgement.
// Client.Call supplies and correlates the request ID itself.
func navigationRequest(ctx context.Context, c caller, kind string, payload map[string]any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	raw, err = c.Call(ctx, kind, raw)
	if err != nil {
		if errors.Is(err, client.ErrOutcomeUnknown) {
			return fmt.Errorf("outcome unknown: %w", err)
		}
		return err
	}
	var ack struct {
		WorkspaceID string          `json:"workspaceId"`
		ProjectID   string          `json:"projectId"`
		Accepted    *bool           `json:"accepted"`
		Success     *bool           `json:"success"`
		Error       *string         `json:"error"`
		ArchivedAt  *string         `json:"archivedAt"`
		Project     json.RawMessage `json:"project"`
		Workspace   json.RawMessage `json:"workspace"`
		Creation    *struct {
			Phase          string  `json:"phase"`
			Error          *string `json:"error"`
			OutcomeUnknown bool    `json:"outcomeUnknown"`
		} `json:"creation"`
	}
	if json.Unmarshal(raw, &ack) != nil {
		return errors.New("invalid acknowledgement")
	}
	if ack.Error != nil && *ack.Error != "" {
		return errors.New(*ack.Error)
	}
	if id, ok := payload["workspaceId"].(string); ok && ack.WorkspaceID != id {
		return errors.New("invalid workspace acknowledgement")
	}
	if id, ok := payload["projectId"].(string); ok && ack.ProjectID != id {
		return errors.New("invalid project acknowledgement")
	}
	switch kind {
	case "workspace.title.set.request", "workspace.pin.set.request", "project.rename.request", "project.remove.request":
		if ack.Accepted == nil {
			return errors.New("invalid acknowledgement: missing accepted")
		}
		if !*ack.Accepted {
			return errors.New("request rejected")
		}
	case "workspace.clear_attention.request", "workspace.mark_unread.request":
		if ack.Success == nil {
			return errors.New("invalid acknowledgement: missing success")
		}
		if !*ack.Success {
			return errors.New("request rejected")
		}
	case "archive_workspace_request":
		if ack.ArchivedAt == nil || *ack.ArchivedAt == "" {
			return errors.New("invalid acknowledgement: missing archive time")
		}
	case "project.add.request":
		if len(ack.Project) == 0 || string(ack.Project) == "null" {
			return errors.New("invalid acknowledgement: missing project")
		}
	case "workspace.create.request":
		if ack.Creation != nil {
			if ack.Creation.Error != nil && *ack.Creation.Error != "" {
				return errors.New(*ack.Creation.Error)
			}
			if ack.Creation.OutcomeUnknown {
				return fmt.Errorf("outcome unknown: %w", client.ErrOutcomeUnknown)
			}
			if ack.Creation.Phase == "failed" {
				return errors.New("workspace creation failed")
			}
			if ack.Creation.Phase != "completed" {
				return errNavigationPending
			}
		}
		if len(ack.Workspace) == 0 || string(ack.Workspace) == "null" {
			return errNavigationPending
		}
	default:
		return errors.New("unsupported navigation operation")
	}
	return nil
}

func (a *App) navigationProject(id string) (Project, bool) {
	for _, p := range a.projects {
		if p.ID == id {
			return p, true
		}
	}
	for _, w := range a.workspaces {
		if w.ProjectID == id {
			return Project{ID: id, Name: w.Project, Root: w.Root, Kind: w.Kind}, true
		}
	}
	return Project{}, false
}
func (a *App) navigationWorkspace(id string) (Workspace, bool) {
	for _, w := range a.workspaces {
		if w.ID == id {
			return w, true
		}
	}
	return Workspace{}, false
}
func (a *App) navigationOpen(kind, id, value, root string) bool {
	n := &a.desk.navigation
	if n.busy {
		return false
	}
	if n.open && n.kind == "settings" && n.value != n.original {
		a.confirm("Discard unsaved project settings?", func() {
			n.open = false
			if a.navigationOpen(kind, id, value, root) && kind == "settings" {
				a.navigationReadSettings()
			}
		})
		return false
	}
	*n = navigationState{open: true, kind: kind, id: id, value: value, original: value, root: root, blocked: n.blocked}
	return true
}
func (a *App) navigationDirty(ids []string) bool {
	for _, id := range ids {
		if p := a.desk.supporting[workbenchstate.Scope(a.desk.prefs.ActiveHost, id)]; p != nil && p.HasUnsavedWork() {
			return true
		}
	}
	return false
}
func (a *App) navigationExecute(key, kind string, payload map[string]any, closeDialog bool) {
	n := &a.desk.navigation
	if n.busy {
		return
	}
	if a.client == nil || !a.connected {
		n.err = "Connect to the host first."
		a.status = n.err
		return
	}
	if n.blocked[key] {
		n.err = "Previous outcome is unknown or pending."
		n.open = true
		n.kind = "outcome"
		n.id = key
		n.value = "Check the refreshed directory and confirm the result before trying this action again."
		a.status = n.err
		return
	}
	n.busy = true
	n.err = ""
	daemon, gen := a.client, a.generation
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		err := navigationRequest(ctx, daemon, kind, payload)
		a.update(gen, func() {
			n := &a.desk.navigation
			n.busy = false
			if err != nil {
				n.err = err.Error()
				a.status = n.err
				if errors.Is(err, client.ErrOutcomeUnknown) || errors.Is(err, errNavigationPending) {
					if n.blocked == nil {
						n.blocked = map[string]bool{}
					}
					n.blocked[key] = true
					n.open = true
					n.kind = "outcome"
					n.id = key
					n.value = "Check the refreshed directory and confirm the result before trying this action again."
					a.refreshDirectory()
				}
				return
			}
			if closeDialog {
				n.open = false
			}
			a.status = "Done"
			a.refreshDirectory()
		})
	}()
}
func (a *App) installNavigationActions(actions *desktopui.ShellActions) {
	actions.RenameWorkspace = func(id string) {
		if w, ok := a.navigationWorkspace(id); ok {
			a.navigationOpen("workspace rename", id, w.Name, "")
		}
	}
	actions.RenameProject = func(id string) {
		if p, ok := a.navigationProject(id); ok {
			a.navigationOpen("project rename", id, p.Name, p.Root)
		}
	}
	actions.AddProject = func() { a.requestHomeAction("project") }
	actions.NewWorkspace = func() { a.requestHomeAction("project") }
	actions.CreateWorkspace = func(id string) {
		if p, ok := a.navigationProject(id); ok {
			a.navigationOpen("create workspace", id, p.Root, p.Root)
		}
	}
	actions.CopyWorkspacePath = func(id string) {
		if w, ok := a.navigationWorkspace(id); ok {
			path := w.Directory
			if path == "" {
				path = w.Root
			}
			if path != "" {
				a.desk.navigation.clipboard = path
			} else {
				a.status = "Workspace path unavailable"
			}
		}
	}
	actions.CopyWorkspaceBranch = func(id string) {
		if w, ok := a.navigationWorkspace(id); ok {
			if w.Git.CurrentBranch != "" {
				a.desk.navigation.clipboard = w.Git.CurrentBranch
			} else {
				a.status = "Workspace branch unavailable"
			}
		}
	}
	actions.MarkWorkspaceRead = func(id string) {
		a.navigationExecute("workspace:"+id, "workspace.clear_attention.request", map[string]any{"workspaceId": id}, false)
	}
	actions.MarkWorkspaceUnread = func(id string) {
		a.navigationExecute("workspace:"+id, "workspace.mark_unread.request", map[string]any{"workspaceId": id}, false)
	}
	actions.ToggleWorkspacePin = func(id string) {
		if w, ok := a.navigationWorkspace(id); ok {
			a.navigationExecute("workspace:"+id, "workspace.pin.set.request", map[string]any{"workspaceId": id, "pinned": w.PinnedAt == nil}, false)
		}
	}
	actions.ArchiveWorkspace = func(id string) {
		w, ok := a.navigationWorkspace(id)
		if !ok {
			return
		}
		message := "Archive workspace " + w.Name + "?"
		if a.navigationDirty([]string{id}) {
			message += " Unsaved file changes will be lost."
		}
		a.navigationOpen("confirm archive", id, message, "")
	}
	actions.RemoveProject = func(id string) {
		p, ok := a.navigationProject(id)
		if !ok {
			return
		}
		var ids []string
		for _, w := range a.workspaces {
			if w.ProjectID == id {
				ids = append(ids, w.ID)
			}
		}
		message := "Remove project " + p.Name + " and its workspaces?"
		if a.navigationDirty(ids) {
			message += " Unsaved file changes will be lost."
		}
		a.navigationOpen("confirm remove", id, message, "")
	}
	actions.ProjectSettings = func(id string) {
		p, ok := a.navigationProject(id)
		if !ok || p.Root == "" {
			a.status = "Project directory unavailable"
			return
		}
		if a.navigationOpen("settings", id, "", p.Root) {
			a.navigationReadSettings()
		}
	}
}

func (a *App) navigationReadSettings() {
	n := &a.desk.navigation
	if a.client == nil || !a.connected {
		n.err = "Connect to the host first."
		return
	}
	n.busy = true
	daemon, gen, root := a.client, a.generation, n.root
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		raw, _ := json.Marshal(map[string]string{"repoRoot": root})
		response, err := daemon.Call(ctx, "read_project_config_request", raw)
		var result struct {
			OK       bool            `json:"ok"`
			Config   json.RawMessage `json:"config"`
			Revision json.RawMessage `json:"revision"`
			Error    json.RawMessage `json:"error"`
		}
		if err == nil {
			err = json.Unmarshal(response, &result)
			if err == nil && !result.OK {
				err = fmt.Errorf("project settings: %s", result.Error)
			}
		}
		a.update(gen, func() {
			n := &a.desk.navigation
			n.busy = false
			if err != nil {
				n.err = err.Error()
				return
			}
			if len(result.Config) == 0 || string(result.Config) == "null" {
				result.Config = json.RawMessage(`{}`)
			}
			var pretty any
			if json.Unmarshal(result.Config, &pretty) != nil {
				n.err = "Invalid project settings"
				return
			}
			b, _ := json.MarshalIndent(pretty, "", "  ")
			n.value = string(b)
			n.original = n.value
			n.revision = result.Revision
			delete(n.blocked, "settings:"+n.id)
		})
	}()
}
func (a *App) navigationSaveSettings() {
	n := &a.desk.navigation
	var cfg map[string]any
	if json.Unmarshal([]byte(n.value), &cfg) != nil || cfg == nil {
		n.err = "Enter a JSON object."
		return
	}
	if a.client == nil || !a.connected {
		n.err = "Connect to the host first."
		return
	}
	if n.blocked["settings:"+n.id] {
		n.err = "Previous save outcome is unknown; reopen settings to inspect it."
		return
	}
	n.busy = true
	daemon, gen, root, id := a.client, a.generation, n.root, n.id
	revision := n.revision
	value := n.value
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		raw, _ := json.Marshal(map[string]any{"repoRoot": root, "config": cfg, "expectedRevision": revision})
		response, err := daemon.Call(ctx, "write_project_config_request", raw)
		var result struct {
			OK    *bool           `json:"ok"`
			Error json.RawMessage `json:"error"`
		}
		if err == nil {
			err = json.Unmarshal(response, &result)
			if err == nil && (result.OK == nil || !*result.OK) {
				err = fmt.Errorf("settings save rejected: %s", result.Error)
			}
		}
		a.update(gen, func() {
			n := &a.desk.navigation
			n.busy = false
			if err != nil {
				n.err = err.Error()
				if errors.Is(err, client.ErrOutcomeUnknown) {
					n.err = "Save outcome unknown: " + err.Error()
					if n.blocked == nil {
						n.blocked = map[string]bool{}
					}
					n.blocked["settings:"+id] = true
				}
				return
			}
			n.original = value
			n.open = false
			a.status = "Project settings saved"
		})
	}()
}
func (a *App) navigationDialogs(c *ui.Context) {
	n := &a.desk.navigation
	if n.clipboard != "" {
		c.WriteClipboard(n.clipboard)
		n.clipboard = ""
	}
	modalOpen := n.open
	ui.Modal(c, &modalOpen, func() {
		ui.Column(c).Width(520).Padding(24).Gap(10).Children(func() {
			ui.Text(c, strings.Title(n.kind)).Bold()
			if n.kind == "confirm archive" || n.kind == "confirm remove" || n.kind == "outcome" {
				ui.Text(c, n.value)
			} else if n.kind == "settings" {
				ui.TextArea(c, &n.value).Label("Project configuration (JSON)").MinHeight(240)
			} else {
				label := "Name"
				if n.kind == "add project" || n.kind == "create workspace" {
					label = "Directory path"
				}
				ui.TextInput(c, &n.value).Label(label)
			}
			if n.err != "" {
				ui.Text(c, n.err)
			}
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c, "Cancel").Disabled(n.busy).Clicked() {
					if n.kind == "settings" && n.value != n.original {
						a.confirm("Discard unsaved project settings?", func() { n.open = false })
					} else {
						n.open = false
					}
				}
				label := "Save"
				if n.kind == "confirm archive" {
					label = "Archive"
				}
				if n.kind == "confirm remove" {
					label = "Remove"
				}
				if n.kind == "outcome" {
					label = "I checked the result"
				}
				if n.kind == "add project" || n.kind == "create workspace" {
					label = "Create"
				}
				if ui.PrimaryButton(c, label).Disabled(n.busy).Clicked() {
					value := strings.TrimSpace(n.value)
					switch n.kind {
					case "workspace rename":
						a.navigationExecute("workspace:"+n.id, "workspace.title.set.request", map[string]any{"workspaceId": n.id, "title": value}, true)
					case "project rename":
						a.navigationExecute("project:"+n.id, "project.rename.request", map[string]any{"projectId": n.id, "customName": value}, true)
					case "add project":
						if value == "" {
							n.err = "Enter a directory path."
							return
						}
						a.navigationExecute("add:"+value, "project.add.request", map[string]any{"cwd": value}, true)
					case "create workspace":
						if value == "" {
							n.err = "Enter a directory path."
							return
						}
						a.navigationExecute("create:"+n.id, "workspace.create.request", map[string]any{"source": map[string]any{"kind": "directory", "path": value, "projectId": n.id}, "idempotencyKey": rand.Text()}, true)
					case "confirm archive":
						a.navigationExecute("workspace:"+n.id, "archive_workspace_request", map[string]any{"workspaceId": n.id}, true)
					case "confirm remove":
						a.navigationExecute("project:"+n.id, "project.remove.request", map[string]any{"projectId": n.id}, true)
					case "settings":
						a.navigationSaveSettings()
					case "outcome":
						delete(n.blocked, n.id)
						n.open = false
					}
				}
			})
		})
	})
	if n.open && !modalOpen {
		if n.busy {
			return
		}
		if n.kind == "settings" && n.value != n.original {
			a.confirm("Discard unsaved project settings?", func() { n.open = false })
		} else {
			n.open = false
		}
	}
}
