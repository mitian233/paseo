package workbench

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/client"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/timeline"
)

func (a *App) openForkDraft(attachment json.RawMessage, target string) {
	if a.owner != nil {
		a.owner.openForkDraft(attachment, target)
		return
	}
	if len(attachment) == 0 || string(attachment) == "null" {
		a.status = "Fork context was not returned by the daemon."
		return
	}
	a.desk.forkAttachment = append(json.RawMessage(nil), attachment...)
	a.desk.forkTarget = target
	a.desk.newAgentPrompt = ""
	a.desk.newWorkspaceTitle = ""
	a.desk.creationPayload = nil
	a.desk.creationType = ""
	a.newAgent()
}

func (a *App) creationDialog(c *ui.Context) {
	ui.Modal(c, &a.desk.newAgentOpen, func() {
		ui.Column(c).Width(480).Padding(24).Gap(12).Children(func() {
			title := "New agent"
			if a.desk.forkTarget == "workspace" {
				title = "Fork into a new workspace"
			} else if len(a.desk.forkAttachment) > 0 {
				title = "Fork into a new conversation"
			}
			ui.Text(c, title).Bold()
			ui.Column(c).Disabled(a.desk.newAgentBusy || len(a.desk.creationPayload) > 0).Gap(8).Children(func() {
				ui.TextInput(c, &a.desk.newAgentTitle).Label("Title").Placeholder("Conversation title")
				if a.desk.forkTarget == "workspace" {
					ui.TextInput(c, &a.desk.newWorkspaceTitle).Label("Workspace name").Placeholder("New workspace name")
				}
				provider := a.desk.newAgentProvider
				if provider == "" {
					provider = "Choose provider"
				}
				ui.PopoverMenuButton(c, provider, func(m *ui.Menu) {
					for _, p := range a.desk.providers {
						if m.Item(p).Chosen() {
							a.desk.newAgentProvider = p
						}
					}
				})
				ui.TextArea(c, &a.desk.newAgentPrompt).Label("Initial message").Placeholder("Initial message (optional)").MinHeight(80)
				if len(a.desk.forkAttachment) > 0 {
					ui.Text(c, "Conversation history is attached to this draft.").TextColor(c.Theme().TextMuted)
				}
			})
			if a.desk.newAgentError != "" {
				ui.Text(c, a.desk.newAgentError)
			}
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					a.desk.newAgentOpen = false
				}
				label := "Create"
				if len(a.desk.creationPayload) > 0 {
					label = "Retry previous request"
				}
				if ui.PrimaryButton(c, label).Disabled(a.desk.newAgentBusy || a.desk.newAgentProvider == "").Clicked() {
					a.submitCreation()
				}
			})
		})
	})
}

