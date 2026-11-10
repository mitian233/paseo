package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/client"
)

// homeProviderEntry is one provider row of the daemon's provider snapshot.
type homeProviderEntry struct {
	ID      string
	Label   string
	Status  string
	Source  string
	Error   string
	Models  int
	Enabled bool
	// Toggle is the switch binding; Enabled is the acknowledged value.
	Toggle bool
}

// homeProviderConfig is the part of a provider override this workflow edits.
// Other override fields stay in the daemon configuration untouched: saves send
// a patch with only the edited fields, which the daemon deep-merges.
type homeProviderConfig struct {
	Label   string
	Extends string
	Command []string
}

type homeProvidersState struct {
	open    bool
	host    string
	epoch   uint64
	loading bool
	err     string

	providers []homeProviderEntry
	config    map[string]homeProviderConfig

	selected          string
	diagnostic        string
	diagnosticErr     string
	diagnosticLoading bool
	diagnosticEpoch   uint64
	label             string
	binary            string

	toggling   string
	saving     bool
	refreshing string
	actionErr  string
	saved      string
	uncertain  string
}

var (
	errHomeProviderChanges = errors.New("No changes to save.")
	errHomeProviderCommand = errors.New("Enter at least a binary path.")
	errHomeProviderLabel   = errors.New("Enter a provider label.")
)

