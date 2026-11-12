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
)

const historyPageLimit = 200
const historySearchDebounce = 200 * time.Millisecond

type historyAgent struct {
	HostID, HostName                              string
	ID, Title, WorkspaceID, CWD, Provider, Status string
	ProjectName, WorkspaceName, Branch            string
	UpdatedAt                                     time.Time
	ArchivedAt                                    string
	PendingPermissions                            int
}

type historyIdentity struct{ hostID, agentID string }

type historyHost struct {
	id, name                  string
	connected, supportsSearch bool
}

type historyHostError struct {
	HostID, HostName, Message string
}

type historyPageInfo struct {
	HasMore    bool    `json:"hasMore"`
	NextCursor *string `json:"nextCursor"`
}

type historyHostPage struct {
	hostID          string
	entries         []historyAgent
	pageInfo        historyPageInfo
	searchTruncated bool
}

type historyBatch struct {
	hosts               []historyHost
	selectedHost, query string
	searchSupported     bool
	pages               []historyHostPage
	hostErrors          []historyHostError
	loadErr             string
}

type historyRequest struct {
	epoch, revision     uint64
	selectedHost, query string
	cursorQuery         string
	more                bool
	cursors             map[string]string
	pageDepth           map[string]int
}

type historyListRow struct {
	section string
	index   int
}

// A cursor belongs to one host and one query. Missing cursors mean exhausted
// hosts, never permission to restart a host at its first page.
type historyState struct {
	open                             bool
	epoch, revision                  uint64
	loading, loadingMore, loaded     bool
	selectedHost, search, query      string
	searchSupported, searchTruncated bool
	searchTimer                      *time.Timer
	cancel                           context.CancelFunc
	hosts                            []historyHost
	entries                          []historyAgent
	rows                             []historyListRow
	list                             ui.ListState
	cursors                          map[string]string
	seenCursors                      map[string]map[string]bool
	pageDepth                        map[string]int
	hostErrors                       []historyHostError
	loadErr                          string
}

func (h *historyState) invalidate() {
	h.revision++
	if h.cancel != nil {
		h.cancel()
		h.cancel = nil
	}
	h.loading, h.loadingMore = false, false
}

func (h *historyState) close() {
	h.invalidate()
	if h.searchTimer != nil {
		h.searchTimer.Stop()
		h.searchTimer = nil
	}
	h.open = false
	h.epoch++
}

func (h *historyState) resetQuery(hostID, query string) {
	h.invalidate()
	h.selectedHost, h.search, h.query = hostID, query, strings.TrimSpace(query)
	h.entries, h.rows = nil, nil
	h.cursors, h.seenCursors, h.hostErrors = nil, nil, nil
	h.pageDepth = nil
	h.loadErr = ""
	h.loaded, h.searchTruncated = false, false
	h.list = ui.ListState{}
}

func (h *historyState) hasMore() bool { return !h.searchTruncated && len(h.cursors) > 0 }

func (h *historyState) begin(more bool) (historyRequest, bool) {
	if !h.open || (more && (h.loading || !h.hasMore())) {
		return historyRequest{}, false
	}
	h.invalidate()
	h.loading, h.loadingMore = true, more
	h.loadErr = ""
	r := historyRequest{epoch: h.epoch, revision: h.revision, selectedHost: h.selectedHost, query: strings.TrimSpace(h.search), cursorQuery: h.query, more: more}
	if more {
		r.cursors = make(map[string]string, len(h.cursors))
		for hostID, cursor := range h.cursors {
			r.cursors[hostID] = cursor
		}
	} else {
		r.pageDepth = make(map[string]int, len(h.pageDepth))
		for hostID, depth := range h.pageDepth {
			r.pageDepth[hostID] = depth
		}
	}
	return r, true
}

func (h *historyState) accepts(r historyRequest) bool {
	return h.open && h.epoch == r.epoch && h.revision == r.revision && h.selectedHost == r.selectedHost && strings.TrimSpace(h.search) == r.query
}

