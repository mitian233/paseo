package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/client"
)

// The daemon slices sessions after dropping already imported ones, so paging
// grows the requested limit instead of carrying an offset. 200 is the
// protocol ceiling.
const (
	homeImportPageSize       = 15
	homeImportMaxLimit       = 200
	homeImportSearchDebounce = 200 * time.Millisecond
)

var homeImportPageLimits = [...]int{homeImportPageSize, 45, 90, homeImportMaxLimit}

const homeImportUnknownOutcome = "Import outcome unknown. The directory was refreshed; check the agent list before importing this session again."

// homeImportEntry mirrors RecentProviderSessionDescriptorPayloadSchema. IDs
// are kept exactly as the daemon reported them; the dialog never derives a
// provider session identity of its own.
type homeImportEntry struct {
	ProviderID         string `json:"providerId"`
	ProviderLabel      string `json:"providerLabel"`
	ProviderHandleID   string `json:"providerHandleId"`
	CWD                string `json:"cwd"`
	Title              string `json:"title"`
	FirstPromptPreview string `json:"firstPromptPreview"`
	LastPromptPreview  string `json:"lastPromptPreview"`
	LastActivityAt     string `json:"lastActivityAt"`
}

func (e homeImportEntry) key() string { return e.ProviderID + ":" + e.ProviderHandleID }

type homeImportProviderError struct {
	Provider string `json:"provider"`
	Message  string `json:"message"`
}

// homeImportState is the host-wide session import dialog. Its zero value is
// closed and idle. The parent clears it when the host changes; open and close
// always bump epoch, and every listing request bumps loadEpoch, so a response
// from a closed or reopened dialog can never populate the current one.
type homeImportState struct {
	open      bool
	epoch     uint64
	loadEpoch uint64
	loading   bool
	loaded    bool

	search      string
	query       string
	filter      string
	searchTimer *time.Timer
	limit       int

	entries         []homeImportEntry
	providerErrors  []homeImportProviderError
	alreadyImported int
	labels          map[string]string
	filters         []string
	loadErr         string

	importing string
	importErr string
	blocked   map[string]bool
}

type homeImportListing struct {
	entries         []homeImportEntry
	alreadyImported int
	providerErrors  []homeImportProviderError
}

// homeImportResult is the classified outcome of import_agent_request. failure
// is a definite daemon rejection that may be retried; unknown marks a mutation
// whose result cannot be trusted, so the same session is never resent blindly.
type homeImportResult struct {
	agentID     string
	workspaceID string
	cwd         string
	failure     string
	unknown     bool
}

// openHomeImport opens the dialog from the home screen. The home entry is
// host-wide, so no cwd or workspace is imposed: the listing decides where each
// session belongs, exactly as the original home import entry does.
func (a *App) openHomeImport() {
	h := &a.desk.homeImport
	if h.searchTimer != nil {
		h.searchTimer.Stop()
		h.searchTimer = nil
	}
	h.epoch++
	h.loadEpoch++
	h.open = true
	h.loading = false
	h.loaded = false
	h.search = ""
	h.query = ""
	h.filter = ""
	h.limit = homeImportPageSize
	h.entries = nil
	h.providerErrors = nil
	h.alreadyImported = 0
	h.labels = nil
	h.filters = nil
	h.loadErr = ""
	h.importing = ""
	h.importErr = ""
	if a.client == nil || !a.connected {
		h.loadErr = "Connect to a host to import sessions."
		return
	}
	a.loadHomeImportSessions()
}

// closeHomeImport dismisses the dialog and invalidates every answer still in
// flight. blocked outcomes survive: they describe mutations, not the dialog.
func (a *App) closeHomeImport() {
	h := &a.desk.homeImport
	if h.searchTimer != nil {
		h.searchTimer.Stop()
		h.searchTimer = nil
	}
	h.open = false
	h.epoch++
	h.loadEpoch++
	h.loading = false
	h.loaded = false
	h.search = ""
	h.query = ""
	h.filter = ""
	h.limit = homeImportPageSize
	h.entries = nil
	h.providerErrors = nil
	h.alreadyImported = 0
	h.labels = nil
	h.filters = nil
	h.loadErr = ""
	h.importing = ""
	h.importErr = ""
}

