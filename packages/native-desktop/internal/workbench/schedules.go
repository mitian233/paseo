package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/client"
)

type schedulesState struct {
	open, loading, busy         bool
	epoch, revision, formEpoch  uint64
	cancel                      context.CancelFunc
	hosts                       []scheduleHost
	hostFilter, bucket, message string
	form                        *scheduleForm
	confirm                     *schedulePending
	pending                     *schedulePending
}

func (a *App) openSchedules() {
	a.showDestination("schedules")
	s := &a.desk.schedules
	if !s.open {
		s.open = true
		s.epoch++
		if s.bucket == "" {
			s.bucket = "runnable"
		}
	}
	a.loadSchedules()
}
func (a *App) closeSchedules() {
	s := &a.desk.schedules
	s.open = false
	s.busy = false
	s.epoch++
	s.revision++
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.form = nil
	s.confirm = nil
}
func loadScheduleHost(ctx context.Context, src navigationSource) scheduleHost {
	h := scheduleHost{ID: src.ID, Name: src.Name, Agents: map[string]Agent{}}
	if src.Err != nil {
		h.Error = src.Err.Error()
		return h
	}
	if src.Client == nil {
		h.Error = "Host is offline."
		return h
	}
	rows, err := scheduleList(ctx, src.Client)
	if err != nil {
		h.Error = err.Error()
		return h
	}
	h.Rows = rows
	var info struct {
		Features struct {
			WorkspaceMultiplicity bool `json:"workspaceMultiplicity"`
		} `json:"features"`
	}
	_ = json.Unmarshal(src.Info, &info)
	h.Multiplicity = info.Features.WorkspaceMultiplicity
	raw, err := src.Client.Call(ctx, "project.list.request", json.RawMessage(`{}`))
	if err == nil {
		var p struct {
			Projects *[]Project `json:"projects"`
			Error    string     `json:"error"`
		}
		err = json.Unmarshal(raw, &p)
		if err == nil && p.Projects != nil && p.Error == "" {
			h.Projects = *p.Projects
		} else if err == nil {
			err = errors.New("Projects are unavailable.")
		}
	}
	if err != nil {
		h.FormError = err.Error()
	}
	raw, err = src.Client.Call(ctx, "get_providers_snapshot_request", json.RawMessage(`{}`))
	if err == nil {
		var p struct {
			Entries *[]scheduleProvider `json:"entries"`
		}
		err = json.Unmarshal(raw, &p)
		if err == nil && p.Entries != nil {
			h.Providers = *p.Entries
		} else if err == nil {
			err = errors.New("Provider models are unavailable.")
		}
	}
	if err != nil {
		if h.FormError != "" {
			h.FormError += " · "
		}
		h.FormError += err.Error()
	}
	agents, err := loadPages(ctx, scheduleAgentCaller{src.Client}, "fetch_agents_request")
	if err == nil {
		h.AgentsLoaded = true
		for _, raw := range agents {
			var entry struct {
				Agent Agent `json:"agent"`
			}
			if json.Unmarshal(raw, &entry) != nil {
				h.AgentsLoaded = false
				break
			}
			if entry.Agent.ID != "" {
				h.Agents[entry.Agent.ID] = entry.Agent
			}
		}
	}
	return h
}
func (a *App) loadSchedules() {
	s := &a.desk.schedules
	if !s.open || s.busy {
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	s.cancel = cancel
	s.loading = true
	s.revision++
	epoch, revision, gen := s.epoch, s.revision, a.generation
	go func() {
		defer cancel()
		sources, cleanup := a.navigationSources(ctx)
		defer cleanup()
		hosts := make([]scheduleHost, len(sources))
		var wg sync.WaitGroup
		for i, src := range sources {
			wg.Add(1)
			go func(i int, src navigationSource) { defer wg.Done(); hosts[i] = loadScheduleHost(ctx, src) }(i, src)
		}
		wg.Wait()
		a.update(gen, func() {
			s := &a.desk.schedules
			if !s.open || s.epoch != epoch || s.revision != revision {
				return
			}
			s.loading = false
			s.hosts = hosts
			if s.pending != nil {
				for _, h := range hosts {
					if s.pending.reconciled(h) {
						s.pending = nil
						s.message = "Change confirmed by host."
						s.form = nil
						break
					}
				}
			}
			if s.form != nil {
				for _, h := range hosts {
					if h.ID == s.form.Host {
						applyScheduleFormHost(s.form, h)
						break
					}
				}
			}
		})
	}()
}
func applyScheduleFormHost(f *scheduleForm, h scheduleHost) {
	f.Multiplicity = h.Multiplicity
	f.IsGit = false
	for _, p := range h.Projects {
		if p.Root == f.CWD {
			f.IsGit = p.Kind == "git"
			if f.Project == "" || f.Project == f.CWD {
				f.Project = p.Name
			}
			break
		}
	}
}
func (a *App) openScheduleForm(host string, r *scheduleRecord) {
	s := &a.desk.schedules
	if s.busy || s.pending != nil {
		return
	}
	if host == "" {
		for _, h := range s.hosts {
			if h.Error == "" {
				host = h.ID
				break
			}
		}
	}
	s.formEpoch++
	s.form = newScheduleForm(host, r)
	for _, h := range s.hosts {
		if h.ID == host {
			applyScheduleFormHost(s.form, h)
			break
		}
	}
}
func (a *App) executeSchedule(p schedulePending) {
	s := &a.desk.schedules
	if s.busy || s.pending != nil {
		return
	}
	s.busy = true
	s.confirm = nil
	s.message = ""
	s.revision++
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.loading = false
	epoch, gen := s.epoch, a.generation
	for _, h := range s.hosts {
		if h.ID == p.Key.Host {
			for _, r := range h.Rows {
				if r.ID == p.Key.ID {
					p.LastRun = r.LastRunAt
				}
			}
		}
	}
	s.pending = &p
	// Mutations outlive page cancellation: cancelling a sent request cannot undo it.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		sources, cleanup := a.navigationSources(ctx)
		defer cleanup()
		var daemon caller
		for _, src := range sources {
			if src.ID == p.Key.Host && src.Err == nil {
				daemon = src.Client
				break
			}
		}
		var err error
		if daemon == nil {
			err = errors.New("Host is offline.")
		} else {
			_, err = scheduleMutation(ctx, daemon, p.Kind, p.Payload)
		}
		a.update(gen, func() {
			s := &a.desk.schedules
			if s.pending != &p {
				return
			}
			s.busy = false
			if errors.Is(err, client.ErrOutcomeUnknown) {
				s.message = "The host may have applied this change. Refresh to read its result before making another change."
			} else if err != nil {
				s.pending = nil
				s.message = err.Error()
				if s.form != nil && s.epoch == epoch {
					s.form.Error = err.Error()
				}
			} else {
				s.pending = nil
				s.message = "Saved."
				if s.epoch == epoch {
					s.form = nil
				}
			}
			if s.open {
				a.loadSchedules()
			}
		})
	}()
}
func (a *App) submitScheduleForm() {
	s := &a.desk.schedules
	if s.form == nil {
		return
	}
	kind, payload, err := s.form.payload()
	if err != nil {
		s.form.Error = err.Error()
		return
	}
	p := schedulePending{Kind: kind, Key: scheduleKey{Host: s.form.Host}, Payload: payload, Before: map[string]bool{}}
	if s.form.Original != nil {
		p.Key.ID = s.form.Original.ID
	}
	for _, h := range s.hosts {
		if h.ID == p.Key.Host {
			if err := s.form.selectionError(h); err != nil {
				s.form.Error = err.Error()
				return
			}
			for _, r := range h.Rows {
				p.Before[r.ID] = true
			}
		}
	}
	a.executeSchedule(p)
}
func (a *App) schedulesView(c *ui.Context) {
	s := &a.desk.schedules
	ui.Column(c).Grow(1).Padding(24).Gap(12).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c, "Navigation").Clicked() {
				a.toggleNavigation()
			}
			ui.Text(c, "Schedules").Bold()
			ui.Spacer(c)
			if ui.Button(c, "Refresh").Disabled(s.loading || s.busy).Clicked() {
				a.loadSchedules()
			}
			if ui.PrimaryButton(c, "New schedule").Disabled(s.loading || s.busy || s.pending != nil || len(s.hosts) == 0).Clicked() {
				a.openScheduleForm(s.hostFilter, nil)
			}
		})
		ui.Row(c).Gap(8).Children(func() {
			for _, b := range []struct{ value, label string }{{"runnable", "Active"}, {"ended", "Ended"}} {
				if ui.Button(c, b.label).Clicked() {
					s.bucket = b.value
				}
			}
			label := "All hosts"
			for _, h := range s.hosts {
				if h.ID == s.hostFilter {
					label = h.Name
				}
			}
			ui.PopoverMenuButton(c, label, func(m *ui.Menu) {
				if m.Item("All hosts").Chosen() {
					s.hostFilter = ""
				}
				for _, h := range s.hosts {
					if m.Item(h.Name).Chosen() {
						s.hostFilter = h.ID
					}
				}
			})
		})
		if s.message != "" {
			ui.Text(c, s.message).TextColor(c.Theme().TextMuted)
		}
		if s.loading {
			ui.Text(c, "Loading schedules…")
		}
		for _, h := range s.hosts {
			if h.Error != "" {
				ui.Text(c, h.Name+": "+h.Error).TextColor(c.Theme().Danger)
			}
		}
		type row struct {
			host   scheduleHost
			record scheduleRecord
		}
		var rows []row
		for _, h := range s.hosts {
			if s.hostFilter != "" && s.hostFilter != h.ID {
				continue
			}
			for _, r := range h.Rows {
				state := r.derived(h, time.Now())
				active := state == "active" || state == "paused"
				if (s.bucket == "runnable") == active {
					rows = append(rows, row{h, r})
				}
			}
		}
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].record.CreatedAt > rows[j].record.CreatedAt })
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(1).Children(func() {
				if !s.loading && len(rows) == 0 {
					text := "No schedules yet"
					if s.bucket == "ended" {
						text = "No ended schedules"
					}
					for _, h := range s.hosts {
						if h.Error != "" {
							text = "No schedules available for this filter"
							break
						}
					}
					ui.Text(c, text).TextColor(c.Theme().TextMuted)
				}
				for _, item := range rows {
					h, r := item.host, item.record
					state := r.derived(h, time.Now())
					ui.Column(c.Key(fmt.Sprintf("schedule:%q:%q", h.ID, r.ID))).Padding(12).Gap(6).Children(func() {
						ui.Row(c).Gap(8).Children(func() {
							ui.Text(c, r.title()).Bold()
							ui.Text(c, state).TextColor(c.Theme().TextMuted)
							ui.Spacer(c)
							if len(s.hosts) > 1 {
								ui.Text(c, h.Name).TextColor(c.Theme().TextMuted)
							}
						})
						target := r.Target.Config.CWD
						if r.Target.Type == "agent" {
							target = "Agent unavailable"
							if agent, ok := h.Agents[r.Target.AgentID]; ok {
								target = agent.Title
								if target == "" {
									target = "Untitled agent"
								}
							}
						} else {
							for _, p := range h.Projects {
								if p.Root == target {
									target = p.Name
									break
								}
							}
						}
						cadence := r.Cadence.Expression
						if r.Cadence.Type == "every" {
							cadence = fmt.Sprintf("Every %s", time.Duration(r.Cadence.EveryMS)*time.Millisecond)
						} else if r.Cadence.Timezone != "" {
							cadence += " · " + r.Cadence.Timezone
						}
						ui.Text(c, target+" · "+cadence).TextColor(c.Theme().TextMuted)
						if r.NextRunAt != "" {
							ui.Text(c, "Next run: "+r.NextRunAt).TextColor(c.Theme().TextMuted)
						}
						ui.Row(c).Gap(8).Disabled(s.busy || s.pending != nil).Children(func() {
							if ui.Button(c, "Edit").Clicked() {
								a.openScheduleForm(h.ID, &r)
							}
							if state == "active" || state == "paused" {
								label, kind := "Pause", "schedule/pause"
								if state == "paused" {
									label, kind = "Resume", "schedule/resume"
								}
								if ui.Button(c, label).Clicked() {
									a.executeSchedule(schedulePending{Kind: kind, Key: scheduleKey{h.ID, r.ID}, Payload: map[string]any{"scheduleId": r.ID}})
								}
								if ui.Button(c, "Run now").Clicked() {
									a.executeSchedule(schedulePending{Kind: "schedule/run-once", Key: scheduleKey{h.ID, r.ID}, Payload: map[string]any{"scheduleId": r.ID}})
								}
							}
							if ui.Button(c, "Delete").Clicked() {
								s.confirm = &schedulePending{Kind: "schedule/delete", Key: scheduleKey{h.ID, r.ID}, Payload: map[string]any{"scheduleId": r.ID}}
							}
						})
					})
				}
			})
		})
	})
	a.scheduleFormView(c)
	confirm := s.confirm != nil
	ui.Modal(c, &confirm, func() {
		ui.Column(c).Width(420).Padding(24).Gap(12).Children(func() {
			ui.Text(c, "Delete schedule?").Bold()
			ui.Text(c, "This cannot be undone.")
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					s.confirm = nil
				}
				if ui.Button(c, "Delete").Clicked() && s.confirm != nil {
					a.executeSchedule(*s.confirm)
				}
			})
		})
	})
	if !confirm {
		s.confirm = nil
	}
}