func (h *historyState) apply(r historyRequest, batch historyBatch, now time.Time) bool {
	if !h.accepts(r) {
		return false
	}
	replace := !r.more || batch.selectedHost != r.selectedHost
	if batch.selectedHost != r.selectedHost {
		h.entries, h.rows = nil, nil
		h.list = ui.ListState{}
	}
	h.loading, h.loadingMore, h.loaded = false, false, true
	h.cancel = nil
	h.hosts, h.searchSupported = batch.hosts, batch.searchSupported
	h.selectedHost, h.query = batch.selectedHost, batch.query
	h.loadErr = batch.loadErr
	if replace {
		h.cursors = map[string]string{}
		h.seenCursors = map[string]map[string]bool{}
		h.pageDepth = map[string]int{}
		if len(batch.pages) == 0 {
			for hostID, depth := range r.pageDepth {
				h.pageDepth[hostID] = depth
			}
		}
		h.hostErrors = nil
		h.searchTruncated = false
	} else {
		for hostID := range r.cursors {
			delete(h.cursors, hostID)
		}
	}
	for _, failure := range batch.hostErrors {
		h.setHostError(failure)
	}
	if !replace && r.cursorQuery != batch.query {
		// A host changed its search capability while paging. Mixing its plain
		// history into query results would give the query the wrong answer.
		h.entries, h.rows, h.cursors = nil, nil, nil
		h.pageDepth = nil
		h.list = ui.ListState{}
		h.searchTruncated = false
		h.loadErr = "Host search support changed. Refresh history."
		return true
	}
	if len(batch.pages) > 0 {
		if replace {
			h.entries = nil
		}
		positions := make(map[historyIdentity]int, len(h.entries))
		for i := range h.entries {
			positions[historyIdentity{h.entries[i].HostID, h.entries[i].ID}] = i
		}
		for _, page := range batch.pages {
			h.pageDepth[page.hostID]++
			delete(h.cursors, page.hostID)
			for _, entry := range page.entries {
				key := historyIdentity{entry.HostID, entry.ID}
				if i, ok := positions[key]; ok {
					if !entry.UpdatedAt.Before(h.entries[i].UpdatedAt) {
						h.entries[i] = entry
					}
				} else {
					positions[key] = len(h.entries)
					h.entries = append(h.entries, entry)
				}
			}
			if batch.query != "" && page.searchTruncated {
				h.searchTruncated = true
			}
			if !page.pageInfo.HasMore {
				continue
			}
			cursor := ""
			if page.pageInfo.NextCursor != nil {
				cursor = *page.pageInfo.NextCursor
			}
			if cursor == "" || h.seenCursors[page.hostID][cursor] {
				h.setHostError(historyHostError{HostID: page.hostID, HostName: h.hostName(page.hostID), Message: "The host returned an invalid history cursor. Refresh to try again."})
				continue
			}
			if h.seenCursors[page.hostID] == nil {
				h.seenCursors[page.hostID] = map[string]bool{}
			}
			h.seenCursors[page.hostID][cursor] = true
			h.cursors[page.hostID] = cursor
		}
		sort.Slice(h.entries, func(i, j int) bool {
			a, b := h.entries[i], h.entries[j]
			if !a.UpdatedAt.Equal(b.UpdatedAt) {
				return a.UpdatedAt.After(b.UpdatedAt)
			}
			if a.HostID != b.HostID {
				return a.HostID < b.HostID
			}
			return a.ID < b.ID
		})
		h.rebuildRows(now)
	}
	for _, failure := range batch.hostErrors {
		delete(h.cursors, failure.HostID)
	}
	return true
}

func (h *historyState) setHostError(failure historyHostError) {
	for i := range h.hostErrors {
		if h.hostErrors[i].HostID == failure.HostID {
			h.hostErrors[i] = failure
			return
		}
	}
	h.hostErrors = append(h.hostErrors, failure)
}

func (h *historyState) hostName(id string) string {
	for _, host := range h.hosts {
		if host.id == id {
			return host.name
		}
	}
	return id
}

func (h *historyState) rebuildRows(now time.Time) {
	h.rows = h.rows[:0]
	section := ""
	for i := range h.entries {
		next := historyDateSection(h.entries[i].UpdatedAt, now)
		if next != section {
			section = next
			h.rows = append(h.rows, historyListRow{section: section})
		}
		h.rows = append(h.rows, historyListRow{index: i})
	}
}

