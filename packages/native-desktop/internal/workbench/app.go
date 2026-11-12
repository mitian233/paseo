package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/client"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/timeline"
)

type App struct {
	win                            *mygo.Window
	URL, Password, Credential      string
	draft, status, host, selected  string
	workspaces                     []Workspace
	agents                         []Agent
	items                          []timeline.Item
	selectionCtx                   context.Context
	connected, connecting, sending bool
	observerReady                  bool
	generation, selection          uint64
	cancel, cancelSelection        context.CancelFunc
	client                         *client.Client
	list                           ui.ListState
	markdown                       map[string]*markdown.Options
	diagrams                       *Diagrams
	permissions                    permissions
	prompts                        promptSender
	desk                           desktopState
	toolExpanded                   map[string]bool
	agentRunning                   bool
	turnExpanded                   map[string]bool
	rewindCapabilities             struct{ Conversation, Files, Both bool }
	forkAvailable                  bool
	conversationActionBusy         bool
	projects                       []Project
	owner                          *App
	terminalRestoreSupported       bool
}

func terminalRestoreAvailable(serverInfo json.RawMessage) bool {
	var info struct {
		Features struct {
			TerminalRestoreModes bool `json:"terminalRestoreModes"`
		} `json:"features"`
	}
	return json.Unmarshal(serverInfo, &info) == nil && info.Features.TerminalRestoreModes
}

func New(url string) *App { a := &App{URL: url}; a.initializeDesktop(url); return a }
func (a *App) Open() {
	a.win = mygo.NewWindow(mygo.WindowOptions{Title: "Paseo", Width: 1100, Height: 760, MinWidth: 700, MinHeight: 480, Content: ui.View(a.view)})
	a.diagrams = NewDiagrams(a.win.Update)
	a.win.OnClosed(func() {
		a.captureConversation()
		a.closeSidebarDirectory()
		a.persistDesktop()
		a.stop()
		a.diagrams.Close()
	})
	if a.URL != "" {
		a.connect()
	}
}
func (a *App) stop() {
	a.captureConversation()
	a.closeWorkspacePanels()
	a.closeHomeImport()
	a.closeHomeProviders()
	a.closeHomePairing()
	a.closeHistory()
	a.closeSchedules()
	a.closeNewWorkspace()
	a.closeUsage()
	a.desk.commandCenter.open = false
	a.desk.commandCenter.revision++
	a.workspaces = nil
	a.agents = nil
	a.projects = nil
	a.desk.navigation = navigationState{}
	a.generation++
	a.selection++
	a.clearContent()
	if a.cancelSelection != nil {
		a.cancelSelection()
		a.cancelSelection = nil
	}
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	if a.client != nil {
		c := a.client
		a.client = nil
		go c.Close()
	}
	a.connected = false
	a.connecting = false
	a.sending = false
}