func scheduleOptionLabel(options []scheduleOption, id string) string {
	for _, o := range options {
		if o.ID == id {
			return o.Label
		}
	}
	if id == "" {
		return "Default"
	}
	return id
}
func scheduleSelect(c *ui.Context, label string, value *string, options []scheduleOption) {
	ui.Text(c, label).TextColor(c.Theme().TextMuted)
	ui.PopoverMenuButton(c, scheduleOptionLabel(options, *value), func(m *ui.Menu) {
		if m.Item("Default").Chosen() {
			*value = ""
		}
		for _, o := range options {
			if m.Item(o.Label).Chosen() {
				*value = o.ID
			}
		}
	})
}
func (a *App) scheduleFormView(c *ui.Context) {
	s := &a.desk.schedules
	f := s.form
	if f == nil {
		return
	}
	open := true
	c = c.Key(fmt.Sprintf("schedule-form:%d", s.formEpoch))
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(560).MaxHeight(760).Padding(24).Gap(12).Children(func() {
			title := "New schedule"
			if f.Original != nil {
				title = "Edit schedule"
				if f.Original.Target.Type == "agent" {
					title = "Edit heartbeat"
				}
			}
			ui.Text(c, title).Bold()
			ui.Scroll(c).Height(500).Children(func() {
				ui.Column(c).Gap(10).Disabled(s.busy || s.pending != nil).Children(func() {
					heartbeat := f.Original != nil && f.Original.Target.Type == "agent"
					if heartbeat {
						label := "Agent unavailable"
						for _, h := range s.hosts {
							if h.ID == f.Host {
								if agent, ok := h.Agents[f.Original.Target.AgentID]; ok {
									label = agent.Title
									if label == "" {
										label = "Untitled agent"
									}
								}
							}
						}
						ui.Text(c, "Agent: "+label)
					} else {
						ui.TextInput(c, &f.Name).Label("Name").Placeholder("Optional")
						ui.TextArea(c, &f.Prompt).Label("Prompt").Placeholder("What should the agent do each run?").MinHeight(80)
						var host scheduleHost
						for _, h := range s.hosts {
							if h.ID == f.Host {
								host = h
							}
						}
						ui.Column(c).Disabled(f.Original != nil).Children(func() {
							ui.Text(c, "Host").TextColor(c.Theme().TextMuted)
							label := host.Name
							if label == "" {
								label = "Select host"
							}
							ui.PopoverMenuButton(c, label, func(m *ui.Menu) {
								for _, h := range s.hosts {
									if m.Item(h.Name).Chosen() && f.Host != h.ID {
										f.Host = h.ID
										f.CWD = ""
										f.Project = ""
										f.Provider = ""
										f.Model = ""
										f.Mode = ""
										f.Thinking = ""
										applyScheduleFormHost(f, h)
									}
								}
							})
						})
						if host.Error != "" {
							ui.Text(c, host.Error).TextColor(c.Theme().Danger)
						}
						if host.FormError != "" {
							ui.Text(c, host.FormError).TextColor(c.Theme().Danger)
							if ui.Button(c, "Reload options").Clicked() {
								a.loadSchedules()
							}
						}
						if f.Host != "" {
							ui.Text(c, "Project").TextColor(c.Theme().TextMuted)
							label := f.Project
							if label == "" {
								label = "Select project"
							}
							ui.PopoverMenuButton(c, label, func(m *ui.Menu) {
								for _, p := range host.Projects {
									if m.Item(p.Name).Chosen() {
										f.CWD = p.Root
										f.Project = p.Name
										f.IsGit = p.Kind == "git"
										if !f.IsGit {
											f.Isolation = "local"
										}
									}
								}
							})
						}
						if f.CWD != "" {
							ui.Text(c, "Model").TextColor(c.Theme().TextMuted)
							label := strings.TrimSpace(f.Provider + " / " + f.Model)
							if f.Provider == "" {
								label = "Select model"
							}
							ui.PopoverMenuButton(c, label, func(m *ui.Menu) {
								for _, p := range host.Providers {
									if p.Enabled != nil && !*p.Enabled || p.Status != "ready" {
										continue
									}
									provider := p.Label
									if provider == "" {
										provider = p.ID
									}
									if m.Item(provider + " / Default").Chosen() {
										f.Provider = p.ID
										f.Model = ""
										f.Mode = ""
										f.Thinking = ""
									}
									for _, model := range p.Models {
										if model.Selectable != nil && !*model.Selectable {
											continue
										}
										name := model.Label
										if name == "" {
											name = model.ID
										}
										if m.Item(provider + " / " + name).Chosen() {
											f.Provider = p.ID
											f.Model = model.ID
											f.Mode = ""
											f.Thinking = ""
										}
									}
								}
							})
							for _, p := range host.Providers {
								if p.ID == f.Provider {
									if len(p.Modes) > 0 {
										scheduleSelect(c, "Mode", &f.Mode, p.Modes)
									}
									for _, model := range p.Models {
										if model.ID == f.Model && len(model.Thinking) > 0 {
											scheduleSelect(c, "Thinking", &f.Thinking, model.Thinking)
										}
									}
								}
							}
							if f.Multiplicity && f.IsGit {
								scheduleSelect(c, "Isolation", &f.Isolation, []scheduleOption{{ID: "local", Label: "Local"}, {ID: "worktree", Label: "Worktree"}})
								if f.Isolation == "" {
									f.Isolation = "local"
								}
							}
							if f.Multiplicity {
								ui.Checkbox(c, &f.Archive, "Archive on finish")
							}
						}
					}
					ui.Text(c, "Cadence").TextColor(c.Theme().TextMuted)
					ui.PopoverMenuButton(c, "Choose cadence", func(m *ui.Menu) {
						for _, option := range []struct{ label, expression string }{{"Every minute", "* * * * *"}, {"Every hour", "0 * * * *"}, {"Daily 9:00", "0 9 * * *"}, {"Weekdays 9:00", "0 9 * * 1-5"}, {"Mondays 9:00", "0 9 * * 1"}} {
							if m.Item(option.label).Chosen() {
								f.Expression = option.expression
							}
						}
					})
					ui.TextInput(c, &f.Expression).Label("Cron").Placeholder("0 9 * * 1")
					ui.TextInput(c, &f.Timezone).Label("Timezone").Placeholder("UTC")
					if f.Original != nil && f.Original.Cadence.Type == "every" && f.Expression == "" {
						ui.Text(c, fmt.Sprintf("Current interval: %s. Enter cron to replace it.", time.Duration(f.Original.Cadence.EveryMS)*time.Millisecond)).TextColor(c.Theme().TextMuted)
					}
					if !heartbeat {
						ui.TextInput(c, &f.MaxRuns).Label("Max runs").Placeholder("Unlimited")
					}
				})
			})
			if f.Error != "" {
				ui.Text(c, f.Error).TextColor(c.Theme().Danger)
			}
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c, "Cancel").Disabled(s.busy).Clicked() {
					s.form = nil
				}
				label := "Create schedule"
				if f.Original != nil {
					label = "Save changes"
				}
				_, _, err := f.payload()
				for _, h := range s.hosts {
					if h.ID == f.Host && err == nil {
						err = f.selectionError(h)
					}
				}
				if ui.PrimaryButton(c, label).Disabled(s.busy || s.pending != nil || err != nil).Clicked() {
					a.submitScheduleForm()
				}
			})
		})
	})
	if !open && !s.busy {
		s.form = nil
	}
}