func (a *App) homeImportSearchChanged() {
	h := &a.desk.homeImport
	if h.searchTimer != nil {
		h.searchTimer.Stop()
	}
	gen, epoch := a.generation, h.epoch
	h.searchTimer = time.AfterFunc(homeImportSearchDebounce, func() {
		if a.win == nil {
			return
		}
		a.update(gen, func() {
			h := &a.desk.homeImport
			if !h.open || h.epoch != epoch {
				return
			}
			a.commitHomeImportSearch()
		})
	})
}

func (a *App) commitHomeImportSearch() {
	h := &a.desk.homeImport
	if h.searchTimer != nil {
		h.searchTimer.Stop()
		h.searchTimer = nil
	}
	query := strings.TrimSpace(h.search)
	if query == h.query {
		return
	}
	h.query = query
	h.limit = homeImportPageSize
	a.loadHomeImportSessions()
}

func (a *App) refreshHomeImport() {
	h := &a.desk.homeImport
	if !h.open || h.loading || h.importing != "" {
		return
	}
	a.loadHomeImportSessions()
}

func (a *App) loadHomeImportSessions() {
	h := &a.desk.homeImport
	if !h.open {
		return
	}
	if a.client == nil || !a.connected {
		h.loading = false
		h.loadErr = "Connect to a host to import sessions."
		return
	}
	if h.limit <= 0 {
		h.limit = homeImportPageSize
	}
	h.loadEpoch++
	epoch, loadEpoch := h.epoch, h.loadEpoch
	h.loading = true
	h.loadErr = ""
	c, gen := a.client, a.generation
	limit, query := h.limit, h.query
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		listing, err := fetchHomeImportSessions(ctx, c, limit, query, nil)
		a.update(gen, func() {
			h := &a.desk.homeImport
			if !h.open || h.epoch != epoch || h.loadEpoch != loadEpoch {
				return
			}
			h.loading = false
			if err != nil {
				h.loadErr = "Sessions could not be loaded: " + err.Error()
				return
			}
			h.applyListing(listing)
		})
	}()
}

// retryHomeImportProvider refetches one provider after the daemon reported it
// as failed, leaving the other providers' rows untouched.
func (a *App) retryHomeImportProvider(provider string) {
	h := &a.desk.homeImport
	if !h.open || h.loading || h.importing != "" || provider == "" {
		return
	}
	if a.client == nil || !a.connected {
		h.loadErr = "Connect to a host to import sessions."
		return
	}
	h.loadEpoch++
	epoch, loadEpoch := h.epoch, h.loadEpoch
	h.loading = true
	h.loadErr = ""
	c, gen := a.client, a.generation
	limit, query := h.limit, h.query
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		listing, err := fetchHomeImportSessions(ctx, c, limit, query, []string{provider})
		a.update(gen, func() {
			h := &a.desk.homeImport
			if !h.open || h.epoch != epoch || h.loadEpoch != loadEpoch {
				return
			}
			h.loading = false
			if err != nil {
				h.setProviderError(provider, err.Error())
				return
			}
			h.mergeProviderListing(provider, listing)
		})
	}()
}