type homeProviderCaller interface {
	Call(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

func (s *homeProvidersState) owned(epoch uint64, host, activeHost string) bool {
	return s != nil && s.epoch == epoch && s.host == host && host == activeHost
}

func (s *homeProvidersState) mutationFailed(id string, err error) bool {
	s.toggling = ""
	s.saving = false
	s.actionErr = err.Error()
	if errors.Is(err, client.ErrOutcomeUnknown) {
		s.uncertain = id
		s.loading = true
		return true
	}
	syncHomeProviderToggles(s)
	return false
}

func (s *homeProvidersState) applyDiagnosticResult(id string, epoch uint64, text string, err error) {
	if s.selected != id || s.diagnosticEpoch != epoch {
		return
	}
	s.diagnosticLoading = false
	if err != nil {
		s.diagnosticErr = err.Error()
		return
	}
	s.diagnosticErr = ""
	s.diagnostic = text
}

func (s *homeProvidersState) index(id string) int {
	for i := range s.providers {
		if s.providers[i].ID == id {
			return i
		}
	}
	return -1
}

func (s *homeProvidersState) selectedEntry() (homeProviderEntry, bool) {
	i := s.index(s.selected)
	if i < 0 {
		return homeProviderEntry{}, false
	}
	return s.providers[i], true
}

func syncHomeProviderToggles(s *homeProvidersState) {
	for i := range s.providers {
		s.providers[i].Toggle = s.providers[i].Enabled
	}
}

func (p homeProviderEntry) statusLabel() string {
	if !p.Enabled {
		return "Disabled"
	}
	switch p.Status {
	case "loading":
		return "Loading"
	case "error":
		return "Error"
	case "ready":
		switch p.Models {
		case 0:
			return "Available"
		case 1:
			return "Available · 1 model"
		default:
			return fmt.Sprintf("Available · %d models", p.Models)
		}
	default:
		return "Not installed"
	}
}

func homeProviderStatusColor(p homeProviderEntry, theme *ui.Theme) ui.Color {
	if !p.Enabled || p.Status == "loading" {
		return theme.TextMuted
	}
	switch p.Status {
	case "error":
		return theme.Danger
	case "ready":
		return theme.Success
	default:
		return theme.Warning
	}
}

func loadHomeProviderSnapshot(ctx context.Context, caller homeProviderCaller) ([]homeProviderEntry, error) {
	raw, err := caller.Call(ctx, "get_providers_snapshot_request", json.RawMessage(`{}`))
	if err != nil {
		return nil, err
	}
	var response struct {
		Entries *[]struct {
			Provider string `json:"provider"`
			Status   string `json:"status"`
			Enabled  *bool  `json:"enabled"`
			Source   string `json:"source"`
			Error    string `json:"error"`
			Label    string `json:"label"`
			Models   []struct {
				Selectable *bool `json:"isSelectable"`
			} `json:"models"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("provider snapshot: %w", err)
	}
	if response.Entries == nil {
		return nil, errors.New("provider snapshot is missing entries")
	}
	entries := make([]homeProviderEntry, 0, len(*response.Entries))
	for _, entry := range *response.Entries {
		if entry.Provider == "" {
			return nil, errors.New("provider snapshot has an entry without a provider ID")
		}
		label := entry.Label
		if label == "" {
			label = entry.Provider
		}
		enabled := true
		if entry.Enabled != nil {
			enabled = *entry.Enabled
		}
		models := 0
		for _, model := range entry.Models {
			if model.Selectable == nil || *model.Selectable {
				models++
			}
		}
		entries = append(entries, homeProviderEntry{
			ID:      entry.Provider,
			Label:   label,
			Status:  entry.Status,
			Source:  entry.Source,
			Error:   entry.Error,
			Models:  models,
			Enabled: enabled,
			Toggle:  enabled,
		})
	}
	return entries, nil
}

func parseHomeProviderConfig(raw json.RawMessage) (map[string]homeProviderConfig, error) {
	var payload struct {
		Config *struct {
			Providers map[string]json.RawMessage `json:"providers"`
		} `json:"config"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("daemon configuration: %w", err)
	}
	if payload.Config == nil {
		return nil, errors.New("daemon configuration response is missing config")
	}
	configs := make(map[string]homeProviderConfig, len(payload.Config.Providers))
	for id, entry := range payload.Config.Providers {
		var fields struct {
			Label   string   `json:"label"`
			Extends string   `json:"extends"`
			Command []string `json:"command"`
		}
		if err := json.Unmarshal(entry, &fields); err != nil {
			return nil, fmt.Errorf("provider %s configuration: %w", id, err)
		}
		configs[id] = homeProviderConfig{Label: fields.Label, Extends: fields.Extends, Command: fields.Command}
	}
	return configs, nil
}

func loadHomeProviderConfig(ctx context.Context, caller homeProviderCaller) (map[string]homeProviderConfig, error) {
	raw, err := caller.Call(ctx, "get_daemon_config_request", json.RawMessage(`{}`))
	if err != nil {
		return nil, err
	}
	return parseHomeProviderConfig(raw)
}

func saveHomeProviderConfig(ctx context.Context, caller homeProviderCaller, request json.RawMessage) (map[string]homeProviderConfig, error) {
	raw, err := caller.Call(ctx, "set_daemon_config_request", request)
	if err != nil {
		return nil, err
	}
	config, err := parseHomeProviderConfig(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", client.ErrOutcomeUnknown, err)
	}
	return config, nil
}

func homeProviderConfigRequest(provider string, fields map[string]any) (json.RawMessage, error) {
	if provider == "" {
		return nil, errors.New("Choose a provider first.")
	}
	if len(fields) == 0 {
		return nil, errHomeProviderChanges
	}
	return json.Marshal(map[string]any{
		"config": map[string]any{
			"providers": map[string]any{provider: fields},
		},
	})
}

func homeProviderEnabledRequest(provider string, enabled bool) (json.RawMessage, error) {
	return homeProviderConfigRequest(provider, map[string]any{"enabled": enabled})
}

// refreshHomeProviderSnapshot makes the daemon probe the provider again and
// waits for its acknowledgement, so the next snapshot reports the status the
// new configuration actually produces instead of the cached one.
func refreshHomeProviderSnapshot(ctx context.Context, caller homeProviderCaller, provider string) error {
	request, err := json.Marshal(map[string]any{"providers": []string{provider}})
	if err != nil {
		return err
	}
	raw, err := caller.Call(ctx, "refresh_providers_snapshot_request", request)
	if err != nil {
		return err
	}
	var response struct {
		Acknowledged *bool `json:"acknowledged"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return fmt.Errorf("provider refresh: %w", err)
	}
	if response.Acknowledged == nil || !*response.Acknowledged {
		return errors.New("provider refresh was not acknowledged")
	}
	return nil
}

// Each line is an argv entry, including whitespace and empty arguments. Only
// the executable must be nonblank; trimming the other entries changes commands.
func parseHomeProviderCommand(text string) ([]string, error) {
	command := strings.Split(text, "\n")
	if strings.TrimSpace(command[0]) == "" {
		return nil, errHomeProviderCommand
	}
	return command, nil
}

// homeProviderSaveFields returns only the provider fields the user actually
// changed. Unchanged fields stay out of the patch so env, options, models and
// any newer daemon-side override survive the deep merge.
func homeProviderSaveFields(config homeProviderConfig, labelText, commandText string) (map[string]any, error) {
	fields := map[string]any{}
	if config.Extends != "" {
		if strings.TrimSpace(labelText) == "" {
			return nil, errHomeProviderLabel
		}
		if labelText != config.Label {
			fields["label"] = labelText
		}
	}
	if commandText != strings.Join(config.Command, "\n") && !(len(config.Command) == 0 && strings.TrimSpace(commandText) == "") {
		command, err := parseHomeProviderCommand(commandText)
		if err != nil {
			return nil, err
		}
		fields["command"] = command
	}
	if len(fields) == 0 {
		return nil, errHomeProviderChanges
	}
	return fields, nil
}

func loadHomeProviderDiagnostic(ctx context.Context, caller homeProviderCaller, provider string) (string, error) {
	request, err := json.Marshal(map[string]string{"provider": provider})
	if err != nil {
		return "", err
	}
	raw, err := caller.Call(ctx, "provider_diagnostic_request", request)
	if err != nil {
		return "", err
	}
	var response struct {
		Provider   string  `json:"provider"`
		Diagnostic *string `json:"diagnostic"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return "", fmt.Errorf("provider diagnostic: %w", err)
	}
	if response.Provider != "" && response.Provider != provider {
		return "", errors.New("provider diagnostic is for a different provider")
	}
	if response.Diagnostic == nil {
		return "", errors.New("provider diagnostic response is missing diagnostics")
	}
	return *response.Diagnostic, nil
}

// openHomeProviders opens the Setup providers workflow for the active host. A
// reconnect or host change makes older results stale through the operation
// epoch; closing also bumps it so in-flight requests cannot reopen state.
func (a *App) openHomeProviders() {
	s := &a.desk.homeProviders
	epoch := s.epoch + 1
	*s = homeProvidersState{open: true, host: a.desk.prefs.ActiveHost, epoch: epoch}
	if a.client == nil || !a.connected {
		s.err = "Connect to a host to configure providers."
		return
	}
	a.loadHomeProviders(epoch)
}

func (a *App) closeHomeProviders() {
	epoch := a.desk.homeProviders.epoch + 1
	a.desk.homeProviders = homeProvidersState{epoch: epoch}
}

func (a *App) loadHomeProviders(epoch uint64) {
	s := &a.desk.homeProviders
	daemon := a.client
	if daemon == nil || !a.connected {
		s.loading = false
		s.err = "Connect to a host to configure providers."
		return
	}
	host, gen, uncertain := s.host, a.generation, s.uncertain
	s.loading = true
	s.err = ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
		defer cancel()
		var err error
		if uncertain != "" {
			err = refreshHomeProviderSnapshot(ctx, daemon, uncertain)
		}
		var providers []homeProviderEntry
		var config map[string]homeProviderConfig
		if err == nil {
			providers, err = loadHomeProviderSnapshot(ctx, daemon)
		}
		if err == nil {
			config, err = loadHomeProviderConfig(ctx, daemon)
		}
		a.update(gen, func() {
			s := &a.desk.homeProviders
			if !s.owned(epoch, host, a.desk.prefs.ActiveHost) {
				return
			}
			s.loading = false
			if err != nil {
				s.err = err.Error()
				return
			}
			s.providers = providers
			s.config = config
			s.uncertain = ""
			if s.selected != "" {
				current := s.config[s.selected]
				s.label = current.Label
				s.binary = strings.Join(current.Command, "\n")
			}
			syncHomeProviderToggles(s)
			if s.selected != "" && s.index(s.selected) < 0 {
				s.selected = ""
				s.label = ""
				s.binary = ""
				s.diagnostic = ""
				s.diagnosticErr = ""
			}
			if uncertain != "" && s.selected != "" {
				a.loadHomeProviderDiagnostic(s.selected)
			}
		})
	}()
}

func (a *App) selectHomeProvider(id string) {
	s := &a.desk.homeProviders
	if s.index(id) < 0 {
		return
	}
	s.selected = id
	config := s.config[id]
	s.label = config.Label
	s.binary = strings.Join(config.Command, "\n")
	s.actionErr = ""
	s.saved = ""
	s.diagnostic = ""
	s.diagnosticErr = ""
	a.loadHomeProviderDiagnostic(id)
}

func (a *App) loadHomeProviderDiagnostic(id string) {
	s := &a.desk.homeProviders
	daemon := a.client
	if daemon == nil || !a.connected {
		s.diagnosticLoading = false
		s.diagnosticErr = "Connect to a host to load provider diagnostics."
		return
	}
	host, epoch, gen := s.host, s.epoch, a.generation
	s.diagnosticEpoch++
	diagnosticEpoch := s.diagnosticEpoch
	s.diagnosticLoading = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		text, err := loadHomeProviderDiagnostic(ctx, daemon, id)
		a.update(gen, func() {
			s := &a.desk.homeProviders
			if !s.owned(epoch, host, a.desk.prefs.ActiveHost) {
				return
			}
			s.applyDiagnosticResult(id, diagnosticEpoch, text, err)
		})
	}()
}

func (a *App) toggleHomeProvider(index int, enabled bool) {
	s := &a.desk.homeProviders
	if index < 0 || index >= len(s.providers) {
		return
	}
	entry := &s.providers[index]
	if entry.Enabled == enabled {
		entry.Toggle = enabled
		return
	}
	if s.toggling != "" || s.saving || s.refreshing != "" {
		entry.Toggle = entry.Enabled
		return
	}
	request, err := homeProviderEnabledRequest(entry.ID, enabled)
	if err != nil {
		entry.Toggle = entry.Enabled
		s.actionErr = err.Error()
		return
	}
	daemon := a.client
	if daemon == nil || !a.connected {
		entry.Toggle = entry.Enabled
		s.actionErr = "Connect to a host to change providers."
		return
	}
	host, epoch, gen := s.host, s.epoch, a.generation
	if host != a.desk.prefs.ActiveHost {
		entry.Toggle = entry.Enabled
		s.actionErr = "The active host changed. Reopen providers."
		return
	}
	id, label := entry.ID, entry.Label
	s.toggling = id
	s.actionErr = ""
	s.saved = ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		config, saveErr := saveHomeProviderConfig(ctx, daemon, request)
		cancel()
		if saveErr != nil {
			a.update(gen, func() {
				s := &a.desk.homeProviders
				if !s.owned(epoch, host, a.desk.prefs.ActiveHost) {
					return
				}
				if s.mutationFailed(id, saveErr) {
					a.loadHomeProviders(epoch)
				}
			})
			return
		}
		state := "disabled"
		if enabled {
			state = "enabled"
		}
		a.update(gen, func() {
			s := &a.desk.homeProviders
			if !s.owned(epoch, host, a.desk.prefs.ActiveHost) {
				return
			}
			s.toggling = ""
			s.config = config
			if i := s.index(id); i >= 0 {
				s.providers[i].Enabled = enabled
				s.providers[i].Toggle = enabled
			}
			s.saved = "Saved: " + label + " " + state
			s.actionErr = ""
			s.refreshing = id
		})

		probe, cancel := context.WithTimeout(context.Background(), 150*time.Second)
		refreshErr := refreshHomeProviderSnapshot(probe, daemon, id)
		var providers []homeProviderEntry
		if refreshErr == nil {
			providers, refreshErr = loadHomeProviderSnapshot(probe, daemon)
		}
		cancel()
		a.update(gen, func() {
			s := &a.desk.homeProviders
			if !s.owned(epoch, host, a.desk.prefs.ActiveHost) || s.refreshing != id {
				return
			}
			s.refreshing = ""
			if refreshErr != nil {
				// The daemon accepted the change; only the status refresh failed.
				s.actionErr = "Saved, but provider status could not be refreshed: " + refreshErr.Error()
				return
			}
			s.providers = providers
			syncHomeProviderToggles(s)
			if s.selected != "" && s.index(s.selected) < 0 {
				s.selected = ""
				s.label = ""
				s.binary = ""
				s.diagnostic = ""
				s.diagnosticErr = ""
			}
		})
	}()
}