func (a *App) clearContent() {
	a.observerReady = false
	a.items = nil
	a.markdown = nil
	a.permissions = permissions{}
	if a.diagrams != nil {
		a.diagrams.Reset()
	}
}
func (a *App) update(gen uint64, fn func()) {
	a.win.Update(func() {
		if a.generation == gen {
			fn()
		}
	})
}
func (a *App) connect() {
	a.stop()
	a.workspaces = nil
	a.agents = nil
	a.selected = ""
	a.host = ""
	a.connecting = true
	a.status = "Connecting…"
	gen := a.generation
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	c := &client.Client{}
	a.client = c
	target := client.Target{URL: strings.TrimSpace(a.URL), Password: a.Password, LocalCredential: a.Credential}
	go func() {
		connectCtx, done := context.WithTimeout(ctx, 20*time.Second)
		err := c.Connect(connectCtx, target)
		done()
		if err != nil {
			a.update(gen, func() { a.connecting = false; a.status = "Connection failed: " + err.Error() })
			return
		}
		var info struct {
			ServerID string `json:"serverId"`
			Hostname string `json:"hostname"`
			Features struct {
				Fork                 bool `json:"agentForkContext"`
				ForkCursor           bool `json:"agentForkContextCursor"`
				TerminalRestoreModes bool `json:"terminalRestoreModes"`
			} `json:"features"`
		}
		_ = json.Unmarshal(c.ServerInfo(), &info)
		identity := info.Hostname
		if identity == "" {
			identity = info.ServerID
		}
		if identity == "" {
			identity = "Unknown host"
		}
		if info.ServerID != "" && identity != info.ServerID {
			identity += " (" + info.ServerID + ")"
		}
		a.update(gen, func() {
			a.connecting = false
			a.connected = true
			a.host = identity
			a.forkAvailable = info.Features.Fork && info.Features.ForkCursor
			a.terminalRestoreSupported = terminalRestoreAvailable(c.ServerInfo())
			a.refreshProjects()
			a.status = "Loading workspaces and agents…"
		})
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case event := <-c.Events():
					if event.State == "disconnected" {
						a.update(gen, func() {
							a.connected = false
							a.pauseConversationOwners()
							a.sending = false
							a.selection++
							if a.cancelSelection != nil {
								a.cancelSelection()
								a.cancelSelection = nil
							}
							a.selected = ""
							a.clearContent()
							if event.Err != nil {
								a.status = "Disconnected: " + event.Err.Error()
							} else {
								a.status = "Disconnected"
							}
						})
						return
					}
				}
			}
		}()
		requestCtx, finish := context.WithTimeout(ctx, 30*time.Second)
		ws, agents, err := loadDirectory(requestCtx, c)
		finish()
		a.update(gen, func() {
			if err != nil {
				a.status = "Directory error: " + err.Error()
				// The transport is up but the directory is not. The pending
				// navigation stays pending until a refresh succeeds.
				a.completePendingNavigation()
				return
			}
			a.workspaces = ws
			a.agents = agents
			a.restoreWorkspace()
			a.status = fmt.Sprintf("%d workspaces · %d agents", len(ws), len(agents))
			a.resumeHomeAction()
			a.completePendingNavigation()
		})
	}()
}
func (a *App) selectAgent(id string) {
	a.captureConversation()
	if a.cancelSelection != nil {
		a.cancelSelection()
	}
	a.selection++
	selection := a.selection
	gen := a.generation
	a.selected = id
	a.clearContent()
	a.sending = false
	a.restoreConversation(id)
	a.status = "Loading conversation…"
	ctx, cancel := context.WithCancel(context.Background())
	a.cancelSelection = cancel
	a.selectionCtx = ctx
	c := a.client
	go a.observe(ctx, cancel, c, gen, selection, id)
}
func (a *App) selectionUpdate(gen, selection uint64, fn func()) {
	a.update(gen, func() {
		if a.selection == selection {
			fn()
		}
	})
}
func (a *App) observe(ctx context.Context, cancelSelection context.CancelFunc, c *client.Client, gen, selection uint64, id string) {
	observeCtx, observeCancel := context.WithCancel(ctx)
	var sub *client.Subscription
	defer func() {
		cancelSelection()
		observeCancel()
		a.selectionUpdate(gen, selection, func() { a.observerReady = false })
		if sub != nil {
			_ = sub.Close()
		}
	}()
	timer := time.AfterFunc(20*time.Second, observeCancel)
	var err error
	sub, err = c.Observe(observeCtx, client.Observation{AgentIDs: []string{id}})
	timer.Stop()
	if err != nil {
		a.selectionUpdate(gen, selection, func() { a.status = "Timeline subscription failed: " + err.Error() })
		return
	}
	var model timeline.Model
	type history struct {
		data json.RawMessage
		err  error
	}
	var result <-chan history
	var buffered []json.RawMessage
	fetch := func() {
		ch := make(chan history, 1)
		result = ch
		go func() {
			requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			p, _ := json.Marshal(map[string]any{"agentId": id, "direction": "tail", "limit": 0, "projection": "projected"})
			raw, err := c.Call(requestCtx, "fetch_agent_timeline_request", p)
			ch <- history{raw, err}
		}()
	}
	fetch()
	publish := func() { items := model.Items(); a.selectionUpdate(gen, selection, func() { a.items = items }) }
	for {
		select {
		case <-ctx.Done():
			return
		case message, ok := <-sub.Updates:
			if !ok {
				err := sub.Err()
				a.selectionUpdate(gen, selection, func() {
					if err != nil {
						a.status = "Timeline disconnected: " + err.Error()
					} else {
						a.status = "Timeline subscription closed"
					}
				})
				return
			}
			if result != nil {
				if len(buffered) >= 50000 {
					a.selectionUpdate(gen, selection, func() { a.status = "Timeline updates exceeded the history buffer; select the agent again." })
					return
				}
				buffered = append(buffered, message)
				continue
			}
			a.selectionUpdate(gen, selection, func() {
				if ctx.Err() != nil {
					return
				}
				a.applyAgentActivity(message)
				if err := a.permissions.apply(id, message); err != nil {
					cancelSelection()
					a.status = err.Error()
				}
			})
			reconcile, err := model.Apply(message)
			if err != nil {
				a.selectionUpdate(gen, selection, func() { a.status = "Timeline error: " + err.Error() })
				return
			}
			if reconcile {
				fetch()
			} else {
				publish()
			}
		case res := <-result:
			result = nil
			if res.err != nil {
				a.selectionUpdate(gen, selection, func() { a.status = "History error: " + res.err.Error() })
				return
			}
			if err := model.Replace(res.data); err != nil {
				a.selectionUpdate(gen, selection, func() { a.status = "History error: " + err.Error() })
				return
			}
			pending := buffered
			buffered = nil
			for i, message := range pending {
				reconcile, err := model.Apply(message)
				if err != nil {
					a.selectionUpdate(gen, selection, func() { a.status = "Timeline error: " + err.Error() })
					return
				}
				if reconcile {
					buffered = append(buffered, pending[i+1:]...)
					fetch()
					break
				}
			}
			ready := result == nil
			a.selectionUpdate(gen, selection, func() {
				if ctx.Err() != nil {
					return
				}
				if err := a.permissions.replace(id, res.data); err != nil {
					cancelSelection()
					a.status = err.Error()
					return
				}
				a.applyAgentActivity(res.data)
				for _, message := range pending {
					a.applyAgentActivity(message)
					if err := a.permissions.apply(id, message); err != nil {
						cancelSelection()
						a.status = err.Error()
						return
					}
				}
				a.observerReady = ready
			})
			publish()
			a.selectionUpdate(gen, selection, func() {
				if a.status == "Loading conversation…" {
					a.status = "Conversation live"
				}
			})
		}
	}
}
func (a *App) canMutate() bool {
	return a.connected && a.selected != "" && a.observerReady && a.selectionCtx != nil && a.selectionCtx.Err() == nil
}
func (a *App) send() {
	text := strings.TrimSpace(a.draft)
	if text == "" || a.sending || !a.canMutate() {
		return
	}
	gen, selection, id, c, selectionCtx := a.generation, a.selection, a.selected, a.client, a.selectionCtx
	a.sending = true
	a.status = "Sending…"
	go func() {
		ctx, cancel := context.WithTimeout(selectionCtx, 30*time.Second)
		defer cancel()
		err := a.prompts.sendPrompt(ctx, c, id, text)
		a.selectionUpdate(gen, selection, func() {
			a.sending = false
			if errors.Is(err, client.ErrOutcomeUnknown) {
				a.status = "Message outcome unknown; sending again will retry the same message."
				return
			}
			if err != nil {
				a.status = "Send failed: " + err.Error()
				return
			}
			if strings.TrimSpace(a.draft) == text {
				a.draft = ""
			}
			a.status = "Message accepted"
		})
	}()
}
func (a *App) view(c *ui.Context) { a.shellView(c) }

func roleLabel(kind string) string {
	switch kind {
	case "user_message":
		return "You"
	case "assistant_message":
		return "Assistant"
	case "reasoning":
		return "Thinking"
	case "tool_call":
		return "Tool"
	default:
		return "Event"
	}
}

func (a *App) markdownOptions(id string) *markdown.Options {
	if options := a.markdown[id]; options != nil {
		return options
	}
	if a.markdown == nil {
		a.markdown = make(map[string]*markdown.Options)
	}
	options := &markdown.Options{}
	if a.diagrams != nil {
		options.Code = map[string]func(*ui.Context, *markdown.CodeBlockView){"mermaid": a.diagrams.Hook(id)}
	}
	a.markdown[id] = options
	return options
}