// importHomeSession imports the listed provider session into the workspace the
// daemon derives from the session's own cwd. Home has no workspace scope, so
// no workspaceId is invented here.
func (a *App) importHomeSession(entry homeImportEntry) {
	h := &a.desk.homeImport
	if h.importing != "" {
		return
	}
	if a.client == nil || !a.connected {
		h.importErr = "Connect to a host to import this session."
		return
	}
	key := entry.key()
	if h.blocked[key] {
		h.importErr = homeImportUnknownOutcome
		return
	}
	if _, err := homeImportImportPayload(entry); err != nil {
		h.importErr = err.Error()
		return
	}
	h.importing = key
	h.importErr = ""
	epoch := h.epoch
	c, gen := a.client, a.generation
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		result, err := importHomeSessionRequest(ctx, c, entry)
		if err != nil {
			unknown := errors.Is(err, client.ErrOutcomeUnknown)
			a.update(gen, func() {
				h := &a.desk.homeImport
				if unknown {
					h.block(key)
					if h.epoch == epoch {
						h.importing = ""
						h.importErr = homeImportUnknownOutcome
						a.loadHomeImportSessions()
					}
					a.refreshProjects()
					a.refreshDirectory()
					return
				}
				if h.epoch != epoch {
					return
				}
				h.importing = ""
				h.importErr = "Import failed: " + err.Error()
			})
			return
		}
		if result.failure != "" {
			a.update(gen, func() {
				h := &a.desk.homeImport
				if h.epoch != epoch {
					return
				}
				h.importing = ""
				h.importErr = "Import failed: " + result.failure
			})
			return
		}
		if result.unknown {
			a.update(gen, func() {
				h := &a.desk.homeImport
				h.block(key)
				if h.epoch == epoch {
					h.importing = ""
					h.importErr = homeImportUnknownOutcome
					a.loadHomeImportSessions()
				}
				a.refreshProjects()
				a.refreshDirectory()
			})
			return
		}
		a.update(gen, func() {
			h := &a.desk.homeImport
			if h.epoch == epoch {
				h.importing = ""
				h.importErr = ""
				h.open = false
				delete(h.blocked, key)
			}
			a.status = "Imported " + homeImportTitle(entry)
			a.refreshProjects()
		})
		a.openImportedHomeAgent(c, gen, result)
	}()
}

// openImportedHomeAgent reloads the authoritative directory, then opens the
// imported agent in its own workspace. It never guesses a workspace: if the
// agent or its workspace has not reached the directory yet, the import is
// reported without opening a tab in the wrong workspace.
func (a *App) openImportedHomeAgent(c *client.Client, gen uint64, result homeImportResult) {
	if c == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		workspaces, agents, err := loadDirectory(ctx, c)
		a.update(gen, func() {
			if err != nil {
				a.status = "Imported, but the directory could not be refreshed: " + err.Error()
				return
			}
			a.applyImportedHomeDirectory(workspaces, agents, result)
		})
	}()
}

func (a *App) applyImportedHomeDirectory(workspaces []Workspace, agents []Agent, result homeImportResult) {
	a.workspaces = workspaces
	a.agents = agents
	agent, ok := homeImportPlacement(agents, workspaces, result.agentID, result.workspaceID)
	if !ok {
		a.status = "Imported; the agent is not in the directory yet. Refresh the directory to open it."
		return
	}
	a.selectWorkspace(agent.WorkspaceID)
	a.openAgent(agent.ID)
	a.status = "Imported " + homeImportAgentLabel(agent)
}

func (a *App) homeImportDialog(c *ui.Context) {
	h := &a.desk.homeImport
	open := h.open
	ui.Modal(c, &open, func() {
		theme := c.Theme()
		ui.Column(c).Width(560).MaxHeight(600).Padding(20).Gap(10).Children(func() {
			ui.Row(c).WidthPercent(100).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, "Import session").Bold().FontSize(16)
					ui.Text(c, a.homeImportScopeLabel()).FontSize(12).TextColor(theme.TextMuted)
				})
				if ui.Button(c, "Refresh").Disabled(h.loading || h.importing != "" || !a.connected).Clicked() {
					a.refreshHomeImport()
				}
			})
			ui.TextInput(c, &h.search).Placeholder("Search sessions").
				Disabled(h.importing != "").
				OnChange(func() { a.homeImportSearchChanged() }).
				OnSubmit(func() { a.commitHomeImportSearch() })
			if len(h.filters) > 1 {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "Provider").FontSize(12).TextColor(theme.TextMuted)
					ui.PopoverMenuButton(c, h.filterLabel(), func(m *ui.Menu) {
						if m.Item("All providers").Radio(h.filter == "").Chosen() {
							h.filter = ""
						}
						for _, option := range h.filterOptions() {
							if m.Item(option.label).Radio(h.filter == option.id).Chosen() {
								h.filter = option.id
							}
						}
					}).Disabled(h.importing != "")
				})
			}
			a.homeImportStatus(c)
			visible := h.visibleEntries()
			height := float32(80)
			if len(visible) > 0 {
				height = min(360, float32(len(visible))*64+8)
			}
			ui.Scroll(c).Height(height).WidthPercent(100).Children(func() {
				ui.Column(c).WidthPercent(100).Gap(4).Children(func() {
					for _, entry := range visible {
						a.homeImportRow(c, entry)
					}
				})
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if h.hasMore() {
					if ui.Button(c, "Load more").Disabled(h.loading || h.importing != "").Clicked() {
						h.limit = nextHomeImportLimit(h.limit)
						a.loadHomeImportSessions()
					}
				}
				ui.Text(c, "").Grow(1)
				if ui.Button(c, "Close").Clicked() {
					a.closeHomeImport()
				}
			})
		})
	})
	if h.open && !open {
		a.closeHomeImport()
	}
}