func (a *App) saveHomeProviderChanges() {
	s := &a.desk.homeProviders
	entry, ok := s.selectedEntry()
	if !ok {
		s.actionErr = "Choose a provider first."
		return
	}
	if s.saving || s.toggling != "" || s.refreshing != "" {
		return
	}
	fields, err := homeProviderSaveFields(s.config[entry.ID], s.label, s.binary)
	if err != nil {
		s.actionErr = err.Error()
		return
	}
	request, err := homeProviderConfigRequest(entry.ID, fields)
	if err != nil {
		s.actionErr = err.Error()
		return
	}
	daemon := a.client
	if daemon == nil || !a.connected {
		s.actionErr = "Connect to a host to save provider settings."
		return
	}
	host, epoch, gen := s.host, s.epoch, a.generation
	if host != a.desk.prefs.ActiveHost {
		s.actionErr = "The active host changed. Reopen providers."
		return
	}
	id := entry.ID
	s.diagnosticEpoch++
	diagnosticEpoch := s.diagnosticEpoch
	s.diagnosticLoading = false
	s.saving = true
	s.actionErr = ""
	s.saved = ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		config, saveErr := saveHomeProviderConfig(ctx, daemon, request)
		cancel()
		if saveErr != nil {
			a.update(gen, func() {
				s := &a.desk.homeProviders
				if !s.owned(epoch, host, a.desk.prefs.ActiveHost) {
					return
				}
				if s.mutationFailed(id, saveErr) {
					a.loadHomeProviders(epoch)
				}
			})
			return
		}
		a.update(gen, func() {
			s := &a.desk.homeProviders
			if !s.owned(epoch, host, a.desk.prefs.ActiveHost) {
				return
			}
			s.saving = false
			s.config = config
			s.saved = "Provider settings saved"
			s.actionErr = ""
			s.refreshing = id
			if s.selected == id {
				config := s.config[id]
				s.label = config.Label
				s.binary = strings.Join(config.Command, "\n")
			}
		})

		// The diagnostic probe also refreshes this provider's snapshot entry,
		// so the next snapshot reflects the new command instead of the cache.
		probe, cancel := context.WithTimeout(context.Background(), 150*time.Second)
		diagnostic, diagnosticErr := loadHomeProviderDiagnostic(probe, daemon, id)
		providers, refreshErr := loadHomeProviderSnapshot(probe, daemon)
		cancel()
		a.update(gen, func() {
			s := &a.desk.homeProviders
			if !s.owned(epoch, host, a.desk.prefs.ActiveHost) || s.refreshing != id {
				return
			}
			s.refreshing = ""
			if refreshErr != nil {
				s.actionErr = "Saved, but provider status could not be refreshed: " + refreshErr.Error()
			} else {
				s.providers = providers
				syncHomeProviderToggles(s)
			}
			s.applyDiagnosticResult(id, diagnosticEpoch, diagnostic, diagnosticErr)
		})
	}()
}