func historyDateSection(at, now time.Time) string {
	at = at.In(now.Location())
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	day := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, now.Location())
	if !day.Before(today) {
		return "Today"
	}
	if !day.Before(today.AddDate(0, 0, -1)) {
		return "Yesterday"
	}
	if !day.Before(today.AddDate(0, 0, -7)) {
		return "This week"
	}
	if !day.Before(today.AddDate(0, 0, -30)) {
		return "This month"
	}
	return "Older"
}

// fetchHistoryPage uses the same RPC, chronology and page limit as
// use-agent-history.ts. Provider session discovery belongs to Import session.
func fetchHistoryPage(ctx context.Context, source navigationSource, cursor, query string) (historyHostPage, error) {
	page := struct {
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor,omitempty"`
	}{historyPageLimit, cursor}
	request, err := json.Marshal(struct {
		Page   any                 `json:"page"`
		Sort   []map[string]string `json:"sort"`
		Search string              `json:"search,omitempty"`
	}{Page: page, Sort: []map[string]string{{"key": "updated_at", "direction": "desc"}}, Search: query})
	if err != nil {
		return historyHostPage{}, err
	}
	raw, err := source.Client.Call(ctx, "fetch_agent_history_request", request)
	if err != nil {
		return historyHostPage{}, err
	}
	var payload struct {
		Entries []struct {
			Agent struct {
				ID, Title, WorkspaceID, CWD, Provider, Status, UpdatedAt, ArchivedAt string
				PendingPermissions                                                   []json.RawMessage `json:"pendingPermissions"`
			} `json:"agent"`
			Project struct {
				ProjectName, WorkspaceName string
				Checkout                   struct{ CurrentBranch string } `json:"checkout"`
			} `json:"project"`
		} `json:"entries"`
		PageInfo        *historyPageInfo `json:"pageInfo"`
		SearchTruncated bool             `json:"searchTruncated"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return historyHostPage{}, fmt.Errorf("read history: %w", err)
	}
	if payload.PageInfo == nil {
		return historyHostPage{}, errors.New("the host did not return history page information")
	}
	result := historyHostPage{hostID: source.ID, pageInfo: *payload.PageInfo, searchTruncated: payload.SearchTruncated, entries: make([]historyAgent, 0, len(payload.Entries))}
	for _, entry := range payload.Entries {
		if entry.Agent.ID == "" {
			return historyHostPage{}, errors.New("the host returned a history row without an agent ID")
		}
		updated, err := time.Parse(time.RFC3339Nano, entry.Agent.UpdatedAt)
		if err != nil {
			return historyHostPage{}, fmt.Errorf("read history activity time: %w", err)
		}
		result.entries = append(result.entries, historyAgent{
			HostID: source.ID, HostName: source.Name, ID: entry.Agent.ID, Title: entry.Agent.Title,
			WorkspaceID: entry.Agent.WorkspaceID, CWD: entry.Agent.CWD, Provider: entry.Agent.Provider,
			Status: entry.Agent.Status, UpdatedAt: updated, ArchivedAt: entry.Agent.ArchivedAt,
			PendingPermissions: len(entry.Agent.PendingPermissions), ProjectName: entry.Project.ProjectName,
			WorkspaceName: entry.Project.WorkspaceName, Branch: entry.Project.Checkout.CurrentBranch,
		})
	}
	return result, nil
}

func historySearchAvailable(info json.RawMessage) bool {
	var payload struct {
		Features struct {
			AgentHistorySearch bool `json:"agentHistorySearch"`
		} `json:"features"`
	}
	return json.Unmarshal(info, &payload) == nil && payload.Features.AgentHistorySearch
}

func fetchHistoryBatch(ctx context.Context, sources []navigationSource, request historyRequest) historyBatch {
	batch := historyBatch{selectedHost: request.selectedHost, query: strings.TrimSpace(request.query)}
	selectedExists := request.selectedHost == ""
	for _, source := range sources {
		if source.ID == request.selectedHost {
			selectedExists = true
		}
	}
	if !selectedExists {
		batch.selectedHost = ""
		request.more = false
		request.cursors, request.pageDepth = nil, nil
	}
	targets := make([]navigationSource, 0, len(sources))
	reachable, searchSupported := 0, true
	for _, source := range sources {
		name := source.Name
		if name == "" {
			name = source.ID
		}
		source.Name = name
		connected := source.Err == nil && source.Client != nil
		supportsSearch := connected && historySearchAvailable(source.Info)
		batch.hosts = append(batch.hosts, historyHost{id: source.ID, name: name, connected: connected, supportsSearch: supportsSearch})
		if batch.selectedHost != "" && source.ID != batch.selectedHost {
			continue
		}
		if connected {
			reachable++
			searchSupported = searchSupported && supportsSearch
		}
		if !connected {
			message := "Host is disconnected"
			if source.Err != nil {
				message = source.Err.Error()
			}
			batch.hostErrors = append(batch.hostErrors, historyHostError{HostID: source.ID, HostName: name, Message: message})
			continue
		}
		if request.more {
			if _, ok := request.cursors[source.ID]; !ok {
				continue
			}
		}
		targets = append(targets, source)
	}
	batch.searchSupported = reachable > 0 && searchSupported
	if !batch.searchSupported {
		batch.query = ""
	}
	if request.more && request.cursorQuery != batch.query {
		return batch
	}
	type result struct {
		pages []historyHostPage
		err   error
	}
	results := make([]result, len(targets))
	var tasks sync.WaitGroup
	for i, source := range targets {
		tasks.Add(1)
		go func(i int, source navigationSource) {
			defer tasks.Done()
			cursor, depth := request.cursors[source.ID], 1
			if !request.more {
				depth = max(1, request.pageDepth[source.ID])
			}
			seen := map[string]bool{}
			for range depth {
				page, err := fetchHistoryPage(ctx, source, cursor, batch.query)
				if err != nil {
					results[i].err = err
					break
				}
				results[i].pages = append(results[i].pages, page)
				if !page.pageInfo.HasMore || (batch.query != "" && page.searchTruncated) {
					break
				}
				if page.pageInfo.NextCursor == nil || *page.pageInfo.NextCursor == "" || seen[*page.pageInfo.NextCursor] {
					break
				}
				cursor = *page.pageInfo.NextCursor
				seen[cursor] = true
			}
		}(i, source)
	}
	tasks.Wait()
	for i, result := range results {
		batch.pages = append(batch.pages, result.pages...)
		if result.err != nil {
			batch.hostErrors = append(batch.hostErrors, historyHostError{HostID: targets[i].ID, HostName: targets[i].Name, Message: result.err.Error()})
		}
	}
	for hostID := range request.cursors {
		found := false
		for _, source := range sources {
			if source.ID == hostID {
				found = true
				break
			}
		}
		if !found {
			batch.hostErrors = append(batch.hostErrors, historyHostError{HostID: hostID, HostName: hostID, Message: "Host is no longer available"})
		}
	}
	if len(batch.pages) == 0 && len(batch.hosts) > 0 {
		batch.loadErr = "No connected hosts could load agent history"
	}
	return batch
}

func (a *App) openHistory() {
	a.showDestination("history")
	h := &a.desk.history
	if h.open {
		if !h.loading {
			a.loadHistory(false)
		}
		return
	}
	h.invalidate()
	epoch := h.epoch + 1
	*h = historyState{open: true, epoch: epoch}
	for _, host := range a.desk.prefs.Hosts {
		a.desk.history.hosts = append(a.desk.history.hosts, historyHost{id: host.ID, name: host.Name})
	}
	a.loadHistory(false)
}

func (a *App) closeHistory() { a.desk.history.close() }

func (a *App) loadHistory(more bool) {
	h := &a.desk.history
	request, ok := h.begin(more)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	h.cancel = cancel
	gen := a.generation
	go func() {
		defer cancel()
		sources, cleanup := a.navigationSources(ctx)
		defer cleanup()
		batch := fetchHistoryBatch(ctx, sources, request)
		if a.win == nil {
			return
		}
		a.update(gen, func() { a.desk.history.apply(request, batch, time.Now()) })
	}()
}

func (a *App) historySearchChanged() {
	h := &a.desk.history
	if h.searchTimer != nil {
		h.searchTimer.Stop()
	}
	// Invalidate immediately: an old response may arrive during the debounce.
	h.invalidate()
	gen, epoch, revision := a.generation, h.epoch, h.revision
	h.searchTimer = time.AfterFunc(historySearchDebounce, func() {
		if a.win == nil {
			return
		}
		a.update(gen, func() {
			h := &a.desk.history
			if h.open && h.epoch == epoch && h.revision == revision {
				a.commitHistorySearch()
			}
		})
	})
}

func (a *App) commitHistorySearch() {
	h := &a.desk.history
	if h.searchTimer != nil {
		h.searchTimer.Stop()
		h.searchTimer = nil
	}
	query := strings.TrimSpace(h.search)
	if !h.searchSupported {
		query = ""
	}
	if h.query == query && h.loaded {
		return
	}
	h.resetQuery(h.selectedHost, h.search)
	h.query = query
	a.loadHistory(false)
}

func (a *App) selectHistoryHost(hostID string) {
	h := &a.desk.history
	if h.selectedHost == hostID {
		return
	}
	if h.searchTimer != nil {
		h.searchTimer.Stop()
		h.searchTimer = nil
	}
	h.resetQuery(hostID, h.search)
	a.loadHistory(false)
}

func (a *App) historyView(c *ui.Context) {
	h := &a.desk.history
	theme := c.Theme()
	width, _ := c.Size()
	layout := a.desk.shell.Layout(width)
	if layout.NavVisible {
		width -= layout.NavWidth
	}
	if layout.ExplorerVisible {
		width -= layout.ExplorerWidth
	}
	ui.Column(c).Fill().Gap(0).Children(func() {
		ui.Row(c).WidthPercent(100).Padding(8, 16).Gap(8).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border).Children(func() {
			if ui.Button(c, "Navigation").Clicked() {
				a.toggleNavigation()
			}
			ui.Text(c, "History").Bold().Grow(1)
			if ui.Button(c, "Refresh").Disabled(h.loading).Clicked() {
				a.loadHistory(false)
			}
		})
		if h.searchSupported || len(h.hosts) > 1 {
			ui.Row(c).WidthPercent(100).Padding(12, 24).Gap(8).AlignItems(ui.Center).Children(func() {
				if h.searchSupported {
					ui.TextInput(c, &h.search).Placeholder("Search history").Label("Search history").Grow(1).
						OnChange(a.historySearchChanged).OnSubmit(a.commitHistorySearch)
					if h.search != "" && ui.Button(c, "Clear search").Clicked() {
						h.search = ""
						a.commitHistorySearch()
					}
				}
				if len(h.hosts) > 1 {
					label := "All hosts"
					if h.selectedHost != "" {
						label = h.hostName(h.selectedHost)
					}
					ui.PopoverMenuButton(c, label, func(menu *ui.Menu) {
						if menu.Item("All hosts").Radio(h.selectedHost == "").Chosen() {
							a.selectHistoryHost("")
						}
						for _, host := range h.hosts {
							label := host.name
							if !host.connected {
								label += " (Offline)"
							}
							if menu.Item(label).Radio(h.selectedHost == host.id).Chosen() {
								a.selectHistoryHost(host.id)
							}
						}
					})
				}
			})
		}
		if h.loaded && !h.searchSupported && len(h.hosts) > 0 {
			for _, host := range h.hosts {
				if host.connected && !host.supportsSearch && (h.selectedHost == "" || h.selectedHost == host.id) {
					ui.Text(c, "Update "+host.name+" to search history.").Padding(4, 24).TextColor(theme.TextMuted).FontSize(12)
				}
			}
		}
		for _, failure := range h.hostErrors {
			ui.Text(c, failure.HostName+": Could not load history. "+failure.Message).Padding(4, 24).TextColor(theme.Danger).FontSize(12)
		}
		if h.loadErr != "" {
			ui.Row(c).Padding(12, 24).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Unable to load sessions. "+h.loadErr).TextColor(theme.Danger).Grow(1)
				if ui.Button(c, "Try again").Disabled(h.loading).Clicked() {
					a.loadHistory(false)
				}
			})
		}
		if h.loading && len(h.entries) == 0 {
			ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Gap(8).Children(func() { ui.Spinner(c).Label("Loading history"); ui.Text(c, "Loading...").TextColor(theme.TextMuted) })
		} else if len(h.entries) == 0 && h.loadErr == "" {
			ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Gap(12).Children(func() {
				message := "No sessions yet"
				if h.query != "" {
					message = "No sessions match"
				} else if h.selectedHost != "" {
					message = "No sessions for this host"
				}
				if len(h.hostErrors) > 0 {
					message = "History is unavailable for the hosts listed above"
				}
				ui.Text(c, message).TextColor(theme.TextMuted)
				ui.Row(c).Gap(8).Children(func() {
					if h.query != "" {
						if ui.Button(c, "Clear search").Clicked() {
							h.search = ""
							a.commitHistorySearch()
						}
					} else if ui.Button(c, "Back").Clicked() {
						a.showDestination("home")
					}
					if ui.Button(c, "Import session").Clicked() {
						a.requestHomeAction("import")
					}
				})
			})
		} else if len(h.entries) > 0 {
			h.list.Key = func(i int) any {
				row := h.rows[i]
				if row.section != "" {
					return row.section
				}
				entry := h.entries[row.index]
				return historyIdentity{entry.HostID, entry.ID}
			}
			ui.List(c, &h.list, len(h.rows), func(i int) {
				row := h.rows[i]
				if row.section != "" {
					ui.Text(c, row.section).Padding(12, 12, 8, 12).TextColor(theme.TextMuted)
					return
				}
				a.historyAgentRow(c, h.entries[row.index], width)
			}).Grow(1).Padding(16, 24, 24, 24).Gap(4)
		}
		if h.loading && len(h.entries) > 0 {
			ui.Text(c, "Loading...").Padding(8, 24).TextColor(theme.TextMuted)
		}
		if h.searchTruncated {
			ui.Text(c, "Too many matches — narrow your search").Padding(12, 24).TextColor(theme.TextMuted)
		} else if h.hasMore() {
			ui.Row(c).Padding(12, 24).Justify(ui.Center).Children(func() {
				if ui.Button(c, "Load more").Disabled(h.loading).Clicked() {
					a.loadHistory(true)
				}
			})
		}
	})
}

func (a *App) historyAgentRow(c *ui.Context, entry historyAgent, width float32) {
	theme := c.Theme()
	title := entry.Title
	if title == "" {
		title = "New session"
	}
	workspace := entry.WorkspaceName
	if workspace == "" {
		workspace = entry.ProjectName
	}
	if workspace == "" {
		workspace = entry.CWD
	}
	row := ui.ButtonBase(c.Key(historyIdentity{entry.HostID, entry.ID})).WidthPercent(100).Padding(8, 12).MinHeight(36).Gap(8).AlignItems(ui.Center).Label(title)
	if row.Hovered() {
		row = row.Background(theme.Surface)
	}
	if row.Pressed() {
		row = row.Background(theme.Border)
	}
	row.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, workspace).MaxWidth(220).Ellipsis("…")
				ui.Text(c, "›").TextColor(theme.TextMuted)
				ui.Text(c, title).Grow(1).Ellipsis("…").TextColor(theme.TextMuted).Tooltip(entry.Provider)
				if entry.ArchivedAt != "" {
					ui.Badge(c, "Archived")
				}
				if entry.PendingPermissions > 0 {
					ui.Badge(c, fmt.Sprintf("%d pending", entry.PendingPermissions))
				}
			})
			if width < 850 {
				ui.Text(c, entry.ProjectName+" · "+entry.HostName+" · "+entry.Branch).FontSize(12).TextColor(theme.TextMuted).Ellipsis("…")
			}
		})
		if width >= 850 {
			ui.Text(c, entry.ProjectName).Width(110).Ellipsis("…").TextColor(theme.TextMuted)
			ui.Text(c, entry.HostName).Width(100).Ellipsis("…").TextColor(theme.TextMuted)
			ui.Text(c, entry.Branch).Width(100).Ellipsis("…").TextColor(theme.TextMuted)
		}
		ui.Text(c, entry.UpdatedAt.Local().Format("Jan 2 15:04")).Width(96).FontSize(12).TextColor(theme.TextMuted)
	})
	if row.Clicked() {
		a.openHistoryAgent(entry.HostID, entry.ID, entry.WorkspaceID)
	}
}