func (a *App) homeImportScopeLabel() string {
	if a.host == "" {
		return "Recent sessions on this host"
	}
	return "Recent sessions on " + a.host
}

func (a *App) homeImportStatus(c *ui.Context) {
	h := &a.desk.homeImport
	theme := c.Theme()
	if h.loadErr != "" {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, h.loadErr).Grow(1).FontSize(13).TextColor(theme.Danger)
			if ui.Button(c, "Retry").Disabled(h.loading).Clicked() {
				a.refreshHomeImport()
			}
		})
	}
	if h.importErr != "" {
		ui.Text(c, h.importErr).FontSize(13).TextColor(theme.Danger)
	}
	for _, failure := range h.providerErrors {
		failure := failure
		label := strings.TrimSpace(failure.Provider)
		if label == "" {
			label = "Unknown provider"
		}
		message := label + " sessions are unavailable"
		if strings.TrimSpace(failure.Message) != "" {
			message += ": " + failure.Message
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, message).Grow(1).FontSize(12).TextColor(theme.Danger)
			if ui.Button(c, "Retry").Disabled(h.loading || failure.Provider == "").Clicked() {
				a.retryHomeImportProvider(failure.Provider)
			}
		})
	}
	if h.loading {
		label := "Loading sessions…"
		if len(h.entries) > 0 {
			label = "Refreshing sessions…"
		}
		ui.Text(c, label).FontSize(13).TextColor(theme.TextMuted)
	}
	if view := h.emptyMessage(); view != "" {
		ui.Text(c, view).FontSize(13).TextColor(theme.TextMuted)
	}
}

func (a *App) homeImportRow(c *ui.Context, entry homeImportEntry) {
	h := &a.desk.homeImport
	theme := c.Theme()
	key := entry.key()
	importing := h.importing == key
	blocked := h.blocked[key]
	meta := homeImportActivityTime(entry.LastActivityAt, time.Now())
	switch {
	case importing:
		meta = "Importing…"
	case blocked:
		meta = "Outcome unknown"
	}
	button := ui.ButtonBase(c).WidthPercent(100).Padding(8).Radius(8).Background(theme.Surface).
		Disabled(h.importing != "" || blocked).
		Children(func() {
			ui.Column(c).WidthPercent(100).Gap(2).Children(func() {
				ui.Row(c).WidthPercent(100).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, homeImportTitle(entry)).Grow(1).FontSize(14)
					ui.Text(c, meta).FontSize(12).TextColor(theme.TextMuted)
				})
				ui.Text(c, homeImportPromptPreview(entry)).FontSize(13).TextColor(theme.TextMuted)
				ui.Text(c, homeImportProviderLabel(entry)+" · "+homeImportFolderLabel(entry.CWD, a.projects)).
					FontSize(12).TextColor(theme.TextMuted)
			})
		})
	if button.Hovered() {
		button.Background(theme.SurfaceHover)
	}
	if button.Clicked() {
		a.importHomeSession(entry)
	}
}