func (a *App) submitCreation() {
	if a.desk.newAgentBusy || a.client == nil || !a.connected {
		return
	}
	c, gen, workspace, cwd := a.client, a.generation, a.desk.workspace, a.workspaceCWD()
	if len(a.desk.creationPayload) == 0 {
		config := map[string]any{"provider": a.desk.newAgentProvider, "cwd": cwd}
		if title := strings.TrimSpace(a.desk.newAgentTitle); title != "" {
			config["title"] = title
		}
		agent := map[string]any{"config": config}
		if prompt := strings.TrimSpace(a.desk.newAgentPrompt); prompt != "" {
			agent["initialPrompt"] = prompt
			agent["clientMessageId"] = rand.Text()
		}
		if len(a.desk.forkAttachment) > 0 {
			agent["attachments"] = []json.RawMessage{a.desk.forkAttachment}
		}
		if a.desk.forkTarget == "workspace" {
			var source map[string]any
			isGit := false
			for _, w := range a.workspaces {
				if w.ID == workspace {
					isGit = w.Kind == "git"
					break
				}
			}
			if isGit {
				source = map[string]any{"kind": "worktree", "cwd": cwd, "action": "branch-off", "branchName": "paseo/fork-" + strings.ToLower(rand.Text()[:8])}
			} else {
				source = map[string]any{"kind": "directory", "path": cwd}
			}
			body := map[string]any{"source": source, "agent": agent, "idempotencyKey": rand.Text()}
			if title := strings.TrimSpace(a.desk.newWorkspaceTitle); title != "" {
				body["title"] = title
			}
			a.desk.creationType = "workspace.create.request"
			a.desk.creationPayload, _ = json.Marshal(body)
		} else {
			agent["workspaceId"] = workspace
			agent["idempotencyKey"] = rand.Text()
			a.desk.creationType = "create_agent_request"
			a.desk.creationPayload, _ = json.Marshal(agent)
		}
	}
	a.desk.newAgentBusy = true
	payload := append(json.RawMessage(nil), a.desk.creationPayload...)
	typ := a.desk.creationType
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		raw, err := c.Call(ctx, typ, payload)
		a.update(gen, func() {
			a.desk.newAgentBusy = false
			if err != nil {
				a.desk.newAgentError = err.Error()
				if errors.Is(err, client.ErrOutcomeUnknown) {
					a.desk.newAgentError = "Result unknown. Retry reuses this exact submission. " + err.Error()
				} else {
					a.desk.creationPayload = nil
				}
				return
			}
			var ack struct {
				Agent     *Agent     `json:"agent"`
				Workspace *Workspace `json:"workspace"`
				Error     *string    `json:"error"`
			}
			if err := json.Unmarshal(raw, &ack); err != nil {
				a.desk.newAgentError = "Invalid creation acknowledgement: " + err.Error()
				return
			}
			if ack.Error != nil {
				a.desk.newAgentError = *ack.Error
				a.desk.creationPayload = nil
				return
			}
			if ack.Agent == nil || ack.Agent.ID == "" {
				a.desk.newAgentError = "Creation acknowledged; waiting for authoritative directory."
				a.refreshDirectory()
				return
			}
			a.desk.newAgentOpen = false
			a.desk.creationPayload = nil
			a.desk.forkAttachment = nil
			a.desk.forkTarget = ""
			a.desk.newAgentPrompt = ""
			a.desk.newAgentTitle = ""
			if ack.Workspace != nil {
				a.workspaces = append(a.workspaces, *ack.Workspace)
				a.selectWorkspace(ack.Workspace.ID)
			}
			exists := false
			for i, ag := range a.agents {
				if ag.ID == ack.Agent.ID {
					a.agents[i] = *ack.Agent
					exists = true
					break
				}
			}
			if !exists {
				a.agents = append(a.agents, *ack.Agent)
			}
			a.openAgent(ack.Agent.ID)
		})
	}()
}

func (a *App) refreshDirectory() {
	if a.client == nil || !a.connected {
		return
	}
	c, gen := a.client, a.generation
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		ws, agents, err := loadDirectory(ctx, c)
		a.update(gen, func() {
			if err != nil {
				a.status = err.Error()
				return
			}
			a.workspaces = ws
			a.agents = agents
			a.refreshSidebarDirectory()
			a.completePendingNavigation()
		})
	}()
}

func (a *App) rewindCompleted(item timeline.Item, mode string) {
	if mode == "files" {
		if p := a.currentSupporting(); p != nil {
			p.Open("changes", "")
		}
		a.status = "Files restored"
		return
	}
	id := a.selected
	a.draft = item.Text
	a.captureConversation()
	a.selectAgent(id)
}

func (a *App) refreshProjects() {
	if a.client == nil || !a.connected {
		return
	}
	daemon, gen := a.client, a.generation
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		raw, err := daemon.Call(ctx, "project.list.request", json.RawMessage(`{}`))
		var response struct {
			Projects []Project `json:"projects"`
		}
		if err == nil {
			err = json.Unmarshal(raw, &response)
			if err == nil && response.Projects == nil {
				err = errors.New("invalid project directory response")
			}
		}
		a.update(gen, func() {
			if err != nil {
				a.desk.notice = "Project list unavailable: " + err.Error()
				return
			}
			a.projects = response.Projects
			if strings.HasPrefix(a.desk.notice, "Project list unavailable:") {
				a.desk.notice = ""
			}
		})
	}()
}
