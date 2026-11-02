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
}

func New(url string) *App { return &App{URL: url} }
func (a *App) Open() {
	a.win = mygo.NewWindow(mygo.WindowOptions{Title: "Paseo", Width: 1100, Height: 760, MinWidth: 700, MinHeight: 480, Content: ui.View(a.view)})
	a.diagrams = NewDiagrams(a.win.Update)
	a.win.OnClosed(func() { a.stop(); a.diagrams.Close() })
	if a.URL != "" {
		a.connect()
	}
}
func (a *App) stop() {
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
				return
			}
			a.workspaces = ws
			a.agents = agents
			a.status = fmt.Sprintf("%d workspaces · %d agents", len(ws), len(agents))
		})
	}()
}
func (a *App) selectAgent(id string) {
	if a.cancelSelection != nil {
		a.cancelSelection()
	}
	a.selection++
	selection := a.selection
	gen := a.generation
	a.selected = id
	a.clearContent()
	a.sending = false
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
				for _, message := range pending {
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
func (a *App) view(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Padding(12).Gap(8).Children(func() {
		ui.Text(c, "Paseo").Bold().FontSize(20)
		ui.Row(c).Gap(8).Children(func() {
			ui.TextInput(c, &a.URL).Placeholder("ws://host:port/ws").Label("Daemon URL").Grow(1)
			ui.TextInput(c, &a.Password).Placeholder("Password (optional)").Label("Password").Password().Width(180)
			ui.TextInput(c, &a.Credential).Placeholder("Local credential (optional)").Label("Local credential").Password().Width(180)
			if ui.PrimaryButton(c, "Connect").Disabled(a.connecting).Clicked() {
				a.connect()
			}
		})
		if a.host != "" {
			ui.Text(c, "Host: "+a.host).Bold()
		}
		if a.status != "" {
			ui.Text(c, a.status).TextColor(t.TextMuted)
		}
		ui.Row(c).Grow(1).Gap(12).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Width(260).Gap(6).Children(func() {
				ui.Text(c, "Workspaces & agents").Bold()
				ui.Scroll(c).Grow(1).Children(func() {
					ui.Column(c).Gap(4).Children(func() {
						for _, w := range a.workspaces {
							ui.Text(c, w.Project+" / "+w.Name).Bold()
							for _, agent := range a.agents {
								if agent.WorkspaceID == w.ID {
									a.agentButton(c, agent)
								}
							}
						}
						for _, agent := range a.agents {
							found := false
							for _, w := range a.workspaces {
								if agent.WorkspaceID == w.ID {
									found = true
									break
								}
							}
							if !found {
								a.agentButton(c, agent)
							}
						}
					})
				})
			})
			ui.Column(c).Grow(1).Gap(8).Children(func() {
				if a.selected == "" {
					ui.Text(c, "Select an agent to view its conversation.").TextColor(t.TextMuted)
				}
				a.list.Key = func(i int) any { return a.items[i].ID }
				a.list.FollowEnd = true
				ui.List(c, &a.list, len(a.items), func(i int) {
					item := a.items[i]
					ui.Column(c).Gap(4).Children(func() {
						ui.Text(c, roleLabel(item.Kind)).Bold()
						markdown.View(c, item.Text, a.markdownOptions(item.ID))
					})
				}).Grow(1).Gap(12)
				if len(a.permissions.cards) > 0 {
					ui.Scroll(c).MaxHeight(260).Disabled(!a.canMutate()).Children(func() {
						a.permissions.view(c, func(id string, response permissionResponse) {
							if !a.canMutate() {
								return
							}
							gen, selection := a.generation, a.selection
							a.permissions.submit(a.selectionCtx, a.client, a.selected, id, response, func(fn func()) { a.selectionUpdate(gen, selection, fn) })
						})
					})
				}
				ui.Row(c).Gap(8).Children(func() {
					in := ui.TextInput(c, &a.draft).Placeholder("Message agent").Label("Message agent").Grow(1).Disabled(!a.canMutate())
					if ui.PrimaryButton(c, "Send").Disabled(!a.canMutate() || a.sending || strings.TrimSpace(a.draft) == "").Clicked() || in.Submitted() {
						a.send()
					}
				})
			})
		})
	})
}
func (a *App) agentButton(c *ui.Context, agent Agent) {
	title := agent.Title
	if title == "" {
		title = agent.ID
	}
	if ui.Button(c.Key(agent.ID), title).Disabled(!a.connected).Clicked() {
		a.selectAgent(agent.ID)
	}
}

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