// fetchHomeImportSessions lists importable provider sessions using the current
// daemon contract. A nil providers slice means every provider; cwd is omitted
// because the home entry is host-wide.
func fetchHomeImportSessions(ctx context.Context, c caller, limit int, query string, providers []string) (homeImportListing, error) {
	if limit <= 0 {
		limit = homeImportPageSize
	}
	if limit > homeImportMaxLimit {
		limit = homeImportMaxLimit
	}
	payload := map[string]any{"limit": limit}
	if q := strings.TrimSpace(query); q != "" {
		payload["query"] = q
	}
	if len(providers) > 0 {
		payload["providers"] = providers
	}
	request, err := json.Marshal(payload)
	if err != nil {
		return homeImportListing{}, err
	}
	raw, err := c.Call(ctx, "fetch_recent_provider_sessions_request", request)
	if err != nil {
		return homeImportListing{}, err
	}
	var response struct {
		Entries                      *[]homeImportEntry        `json:"entries"`
		FilteredAlreadyImportedCount int                       `json:"filteredAlreadyImportedCount"`
		ProviderErrors               []homeImportProviderError `json:"providerErrors"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return homeImportListing{}, fmt.Errorf("invalid session list: %w", err)
	}
	if response.Entries == nil {
		return homeImportListing{}, errors.New("invalid session list: missing entries")
	}
	seen := map[string]bool{}
	listing := homeImportListing{
		alreadyImported: response.FilteredAlreadyImportedCount,
		providerErrors:  response.ProviderErrors,
	}
	for _, entry := range *response.Entries {
		if strings.TrimSpace(entry.ProviderID) == "" || strings.TrimSpace(entry.ProviderHandleID) == "" {
			return homeImportListing{}, errors.New("invalid session list: a session is missing its provider identity")
		}
		key := entry.key()
		if seen[key] {
			continue
		}
		seen[key] = true
		listing.entries = append(listing.entries, entry)
	}
	homeImportSort(listing.entries)
	return listing, nil
}

// homeImportImportPayload is the exact import_agent_request body for a home
// entry: the listed provider and handle, plus the session's cwd. It carries no
// workspaceId because the daemon provisions or resolves the destination.
func homeImportImportPayload(entry homeImportEntry) (json.RawMessage, error) {
	if strings.TrimSpace(entry.ProviderID) == "" || strings.TrimSpace(entry.ProviderHandleID) == "" {
		return nil, errors.New("Session is missing its provider identity.")
	}
	if strings.TrimSpace(entry.CWD) == "" {
		return nil, errors.New("Session is missing a working directory.")
	}
	return json.Marshal(map[string]string{
		"providerId":       entry.ProviderID,
		"providerHandleId": entry.ProviderHandleID,
		"cwd":              entry.CWD,
	})
}

func importHomeSessionRequest(ctx context.Context, c caller, entry homeImportEntry) (homeImportResult, error) {
	payload, err := homeImportImportPayload(entry)
	if err != nil {
		return homeImportResult{}, err
	}
	raw, err := c.Call(ctx, "import_agent_request", payload)
	if err != nil {
		return homeImportResult{}, err
	}
	return resolveHomeImportAck(raw), nil
}

// resolveHomeImportAck classifies the daemon's status message. A malformed or
// incomplete acknowledgement is treated as an unknown mutation, never as a
// failure that tempts a duplicate import.
func resolveHomeImportAck(raw json.RawMessage) homeImportResult {
	if len(raw) == 0 || string(raw) == "null" {
		return homeImportResult{unknown: true}
	}
	var ack struct {
		Status  string  `json:"status"`
		AgentID string  `json:"agentId"`
		Error   *string `json:"error"`
		Agent   *struct {
			ID          string `json:"id"`
			WorkspaceID string `json:"workspaceId"`
			CWD         string `json:"cwd"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(raw, &ack); err != nil {
		return homeImportResult{unknown: true}
	}
	switch ack.Status {
	case "agent_create_failed":
		message := "The host rejected the import."
		if ack.Error != nil && strings.TrimSpace(*ack.Error) != "" {
			message = strings.TrimSpace(*ack.Error)
		}
		return homeImportResult{failure: message}
	case "agent_resumed":
		result := homeImportResult{}
		payloadID := ""
		if ack.Agent != nil {
			payloadID = strings.TrimSpace(ack.Agent.ID)
			result.workspaceID = strings.TrimSpace(ack.Agent.WorkspaceID)
			result.cwd = strings.TrimSpace(ack.Agent.CWD)
		}
		statusID := strings.TrimSpace(ack.AgentID)
		if statusID != "" && payloadID != "" && statusID != payloadID {
			return homeImportResult{unknown: true}
		}
		result.agentID = payloadID
		if statusID != "" {
			result.agentID = statusID
		}
		if result.agentID == "" {
			return homeImportResult{unknown: true}
		}
		return result
	default:
		return homeImportResult{unknown: true}
	}
}

// homeImportPlacement returns the imported agent only when the loaded
// directory places it in a workspace known to this host. Reporting no
// placement is safer than opening a tab in whichever workspace is current.
func homeImportPlacement(agents []Agent, workspaces []Workspace, agentID, workspaceID string) (Agent, bool) {
	if agentID == "" {
		return Agent{}, false
	}
	var imported *Agent
	for i := range agents {
		if agents[i].ID == agentID {
			imported = &agents[i]
			break
		}
	}
	if imported == nil {
		return Agent{}, false
	}
	target := strings.TrimSpace(imported.WorkspaceID)
	if target == "" {
		target = strings.TrimSpace(workspaceID)
	}
	if target == "" {
		return Agent{}, false
	}
	for _, workspace := range workspaces {
		if workspace.ID != target {
			continue
		}
		agent := *imported
		agent.WorkspaceID = target
		return agent, true
	}
	return Agent{}, false
}

func homeImportAgentLabel(agent Agent) string {
	if title := strings.TrimSpace(agent.Title); title != "" {
		return title
	}
	if provider := strings.TrimSpace(agent.Provider); provider != "" {
		return provider
	}
	return agent.ID
}

func (h *homeImportState) applyListing(listing homeImportListing) {
	h.entries = listing.entries
	h.alreadyImported = listing.alreadyImported
	h.providerErrors = listing.providerErrors
	h.rebuildImportFilters()
	h.loaded = true
}

func (h *homeImportState) mergeProviderListing(provider string, listing homeImportListing) {
	kept := make([]homeImportEntry, 0, len(h.entries)+len(listing.entries))
	for _, entry := range h.entries {
		if entry.ProviderID != provider {
			kept = append(kept, entry)
		}
	}
	kept = append(kept, listing.entries...)
	homeImportSort(kept)
	h.entries = kept
	failures := make([]homeImportProviderError, 0, len(h.providerErrors)+len(listing.providerErrors))
	for _, failure := range h.providerErrors {
		if failure.Provider != provider {
			failures = append(failures, failure)
		}
	}
	h.providerErrors = append(failures, listing.providerErrors...)
	h.rebuildImportFilters()
	h.loaded = true
}

func (h *homeImportState) setProviderError(provider, message string) {
	failures := make([]homeImportProviderError, 0, len(h.providerErrors)+1)
	for _, failure := range h.providerErrors {
		if failure.Provider != provider {
			failures = append(failures, failure)
		}
	}
	h.providerErrors = append(failures, homeImportProviderError{Provider: provider, Message: message})
}

func (h *homeImportState) block(key string) {
	if h.blocked == nil {
		h.blocked = map[string]bool{}
	}
	h.blocked[key] = true
}

func (h *homeImportState) rebuildImportFilters() {
	found := map[string]bool{}
	labels := map[string]string{}
	for _, entry := range h.entries {
		if entry.ProviderID == "" {
			continue
		}
		found[entry.ProviderID] = true
		if label := strings.TrimSpace(entry.ProviderLabel); label != "" {
			labels[entry.ProviderID] = label
		}
	}
	ids := make([]string, 0, len(found))
	for id := range found {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	h.filters = ids
	h.labels = labels
	if h.filter != "" && !found[h.filter] {
		h.filter = ""
	}
}

type homeImportFilterOption struct {
	id    string
	label string
}

func (h *homeImportState) filterOptions() []homeImportFilterOption {
	options := make([]homeImportFilterOption, 0, len(h.filters))
	counts := map[string]int{}
	for _, id := range h.filters {
		counts[h.providerName(id)]++
	}
	for _, id := range h.filters {
		label := h.providerName(id)
		if counts[label] > 1 {
			label += " (" + id + ")"
		}
		options = append(options, homeImportFilterOption{id: id, label: label})
	}
	return options
}

func (h *homeImportState) providerName(id string) string {
	if label := strings.TrimSpace(h.labels[id]); label != "" {
		return label
	}
	return id
}

func (h *homeImportState) filterLabel() string {
	if h.filter == "" {
		return "All providers"
	}
	return h.providerName(h.filter)
}

func (h *homeImportState) visibleEntries() []homeImportEntry {
	if h.filter == "" {
		return h.entries
	}
	visible := make([]homeImportEntry, 0, len(h.entries))
	for _, entry := range h.entries {
		if entry.ProviderID == h.filter {
			visible = append(visible, entry)
		}
	}
	return visible
}

func (h *homeImportState) hasMore() bool {
	return h.limit < homeImportMaxLimit && len(h.entries) >= h.limit
}

func (h *homeImportState) emptyMessage() string {
	if h.loading || h.loadErr != "" || !h.loaded {
		return ""
	}
	if len(h.visibleEntries()) > 0 {
		return ""
	}
	switch {
	case strings.TrimSpace(h.query) != "":
		return "No sessions match this search."
	case h.filter != "":
		return "No recent sessions for this provider."
	case h.alreadyImported > 0:
		return "Every recent session is already imported."
	default:
		return "No recent sessions found."
	}
}

func nextHomeImportLimit(limit int) int {
	for _, candidate := range homeImportPageLimits {
		if candidate > limit {
			return candidate
		}
	}
	return homeImportMaxLimit
}

func homeImportSort(entries []homeImportEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		return homeImportTimestamp(entries[i].LastActivityAt).After(homeImportTimestamp(entries[j].LastActivityAt))
	})
}