// homeProvidersDialog renders the Setup providers modal. The parent owns the
// dialog list; the state lives in desk.homeProviders and is reset on host
// changes by the parent.
func (a *App) homeProvidersDialog(c *ui.Context) {
	s := &a.desk.homeProviders
	if !s.open {
		return
	}
	closeRequested := false
	modal := ui.Modal(c, &s.open, func() {
		ui.Column(c).Width(640).Padding(20).Gap(10).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, "Providers").Bold().Grow(1)
				if ui.Button(c, "Close").Clicked() {
					closeRequested = true
				}
			})
			if closeRequested {
				return
			}
			s := &a.desk.homeProviders
			if s.host != a.desk.prefs.ActiveHost {
				ui.Text(c, "The active host changed. Reopen providers to configure it.").TextColor(c.Theme().Warning)
				return
			}
			if s.loading {
				ui.Text(c, "Loading providers…").TextColor(c.Theme().TextMuted)
				return
			}
			if s.err != "" {
				ui.Text(c, s.err).TextColor(c.Theme().Danger)
				if ui.Button(c, "Retry").Clicked() {
					a.loadHomeProviders(s.epoch)
				}
				return
			}
			if len(s.providers) == 0 {
				ui.Text(c, "This host reported no providers.").TextColor(c.Theme().TextMuted)
				return
			}
			if s.selected != "" {
				a.homeProviderDetails(c)
				return
			}
			busy := s.toggling != "" || s.saving || s.refreshing != ""
			ui.Scroll(c).Height(240).Children(func() {
				ui.Column(c).Gap(6).Children(func() {
					for i := range s.providers {
						i := i
						entry := &s.providers[i]
						ui.Column(c).Gap(2).Children(func() {
							ui.Row(c).Gap(8).Children(func() {
								if ui.Button(c, entry.Label).Grow(1).Disabled(s.saving).Clicked() {
									a.selectHomeProvider(entry.ID)
								}
								ui.Text(c, entry.statusLabel()).TextColor(homeProviderStatusColor(*entry, c.Theme()))
								ui.Switch(c, &entry.Toggle).Disabled(busy).OnChange(func() {
									a.toggleHomeProvider(i, entry.Toggle)
								})
							})
							if entry.Enabled && entry.Status == "error" && strings.TrimSpace(entry.Error) != "" {
								ui.Text(c, strings.TrimSpace(entry.Error)).TextColor(c.Theme().Danger)
							}
							if s.toggling == entry.ID {
								ui.Text(c, "Saving…").TextColor(c.Theme().TextMuted)
							}
							if s.refreshing == entry.ID {
								ui.Text(c, "Refreshing provider status…").TextColor(c.Theme().TextMuted)
							}
						})
					}
				})
			})
			if s.actionErr != "" {
				ui.Text(c, s.actionErr).TextColor(c.Theme().Danger)
			}
			if s.saved != "" {
				ui.Text(c, s.saved).TextColor(c.Theme().Success)
			}
		})
	})
	if closeRequested || modal.Dismissed() || !a.desk.homeProviders.open {
		a.closeHomeProviders()
	}
}