func homeImportTimestamp(raw string) time.Time {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func homeImportTitle(entry homeImportEntry) string {
	if title := strings.TrimSpace(entry.Title); title != "" {
		return title
	}
	if preview := strings.TrimSpace(entry.FirstPromptPreview); preview != "" {
		return preview
	}
	return "Untitled session"
}

func homeImportPromptPreview(entry homeImportEntry) string {
	if preview := strings.TrimSpace(entry.LastPromptPreview); preview != "" {
		return preview
	}
	if preview := strings.TrimSpace(entry.FirstPromptPreview); preview != "" {
		return preview
	}
	return "No prompt preview"
}

func homeImportProviderLabel(entry homeImportEntry) string {
	if label := strings.TrimSpace(entry.ProviderLabel); label != "" {
		return label
	}
	if provider := strings.TrimSpace(entry.ProviderID); provider != "" {
		return provider
	}
	return "Unknown provider"
}

func homeImportFolderLabel(cwd string, projects []Project) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "No working directory"
	}
	normalized := homeImportTrimSlash(cwd)
	bestRoot, bestName := "", ""
	for _, project := range projects {
		root := homeImportTrimSlash(strings.TrimSpace(project.Root))
		if root == "" {
			continue
		}
		if bestRoot != "" && len(root) <= len(bestRoot) {
			continue
		}
		covered := normalized == root
		if !covered && root == "/" {
			covered = strings.HasPrefix(normalized, "/")
		}
		if !covered && strings.HasPrefix(normalized, root+"/") {
			covered = true
		}
		if !covered {
			continue
		}
		name := strings.TrimSpace(project.Name)
		if name == "" {
			name = root
		}
		bestRoot, bestName = root, name
	}
	if bestRoot == "" {
		return normalized
	}
	relative := strings.TrimPrefix(normalized, bestRoot)
	relative = strings.TrimPrefix(relative, "/")
	if relative == "" {
		return bestName
	}
	return bestName + " · " + relative
}

func homeImportTrimSlash(path string) string {
	if len(path) <= 1 {
		return path
	}
	return strings.TrimRight(path, "/")
}

func homeImportActivityTime(raw string, now time.Time) string {
	then, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return "Unknown time"
	}
	elapsed := now.Sub(then)
	if elapsed < 0 {
		elapsed = 0
	}
	switch {
	case elapsed < time.Minute:
		return "just now"
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm ago", int(elapsed/time.Minute))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(elapsed/time.Hour))
	case elapsed < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(elapsed/(24*time.Hour)))
	case then.Year() == now.Year():
		return then.Format("Jan 2")
	default:
		return then.Format("Jan 2, 2006")
	}
}