func (a *App) closeHomeProviderDetails() {
	s := &a.desk.homeProviders
	s.selected = ""
	s.label = ""
	s.binary = ""
	s.diagnostic = ""
	s.diagnosticErr = ""
	s.diagnosticLoading = false
	s.actionErr = ""
	s.saved = ""
}

func (a *App) homeProviderDetails(c *ui.Context) {
	s := &a.desk.homeProviders
	entry, ok := s.selectedEntry()
	if !ok {
		return
	}
	config := s.config[entry.ID]
	busy := s.saving || s.refreshing != ""
	ui.Row(c).Gap(8).Children(func() {
		if ui.Button(c, "Back").Disabled(busy).Clicked() {
			a.closeHomeProviderDetails()
		}
		ui.Text(c, entry.Label).Bold().Grow(1)
	})
	if entry.Source != "" {
		ui.Text(c, "Source: "+entry.Source).TextColor(c.Theme().TextMuted)
	}
	if config.Extends != "" {
		ui.Text(c, "Profile extends "+config.Extends).TextColor(c.Theme().TextMuted)
		ui.TextInput(c, &s.label).Label("Profile label").Disabled(busy)
	}
	ui.TextArea(c, &s.binary).Label("Binary command (one argument per line)").MinHeight(56).MaxHeight(100).Disabled(busy)
	ui.Text(c, "Commands save to this host's configuration. Leaving the box empty keeps the current command.").TextColor(c.Theme().TextMuted)
	if s.refreshing == entry.ID {
		ui.Text(c, "Refreshing provider status…").TextColor(c.Theme().TextMuted)
	}
	if s.diagnosticLoading {
		ui.Text(c, "Loading provider diagnostics…").TextColor(c.Theme().TextMuted)
	} else if s.diagnosticErr != "" {
		ui.Text(c, s.diagnosticErr).TextColor(c.Theme().Danger)
	} else if s.diagnostic != "" {
		ui.Scroll(c).Height(96).Children(func() {
			ui.Text(c, s.diagnostic)
		})
	}
	if s.actionErr != "" {
		ui.Text(c, s.actionErr).TextColor(c.Theme().Danger)
	}
	if s.saved != "" {
		ui.Text(c, s.saved).TextColor(c.Theme().Success)
	}
	_, validationErr := homeProviderSaveFields(config, s.label, s.binary)
	ui.Row(c).Gap(8).Children(func() {
		ui.Spacer(c)
		if ui.PrimaryButton(c, "Save changes").Disabled(busy || s.toggling != "" || validationErr != nil).Clicked() {
			a.saveHomeProviderChanges()
		}
	})
	if validationErr != nil && !errors.Is(validationErr, errHomeProviderChanges) {
		ui.Text(c, validationErr.Error()).TextColor(c.Theme().Warning)
	}
}
