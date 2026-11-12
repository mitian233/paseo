package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/desktopui"
)

// Display keys are opaque. Routes always come from the projection's identity maps.
// In particular neither host IDs nor daemon-local IDs may be split on punctuation.
type sidebarDirectoryTarget struct{ Host, ID, Root string }
type sidebarDirectoryProject struct {
	Project
	SharedKey  string  `json:"projectKey"`
	CustomName *string `json:"projectCustomName"`
}
type sidebarDirectoryWorkspace struct {
	Workspace
	Labels          []string `json:"labels"`
	Background      bool     `json:"background"`
	Status          string   `json:"status"`
	StatusEnteredAt *string  `json:"statusEnteredAt"`
	CreatedAt       *string  `json:"createdAt"`
	LastPromptAt    *string  `json:"lastUserMessageAt"`
	Placement       *struct {
		Key string `json:"projectKey"`
	} `json:"project"`
}
type sidebarDirectoryHost struct {
	ID, Name, Error, ProjectError, WorkspaceError, AgentError, LabelError string
	ProjectsLoaded, WorkspacesLoaded, AgentsLoaded, LabelsLoaded          bool
	LabelSupported, SortSupported, Stale                                  bool
	Projects                                                              []sidebarDirectoryProject
	Workspaces                                                            []sidebarDirectoryWorkspace
	Agents                                                                []Agent
	Labels                                                                []string
}
type sidebarDirectoryPreferences struct {
	GroupMode      string              `json:"groupMode"`
	WorkspaceSort  string              `json:"workspaceSort"`
	ShowBackground bool                `json:"showBackground"`
	HostFilters    []string            `json:"hostFilters"`
	ProjectFilters []string            `json:"projectFilters"`
	Labels         []string            `json:"labels"`
	ProjectOrder   []string            `json:"projectOrder"`
	WorkspaceOrder map[string][]string `json:"workspaceOrder"`
	PinnedOrder    []string            `json:"pinnedOrder"`
}
type sidebarDirectoryRow struct {
	Key, ID, HostID, HostName, ProjectKey, ProjectName, Name, Branch, Directory, Status string
	Labels                                                                              []string
	Pinned, Unread, Stale, Background                                                   bool
	PinnedAt, StatusAt, CreatedAt, LastPromptAt                                         time.Time
}
type sidebarDirectorySection struct {
	Key, Name, Status string
	Targets           []sidebarDirectoryTarget
	Rows              []sidebarDirectoryRow
}
type sidebarDirectoryIssue struct{ HostID, HostName, Area, Message string }
type sidebarDirectoryProjection struct {
	AllProjects, Projects, Groups                                        []sidebarDirectorySection
	Workspaces, Pinned                                                   []sidebarDirectoryRow
	WorkspaceTargets                                                     map[string]sidebarDirectoryTarget
	ProjectTargets                                                       map[string][]sidebarDirectoryTarget
	ResolvedProjectFilters                                               []string
	Errors                                                               []sidebarDirectoryIssue
	Loading, InitialLoading, Revalidating, ShowHostLabels, SortSupported bool
	// Empty distinguishes no configured hosts, no projects, filtered rows and failed reads.
	Empty string
}
type sidebarDirectoryRequest struct {
	revision  uint64
	signature string
	ctx       context.Context
}
type sidebarDirectoryPending struct {
	target    sidebarDirectoryTarget
	workspace bool
	action    func(string)
	expires   time.Time
}
type sidebarDirectoryState struct {
	initialized, loading bool
	revision             uint64
	signature            string
	cancel               context.CancelFunc
	loadedAt             time.Time
	hosts                []sidebarDirectoryHost
	prefs                sidebarDirectoryPreferences
	pending              *sidebarDirectoryPending
	projectChoice        []sidebarDirectoryTarget
	projectChoiceAction  func(sidebarDirectoryTarget)
	cached               *sidebarDirectoryProjection
}

func sidebarDirectoryKey(kind string, identity ...string) string {
	parts := append([]string{kind}, identity...)
	raw, _ := json.Marshal(parts)
	return string(raw)
}
func defaultSidebarDirectoryPreferences() sidebarDirectoryPreferences {
	return sidebarDirectoryPreferences{GroupMode: "project", WorkspaceSort: "created"}
}
func sidebarLabelKey(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " "))
}
func sidebarUnique(items []string, normalize bool) []string {
	var out []string
	seen := make(map[string]bool, len(items))
	for _, key := range items {
		if normalize {
			key = sidebarLabelKey(key)
		}
		if !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
	}
	return out
}
func (s *sidebarDirectoryState) initialize(raw json.RawMessage) {
	if s.initialized {
		return
	}
	s.initialized = true
	s.prefs = defaultSidebarDirectoryPreferences()
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &s.prefs); err != nil {
			s.prefs = defaultSidebarDirectoryPreferences()
		}
	}
	if s.prefs.GroupMode != "status" {
		s.prefs.GroupMode = "project"
	}
	switch s.prefs.WorkspaceSort {
	case "manual", "created", "lastPrompt":
	default:
		s.prefs.WorkspaceSort = "created"
	}
	s.prefs.HostFilters = sidebarUnique(s.prefs.HostFilters, false)
	s.prefs.ProjectFilters = sidebarUnique(s.prefs.ProjectFilters, false)
	s.prefs.Labels = sidebarUnique(s.prefs.Labels, true)
}
func (s *sidebarDirectoryState) persisted() json.RawMessage {
	raw, _ := json.Marshal(s.prefs)
	return raw
}
func (s *sidebarDirectoryState) reconcileHosts(ids []string) bool {
	allowed := make(map[string]bool, len(ids))
	for _, id := range ids {
		allowed[id] = true
	}
	next := make([]string, 0, len(s.prefs.HostFilters))
	for _, id := range s.prefs.HostFilters {
		if allowed[id] {
			next = append(next, id)
		}
	}
	if len(next) == len(s.prefs.HostFilters) {
		return false
	}
	s.prefs.HostFilters = next
	s.cached = nil
	return true
}
func (s *sidebarDirectoryState) reconcileLabels() bool {
	available, online := map[string]bool{"": true}, false
	for _, h := range s.hosts {
		if h.Error != "" || !h.LabelSupported {
			continue
		}
		if h.LabelError != "" || !h.LabelsLoaded {
			return false
		}
		online = true
		for _, name := range h.Labels {
			available[sidebarLabelKey(name)] = true
		}
	}
	if !online {
		return false
	}
	next := make([]string, 0, len(s.prefs.Labels))
	for _, key := range s.prefs.Labels {
		if available[key] {
			next = append(next, key)
		}
	}
	if len(next) == len(s.prefs.Labels) {
		return false
	}
	s.prefs.Labels = next
	s.cached = nil
	return true
}
func (s *sidebarDirectoryState) begin(signature string, parent context.Context) sidebarDirectoryRequest {
	if s.cancel != nil {
		s.cancel()
	}
	s.revision++
	s.signature = signature
	s.loading = true
	s.cached = nil
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	s.cancel = cancel
	return sidebarDirectoryRequest{revision: s.revision, signature: signature, ctx: ctx}
}
func (s *sidebarDirectoryState) accept(r sidebarDirectoryRequest, hosts []sidebarDirectoryHost) bool {
	if !s.loading || r.revision != s.revision || r.signature != s.signature {
		return false
	}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	previous := make(map[string]sidebarDirectoryHost, len(s.hosts))
	for _, h := range s.hosts {
		previous[h.ID] = h
	}
	for i := range hosts {
		h, old := &hosts[i], previous[hosts[i].ID]
		if !h.ProjectsLoaded && old.ProjectsLoaded {
			h.Projects, h.ProjectsLoaded, h.Stale = old.Projects, true, true
		}
		if !h.WorkspacesLoaded && old.WorkspacesLoaded {
			h.Workspaces, h.WorkspacesLoaded, h.Stale = old.Workspaces, true, true
		}
		if !h.AgentsLoaded && old.AgentsLoaded {
			h.Agents, h.AgentsLoaded = old.Agents, true
		}
	}
	s.hosts, s.loading, s.loadedAt = hosts, false, time.Now()
	s.cached = nil
	return true
}
func (s *sidebarDirectoryState) close() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.revision++
	s.loading = false
	s.loadedAt = time.Time{}
	s.pending = nil
	s.projectChoice = nil
	s.projectChoiceAction = nil
	s.cached = nil
}
func (s *sidebarDirectoryState) projection() sidebarDirectoryProjection {
	if s.cached == nil {
		p := projectSidebarDirectory(s.hosts, s.prefs, s.loading)
		s.cached = &p
	}
	return *s.cached
}

func sidebarDirectoryDecodeError(raw json.RawMessage) error {
	var response struct {
		Error *string `json:"error"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return err
	}
	if response.Error != nil {
		return errors.New(*response.Error)
	}
	return nil
}
func fetchSidebarDirectoryHost(ctx context.Context, source navigationSource) sidebarDirectoryHost {
	h := sidebarDirectoryHost{ID: source.ID, Name: source.Name}
	if source.Err != nil {
		h.Error = source.Err.Error()
		return h
	}
	if source.Client == nil {
		h.Error = "Host is disconnected."
		return h
	}
	var info struct {
		Features struct {
			WorkspaceLabels bool `json:"workspaceLabels"`
			SortTimes       bool `json:"workspaceSortTimes"`
		} `json:"features"`
	}
	_ = json.Unmarshal(source.Info, &info)
	h.LabelSupported, h.SortSupported = info.Features.WorkspaceLabels, info.Features.SortTimes
	raw, err := source.Client.Call(ctx, "project.list.request", json.RawMessage(`{}`))
	var projects struct {
		Projects []sidebarDirectoryProject `json:"projects"`
	}
	if err == nil {
		err = sidebarDirectoryDecodeError(raw)
	}
	if err == nil {
		err = json.Unmarshal(raw, &projects)
	}
	if err == nil && projects.Projects == nil {
		err = errors.New("Invalid project list response.")
	}
	if err != nil {
		h.ProjectError = err.Error()
	} else {
		h.Projects, h.ProjectsLoaded = projects.Projects, true
	}
	workspaces, empty, err := fetchSidebarDirectoryWorkspaces(ctx, source.Client)
	if err != nil {
		h.WorkspaceError = err.Error()
	} else {
		h.Workspaces, h.WorkspacesLoaded = workspaces, true
		known := map[string]bool{}
		for _, p := range h.Projects {
			known[p.ID] = true
		}
		for _, p := range empty {
			if !known[p.ID] {
				h.Projects = append(h.Projects, p)
				known[p.ID] = true
			}
		}
	}
	entries, err := loadPages(ctx, source.Client, "fetch_agents_request")
	if err == nil {
		for _, entry := range entries {
			var v struct {
				Agent Agent `json:"agent"`
			}
			if err = json.Unmarshal(entry, &v); err != nil {
				break
			}
			if v.Agent.ID != "" {
				h.Agents = append(h.Agents, v.Agent)
			}
		}
	}
	if err != nil {
		h.AgentError = err.Error()
		h.Agents = nil
	} else {
		h.AgentsLoaded = true
	}
	if h.LabelSupported {
		raw, err = source.Client.Call(ctx, "workspace.label.list.request", json.RawMessage(`{}`))
		var catalog struct {
			Labels []struct {
				Name string `json:"name"`
			} `json:"labels"`
		}
		if err == nil {
			err = sidebarDirectoryDecodeError(raw)
		}
		if err == nil {
			err = json.Unmarshal(raw, &catalog)
		}
		if err == nil && catalog.Labels == nil {
			err = errors.New("Invalid label list response.")
		}
		if err != nil {
			h.LabelError = err.Error()
		} else {
			h.LabelsLoaded = true
			for _, label := range catalog.Labels {
				h.Labels = append(h.Labels, label.Name)
			}
		}
	}
	return h
}
func fetchSidebarDirectoryWorkspaces(ctx context.Context, daemon caller) ([]sidebarDirectoryWorkspace, []sidebarDirectoryProject, error) {
	var workspaces []sidebarDirectoryWorkspace
	var empty []sidebarDirectoryProject
	cursor, seen := "", map[string]bool{}
	for {
		page := map[string]any{"limit": 200}
		if cursor != "" {
			page["cursor"] = cursor
		}
		payload, _ := json.Marshal(map[string]any{"page": page, "sort": []map[string]string{{"key": "project_id", "direction": "asc"}}})
		raw, err := daemon.Call(ctx, "fetch_workspaces_request", payload)
		if err != nil {
			return nil, nil, err
		}
		if err = sidebarDirectoryDecodeError(raw); err != nil {
			return nil, nil, err
		}
		var response struct {
			Entries       []sidebarDirectoryWorkspace `json:"entries"`
			EmptyProjects []sidebarDirectoryProject   `json:"emptyProjects"`
			PageInfo      *struct {
				HasMore    bool    `json:"hasMore"`
				NextCursor *string `json:"nextCursor"`
			} `json:"pageInfo"`
		}
		if err = json.Unmarshal(raw, &response); err != nil {
			return nil, nil, err
		}
		if response.Entries == nil || response.PageInfo == nil {
			return nil, nil, errors.New("Invalid workspace directory response.")
		}
		for _, w := range response.Entries {
			if w.ID == "" || w.ProjectID == "" {
				return nil, nil, errors.New("Workspace directory contains an entry without an identity.")
			}
		}
		workspaces = append(workspaces, response.Entries...)
		empty = append(empty, response.EmptyProjects...)
		if !response.PageInfo.HasMore {
			return workspaces, empty, nil
		}
		next := response.PageInfo.NextCursor
		if next == nil || *next == "" || seen[*next] {
			return nil, nil, errors.New("Workspace pagination returned an invalid cursor.")
		}
		cursor = *next
		seen[cursor] = true
	}
}

func sidebarTime(value *string) time.Time {
	if value == nil {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, *value)
	return t
}
func sidebarStatus(status string) string {
	switch status {
	case "needs_input", "failed", "attention", "running", "done":
		return status
	}
	return "done"
}
func sidebarStatusLabel(status string) string {
	switch status {
	case "needs_input":
		return "Needs input"
	case "failed":
		return "Failed"
	case "attention":
		return "Ready to review"
	case "running":
		return "Working"
	default:
		return "Done"
	}
}
func sidebarContains(items []string, key string) bool {
	for _, item := range items {
		if item == key {
			return true
		}
	}
	return false
}
func sidebarToggle(items []string, key string) []string {
	if !sidebarContains(items, key) {
		return append(items, key)
	}
	out := make([]string, 0, len(items)-1)
	for _, item := range items {
		if item != key {
			out = append(out, item)
		}
	}
	return out
}

// Like the RN order store, only reorder existing slots: newly discovered rows keep
// their natural positions until the user's stored order is extended.
func sidebarStoredOrder[T any](items []T, order []string, key func(T) string) []T {
	if len(items) < 2 || len(order) == 0 {
		return items
	}
	byKey := make(map[string]T, len(items))
	for _, item := range items {
		byKey[key(item)] = item
	}
	var keys []string
	seen := map[string]bool{}
	for _, k := range order {
		if _, ok := byKey[k]; ok && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return items
	}
	out := make([]T, len(items))
	index := 0
	for i, item := range items {
		if seen[key(item)] {
			out[i] = byKey[keys[index]]
			index++
		} else {
			out[i] = item
		}
	}
	return out
}

func projectSidebarDirectory(hosts []sidebarDirectoryHost, prefs sidebarDirectoryPreferences, loading bool) sidebarDirectoryProjection {
	p := sidebarDirectoryProjection{WorkspaceTargets: map[string]sidebarDirectoryTarget{}, ProjectTargets: map[string][]sidebarDirectoryTarget{}, Loading: loading, SortSupported: true}
	selected := make([]sidebarDirectoryHost, 0, len(hosts))
	for _, h := range hosts {
		if len(prefs.HostFilters) == 0 || sidebarContains(prefs.HostFilters, h.ID) {
			selected = append(selected, h)
		}
	}
	if len(selected) == 0 && len(prefs.HostFilters) > 0 {
		selected = hosts
	}
	byKey := map[string]*sidebarDirectorySection{}
	projectKeys := map[sidebarDirectoryTarget]string{}
	visibleHosts, hydrated := map[string]bool{}, false
	for _, h := range selected {
		for _, issue := range []struct{ area, message string }{{"Host", h.Error}, {"Projects", h.ProjectError}, {"Workspaces", h.WorkspaceError}, {"Activity", h.AgentError}, {"Labels", h.LabelError}} {
			if issue.message != "" {
				p.Errors = append(p.Errors, sidebarDirectoryIssue{h.ID, h.Name, issue.area, issue.message})
			}
		}
		if h.ProjectsLoaded || h.WorkspacesLoaded {
			hydrated = true
		}
		if !h.SortSupported {
			p.SortSupported = false
		}
		projects := append([]sidebarDirectoryProject(nil), h.Projects...)
		known := make(map[string]bool, len(projects))
		counts := map[string]int{}
		for _, v := range projects {
			known[v.ID] = true
			if v.SharedKey != "" {
				counts[v.SharedKey]++
			}
		}
		for _, w := range h.Workspaces {
			if known[w.ProjectID] {
				continue
			}
			v := sidebarDirectoryProject{Project: Project{ID: w.ProjectID, Name: w.Project, Root: w.Root, Kind: w.Kind}}
			if w.Placement != nil {
				v.SharedKey = w.Placement.Key
				if v.SharedKey != "" {
					counts[v.SharedKey]++
				}
			}
			projects = append(projects, v)
			known[v.ID] = true
		}
		for _, v := range projects {
			if v.ID == "" {
				continue
			}
			key := sidebarDirectoryKey("shared-project", v.SharedKey)
			if v.SharedKey == "" || counts[v.SharedKey] != 1 {
				key = sidebarDirectoryKey("project", h.ID, v.ID)
			}
			target := sidebarDirectoryTarget{Host: h.ID, ID: v.ID, Root: v.Root}
			projectKeys[sidebarDirectoryTarget{Host: h.ID, ID: v.ID}] = key
			section := byKey[key]
			name := v.Name
			if v.CustomName != nil {
				name = *v.CustomName
			}
			if name == "" {
				name = v.ID
			}
			if section == nil {
				section = &sidebarDirectorySection{Key: key, Name: name}
				byKey[key] = section
			} else if v.CustomName != nil {
				section.Name = name
			}
			section.Targets = append(section.Targets, target)
		}
	}
	for _, h := range selected {
		seen := map[string]bool{}
		for _, w := range h.Workspaces {
			key := sidebarDirectoryKey("workspace", h.ID, w.ID)
			if w.ID == "" || seen[key] {
				continue
			}
			seen[key] = true
			project := byKey[projectKeys[sidebarDirectoryTarget{Host: h.ID, ID: w.ProjectID}]]
			if project == nil {
				continue
			}
			status := sidebarStatus(w.Status)
			if w.Status == "" {
				label, _ := workspaceSidebarStatus(w.ID, h.Agents)
				switch label {
				case "Needs input":
					status = "needs_input"
				case "Failed":
					status = "failed"
				case "Running":
					status = "running"
				case "Unread":
					status = "attention"
				}
			}
			row := sidebarDirectoryRow{Key: key, ID: w.ID, HostID: h.ID, HostName: h.Name, ProjectKey: project.Key, ProjectName: project.Name, Name: w.Name, Branch: w.Git.CurrentBranch, Directory: w.Directory, Status: status, Labels: w.Labels, Background: w.Background, Pinned: w.PinnedAt != nil, PinnedAt: sidebarTime(w.PinnedAt), StatusAt: sidebarTime(w.StatusEnteredAt), CreatedAt: sidebarTime(w.CreatedAt), LastPromptAt: sidebarTime(w.LastPromptAt), Unread: status == "attention", Stale: h.Stale || h.Error != "" || h.WorkspaceError != ""}
			if row.Directory == "" {
				row.Directory = w.Root
			}
			if row.Name == "" {
				row.Name = w.ID
			}
			project.Rows = append(project.Rows, row)
		}
	}
	for _, section := range byKey {
		for _, key := range prefs.ProjectFilters {
			if key == section.Key {
				p.ResolvedProjectFilters = append(p.ResolvedProjectFilters, key)
			}
		}
		p.AllProjects = append(p.AllProjects, *section)
	}
	sort.Slice(p.AllProjects, func(i, j int) bool {
		a, b := p.AllProjects[i], p.AllProjects[j]
		if a.Name != b.Name {
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		}
		return a.Key < b.Key
	})
	p.AllProjects = sidebarStoredOrder(p.AllProjects, prefs.ProjectOrder, func(v sidebarDirectorySection) string { return v.Key })
	for _, section := range p.AllProjects {
		if len(p.ResolvedProjectFilters) > 0 && !sidebarContains(p.ResolvedProjectFilters, section.Key) {
			continue
		}
		allRows := section.Rows
		section.Rows = make([]sidebarDirectoryRow, 0, len(allRows))
		for _, row := range allRows {
			if !prefs.ShowBackground && row.Background {
				continue
			}
			if len(prefs.Labels) > 0 {
				keys := map[string]bool{}
				for _, label := range row.Labels {
					if k := sidebarLabelKey(label); k != "" {
						keys[k] = true
					}
				}
				match := false
				for _, label := range prefs.Labels {
					if label == "" && len(keys) == 0 || label != "" && keys[label] {
						match = true
						break
					}
				}
				if !match {
					continue
				}
			}
			section.Rows = append(section.Rows, row)
			p.WorkspaceTargets[row.Key] = sidebarDirectoryTarget{Host: row.HostID, ID: row.ID, Root: row.Directory}
		}
		if len(section.Rows) == 0 && (len(prefs.Labels) > 0 || !prefs.ShowBackground && len(allRows) > 0) {
			continue
		}
		sort.SliceStable(section.Rows, func(i, j int) bool {
			a, b := section.Rows[i], section.Rows[j]
			if prefs.GroupMode == "project" && p.SortSupported && prefs.WorkspaceSort != "manual" {
				ta, tb := a.CreatedAt, b.CreatedAt
				if prefs.WorkspaceSort == "lastPrompt" {
					if !a.LastPromptAt.IsZero() {
						ta = a.LastPromptAt
					}
					if !b.LastPromptAt.IsZero() {
						tb = b.LastPromptAt
					}
				}
				if !ta.Equal(tb) {
					return ta.After(tb)
				}
			} else if a.Name != b.Name {
				return strings.ToLower(a.Name) < strings.ToLower(b.Name)
			}
			return a.Key < b.Key
		})
		if prefs.WorkspaceSort == "manual" {
			section.Rows = sidebarStoredOrder(section.Rows, prefs.WorkspaceOrder[section.Key], func(v sidebarDirectoryRow) string { return v.Key })
		}
		var unpinned []sidebarDirectoryRow
		for _, row := range section.Rows {
			visibleHosts[row.HostID] = true
			p.Workspaces = append(p.Workspaces, row)
			if row.Pinned {
				p.Pinned = append(p.Pinned, row)
			} else {
				unpinned = append(unpinned, row)
			}
		}
		section.Rows = unpinned
		p.Projects = append(p.Projects, section)
		p.ProjectTargets[section.Key] = section.Targets
		for _, target := range section.Targets {
			visibleHosts[target.Host] = true
		}
	}
	p.ShowHostLabels = len(visibleHosts) > 1
	sort.SliceStable(p.Pinned, func(i, j int) bool { return p.Pinned[i].PinnedAt.After(p.Pinned[j].PinnedAt) })
	p.Pinned = sidebarStoredOrder(p.Pinned, prefs.PinnedOrder, func(v sidebarDirectoryRow) string { return v.Key })
	if prefs.GroupMode == "status" {
		for _, bucket := range []string{"needs_input", "failed", "attention", "running", "done"} {
			section := sidebarDirectorySection{Key: sidebarDirectoryKey("status", bucket), Name: sidebarStatusLabel(bucket), Status: bucket}
			for _, row := range p.Workspaces {
				if !row.Pinned && row.Status == bucket {
					section.Rows = append(section.Rows, row)
				}
			}
			sort.SliceStable(section.Rows, func(i, j int) bool {
				a, b := section.Rows[i], section.Rows[j]
				if !a.StatusAt.Equal(b.StatusAt) {
					return a.StatusAt.After(b.StatusAt)
				}
				if a.ProjectName != b.ProjectName {
					return a.ProjectName < b.ProjectName
				}
				if a.Name != b.Name {
					return a.Name < b.Name
				}
				return a.Key < b.Key
			})
			if len(section.Rows) > 0 {
				p.Groups = append(p.Groups, section)
			}
		}
	}
	p.InitialLoading, p.Revalidating = loading && !hydrated, loading && hydrated
	if len(p.Projects) == 0 && len(p.Workspaces) == 0 {
		switch {
		case p.InitialLoading:
			p.Empty = "loading"
		case len(selected) == 0:
			p.Empty = "no-hosts"
		case len(p.Errors) > 0 && !hydrated:
			p.Empty = "failed"
		case len(p.AllProjects) > 0:
			p.Empty = "filtered"
		case len(p.Errors) > 0:
			p.Empty = "failed"
		default:
			p.Empty = "no-projects"
		}
	}
	return p
}

// ensureSidebarDirectory belongs in the frame, even on application destinations.
// It also finishes host-routed context actions after the destination host connects.
func (a *App) ensureSidebarDirectory() {
	s := &a.desk.sidebarDirectory
	s.initialize(a.desk.prefs.SidebarDirectory)
	var ids []string
	for _, host := range a.desk.prefs.Hosts {
		ids = append(ids, host.ID)
	}
	if s.reconcileHosts(ids) {
		a.saveSidebarDirectoryPreferences()
	}
	signature := a.sidebarDirectorySignature()
	if s.signature != signature || !s.loading && (s.loadedAt.IsZero() || time.Since(s.loadedAt) >= 30*time.Second) {
		a.loadSidebarDirectory(signature)
	}
	a.resumeSidebarDirectoryAction()
}
func (a *App) sidebarDirectorySignature() string {
	// Credentials affect reachability even when the saved host registry is unchanged.
	raw, _ := json.Marshal(struct {
		Hosts       any
		Credentials any
		Active      string
		Generation  uint64
		Connected   bool
	}{a.desk.prefs.Hosts, a.desk.credentials, a.desk.prefs.ActiveHost, a.generation, a.connected})
	return string(raw)
}
func (a *App) loadSidebarDirectory(signature string) {
	s := &a.desk.sidebarDirectory
	r := s.begin(signature, context.Background())
	go func() {
		sources, cleanup := a.navigationSources(r.ctx)
		defer cleanup()
		hosts := make([]sidebarDirectoryHost, len(sources))
		var wg sync.WaitGroup
		for i, source := range sources {
			wg.Add(1)
			go func(i int, source navigationSource) {
				defer wg.Done()
				hosts[i] = fetchSidebarDirectoryHost(r.ctx, source)
			}(i, source)
		}
		wg.Wait()
		finish := func() {
			if a.sidebarDirectorySignature() != r.signature {
				return
			}
			if s.accept(r, hosts) {
				if s.reconcileLabels() {
					a.saveSidebarDirectoryPreferences()
				}
				a.resumeSidebarDirectoryAction()
			}
		}
		if a.win != nil {
			a.win.Update(finish)
		} else {
			finish()
		}
	}()
}
func (a *App) refreshSidebarDirectory() {
	a.desk.sidebarDirectory.initialize(a.desk.prefs.SidebarDirectory)
	a.loadSidebarDirectory(a.sidebarDirectorySignature())
}
func (a *App) closeSidebarDirectory() { a.desk.sidebarDirectory.close() }
func (a *App) saveSidebarDirectoryPreferences() {
	a.desk.sidebarDirectory.cached = nil
	a.desk.prefs.SidebarDirectory = a.desk.sidebarDirectory.persisted()
	a.persistSoon()
}

func sidebarMoveKey(order []string, id, before string) []string {
	if id == before {
		return order
	}
	next := make([]string, 0, len(order))
	for _, key := range order {
		if key != id {
			next = append(next, key)
		}
	}
	index := len(next)
	for i, key := range next {
		if key == before {
			index = i
			break
		}
	}
	next = append(next, "")
	copy(next[index+1:], next[index:])
	next[index] = id
	return next
}
func sidebarExtendOrder(order []string, visible []string) []string {
	seen := make(map[string]bool, len(order)+len(visible))
	next := make([]string, 0, len(order)+len(visible))
	for _, key := range order {
		if !seen[key] {
			seen[key] = true
			next = append(next, key)
		}
	}
	for _, key := range visible {
		if !seen[key] {
			seen[key] = true
			next = append(next, key)
		}
	}
	return next
}
func (s *sidebarDirectoryState) moveProject(id, before string) bool {
	p := s.projection()
	if id == before || len(p.ProjectTargets[id]) == 0 || before != "" && len(p.ProjectTargets[before]) == 0 {
		return false
	}
	keys := make([]string, 0, len(p.AllProjects))
	for _, project := range p.AllProjects {
		keys = append(keys, project.Key)
	}
	s.prefs.ProjectOrder = sidebarMoveKey(sidebarExtendOrder(s.prefs.ProjectOrder, keys), id, before)
	s.cached = nil
	return true
}
func (s *sidebarDirectoryState) moveWorkspace(id, before string) bool {
	p := s.projection()
	var row, destination *sidebarDirectoryRow
	for i := range p.Workspaces {
		if p.Workspaces[i].Key == id {
			row = &p.Workspaces[i]
		}
		if p.Workspaces[i].Key == before {
			destination = &p.Workspaces[i]
		}
	}
	if row == nil || id == before || before != "" && destination == nil {
		return false
	}
	if destination != nil && (row.Pinned != destination.Pinned || !row.Pinned && row.ProjectKey != destination.ProjectKey) {
		return false
	}
	var keys []string
	if row.Pinned {
		for _, pinned := range p.Pinned {
			keys = append(keys, pinned.Key)
		}
		s.prefs.PinnedOrder = sidebarMoveKey(sidebarExtendOrder(s.prefs.PinnedOrder, keys), id, before)
	} else {
		for _, item := range p.Workspaces {
			if !item.Pinned && item.ProjectKey == row.ProjectKey {
				keys = append(keys, item.Key)
			}
		}
		if s.prefs.WorkspaceOrder == nil {
			s.prefs.WorkspaceOrder = map[string][]string{}
		}
		s.prefs.WorkspaceOrder[row.ProjectKey] = sidebarMoveKey(sidebarExtendOrder(s.prefs.WorkspaceOrder[row.ProjectKey], keys), id, before)
		s.prefs.WorkspaceSort = "manual"
	}
	s.cached = nil
	return true
}
func (a *App) moveSidebarDirectoryProject(id, before string) {
	if a.desk.sidebarDirectory.moveProject(id, before) {
		a.saveSidebarDirectoryPreferences()
	}
}
func (a *App) moveSidebarDirectoryWorkspace(id, before string) {
	if a.desk.sidebarDirectory.moveWorkspace(id, before) {
		a.saveSidebarDirectoryPreferences()
	}
}

func (a *App) sidebarDirectoryProjectTarget(targets []sidebarDirectoryTarget, run func(sidebarDirectoryTarget)) {
	if len(targets) == 1 {
		run(targets[0])
		return
	}
	if len(targets) > 1 {
		s := &a.desk.sidebarDirectory
		s.projectChoice = targets
		s.projectChoiceAction = run
	}
}
func (a *App) sidebarDirectoryRoute(target sidebarDirectoryTarget, workspace bool, action func(string)) {
	if action == nil {
		return
	}
	if target.Host == a.desk.prefs.ActiveHost && a.connected {
		if workspace {
			if _, ok := a.navigationWorkspace(target.ID); !ok {
				a.desk.notice = "Workspace is no longer available. Refresh the list."
				return
			}
		}
		if !workspace {
			project, ok := a.navigationProject(target.ID)
			if !ok || target.Root != "" && project.Root != target.Root {
				a.desk.notice = "Project is no longer available. Refresh the list."
				return
			}
		}
		action(target.ID)
		return
	}
	start := func() {
		a.forceSelectHost(target.Host)
		a.desk.sidebarDirectory.pending = &sidebarDirectoryPending{target: target, workspace: workspace, action: action, expires: time.Now().Add(45 * time.Second)}
	}
	for _, panel := range a.desk.supporting {
		if panel.HasUnsavedWork() {
			a.confirm("Switch host and discard unsaved file changes?", start)
			return
		}
	}
	start()
}
func (a *App) resumeSidebarDirectoryAction() {
	s := &a.desk.sidebarDirectory
	pending := s.pending
	if pending == nil {
		return
	}
	if time.Now().After(pending.expires) {
		s.pending = nil
		a.desk.notice = "Could not connect to the workspace host. Try the action again after connecting."
		return
	}
	if a.desk.prefs.ActiveHost != pending.target.Host {
		s.pending = nil
		return
	}
	if !a.connected || a.workspaces == nil {
		return
	}
	if pending.workspace {
		if _, ok := a.navigationWorkspace(pending.target.ID); !ok {
			return
		}
	} else {
		project, ok := a.navigationProject(pending.target.ID)
		if !ok || pending.target.Root != "" && project.Root != pending.target.Root {
			return
		}
	}
	s.pending = nil
	pending.action(pending.target.ID)
}

func (a *App) applySidebarDirectory(props *desktopui.ShellProps, actions *desktopui.ShellActions) {
	s := &a.desk.sidebarDirectory
	s.initialize(a.desk.prefs.SidebarDirectory)
	p := s.projection()
	props.Projects, props.Workspaces = nil, nil
	props.SidebarLoading, props.SidebarEmpty = p.Loading, p.Empty
	props.SidebarHeader, props.SidebarStatus = a.sidebarDirectoryControls, a.sidebarDirectoryErrors
	props.PreserveWorkspaceOrder = true
	props.ActiveWorkspace = sidebarDirectoryKey("workspace", a.desk.prefs.ActiveHost, a.desk.workspace)
	appendSection := func(section sidebarDirectorySection) {
		props.Projects = append(props.Projects, desktopui.Project{ID: section.Key, Name: section.Name, Group: len(section.Targets) == 0})
		for _, row := range section.Rows {
			name := row.Name
			if p.ShowHostLabels {
				host := row.HostName
				if host == "" {
					host = row.HostID
				}
				name += " · " + host
			}
			props.Workspaces = append(props.Workspaces, desktopui.Workspace{ID: row.Key, ProjectID: section.Key, Project: row.ProjectName, Name: name, Branch: row.Branch, Status: sidebarStatusLabel(row.Status), Pinned: row.Pinned, Unread: row.Unread})
		}
	}
	if len(p.Pinned) > 0 {
		appendSection(sidebarDirectorySection{Key: sidebarDirectoryKey("pinned"), Name: "Pinned", Rows: p.Pinned})
	}
	sections := p.Projects
	if s.prefs.GroupMode == "status" {
		sections = p.Groups
	}
	for _, section := range sections {
		appendSection(section)
	}
	actions.SelectWorkspace = func(key string) {
		if target, ok := p.WorkspaceTargets[key]; ok {
			a.openCreatedWorkspace(target.Host, target.ID)
		}
	}
	workspaceAction := func(original func(string)) func(string) {
		if original == nil {
			return nil
		}
		return func(key string) {
			if target, ok := p.WorkspaceTargets[key]; ok {
				a.sidebarDirectoryRoute(target, true, original)
			}
		}
	}
	projectAction := func(original func(string)) func(string) {
		if original == nil {
			return nil
		}
		return func(key string) {
			a.sidebarDirectoryProjectTarget(p.ProjectTargets[key], func(target sidebarDirectoryTarget) { a.sidebarDirectoryRoute(target, false, original) })
		}
	}
	actions.RenameWorkspace = workspaceAction(actions.RenameWorkspace)
	actions.CopyWorkspacePath = workspaceAction(actions.CopyWorkspacePath)
	actions.CopyWorkspaceBranch = workspaceAction(actions.CopyWorkspaceBranch)
	actions.MarkWorkspaceRead = workspaceAction(actions.MarkWorkspaceRead)
	actions.MarkWorkspaceUnread = workspaceAction(actions.MarkWorkspaceUnread)
	actions.ToggleWorkspacePin = workspaceAction(actions.ToggleWorkspacePin)
	actions.ArchiveWorkspace = workspaceAction(actions.ArchiveWorkspace)
	actions.OpenWorkspaceFolder = workspaceAction(actions.OpenWorkspaceFolder)
	actions.ProjectSettings = projectAction(actions.ProjectSettings)
	actions.OpenProjectFolder = projectAction(actions.OpenProjectFolder)
	actions.OpenProjectInNewWindow = projectAction(actions.OpenProjectInNewWindow)
	actions.RemoveProject = projectAction(actions.RemoveProject)
	actions.RenameProject = projectAction(actions.RenameProject)
	actions.CreateWorkspace = func(key string) {
		a.sidebarDirectoryProjectTarget(p.ProjectTargets[key], func(target sidebarDirectoryTarget) { a.openNewWorkspaceContext(target.Host, target.ID, target.Root) })
	}
	actions.ReorderSidebarProject = a.moveSidebarDirectoryProject
	actions.ReorderSidebarWorkspace = a.moveSidebarDirectoryWorkspace
}

// The shell owns row rendering. This small native menu owns the complete persisted
// view selection, so a second single-project filter must not run afterward.
func (a *App) sidebarDirectoryControls(c *ui.Context) {
	s := &a.desk.sidebarDirectory
	p := s.projection()
	ui.Row(c).Height(28).Padding(0, 8).Children(func() {
		ui.Text(c, "Workspaces").FontSize(12).TextColor(c.Theme().TextMuted).Grow(1)
		ui.Button(c.Key("sidebar-directory-display"), "Display").PopoverMenuButton(func(m *ui.Menu) {
			item := func(label string, selected bool) bool {
				if selected {
					label = "✓ " + label
				}
				return m.Item(label).Chosen()
			}
			if item("Group by project", s.prefs.GroupMode == "project") {
				s.prefs.GroupMode = "project"
				a.saveSidebarDirectoryPreferences()
			}
			if item("Group by status", s.prefs.GroupMode == "status") {
				s.prefs.GroupMode = "status"
				a.saveSidebarDirectoryPreferences()
			}
			for _, choice := range []struct{ key, label string }{{"created", "Sort by created"}, {"lastPrompt", "Sort by last prompt"}, {"manual", "Manual order"}} {
				if choice.key != "manual" && !p.SortSupported {
					continue
				}
				if item(choice.label, s.prefs.WorkspaceSort == choice.key) {
					s.prefs.WorkspaceSort = choice.key
					a.saveSidebarDirectoryPreferences()
				}
			}
			if item("Show background", s.prefs.ShowBackground) {
				s.prefs.ShowBackground = !s.prefs.ShowBackground
				a.saveSidebarDirectoryPreferences()
			}
			if item("All hosts", len(s.prefs.HostFilters) == 0) {
				s.prefs.HostFilters = nil
				a.saveSidebarDirectoryPreferences()
			}
			for _, host := range a.desk.prefs.Hosts {
				label := host.Name
				if label == "" {
					label = host.ID
				}
				if item("Host: "+label, sidebarContains(s.prefs.HostFilters, host.ID)) {
					s.prefs.HostFilters = sidebarToggle(s.prefs.HostFilters, host.ID)
					a.saveSidebarDirectoryPreferences()
				}
			}
			if item("All projects", len(p.ResolvedProjectFilters) == 0) {
				s.prefs.ProjectFilters = nil
				a.saveSidebarDirectoryPreferences()
			}
			for _, project := range p.AllProjects {
				if item("Project: "+project.Name, sidebarContains(p.ResolvedProjectFilters, project.Key)) {
					s.prefs.ProjectFilters = sidebarToggle(s.prefs.ProjectFilters, project.Key)
					a.saveSidebarDirectoryPreferences()
				}
			}
			if item("All labels", len(s.prefs.Labels) == 0) {
				s.prefs.Labels = nil
				a.saveSidebarDirectoryPreferences()
			}
			labels := map[string]string{"": "Unlabelled"}
			for _, host := range s.hosts {
				for _, label := range host.Labels {
					if key := sidebarLabelKey(label); key != "" {
						labels[key] = strings.Join(strings.Fields(label), " ")
					}
				}
				for _, w := range host.Workspaces {
					for _, label := range w.Labels {
						if key := sidebarLabelKey(label); key != "" {
							labels[key] = strings.Join(strings.Fields(label), " ")
						}
					}
				}
			}
			for _, key := range s.prefs.Labels {
				if _, exists := labels[key]; !exists {
					labels[key] = key
				}
			}
			keys := make([]string, 0, len(labels))
			for key := range labels {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if item("Label: "+labels[key], sidebarContains(s.prefs.Labels, key)) {
					s.prefs.Labels = sidebarToggle(s.prefs.Labels, key)
					a.saveSidebarDirectoryPreferences()
				}
			}
			if m.Item("Refresh workspaces").Chosen() {
				a.refreshSidebarDirectory()
			}
		})
	})
}
func (a *App) sidebarDirectoryErrors(c *ui.Context) {
	p := a.desk.sidebarDirectory.projection()
	s := &a.desk.sidebarDirectory
	if len(s.projectChoice) > 0 {
		ui.Column(c).Padding(8).Gap(4).Children(func() {
			ui.Text(c, "Choose project host").FontSize(12)
			for _, target := range s.projectChoice {
				name := target.Host
				for _, host := range a.desk.prefs.Hosts {
					if host.ID == target.Host && host.Name != "" {
						name = host.Name
						break
					}
				}
				if ui.Button(c.Key(sidebarDirectoryKey("project-host", target.Host, target.ID)), name).Clicked() {
					run := s.projectChoiceAction
					s.projectChoice, s.projectChoiceAction = nil, nil
					if run != nil {
						run(target)
					}
				}
			}
			if ui.Button(c, "Cancel host choice").Clicked() {
				s.projectChoice, s.projectChoiceAction = nil, nil
			}
		})
	}
	if p.Loading {
		label := "Loading workspaces…"
		if p.Revalidating {
			label = "Refreshing workspaces…"
		}
		ui.Text(c, label).Padding(4, 8).FontSize(12).TextColor(c.Theme().TextMuted)
	}
	for _, issue := range p.Errors {
		host := issue.HostName
		if host == "" {
			host = issue.HostID
		}
		ui.Text(c.Key(sidebarDirectoryKey("error", issue.HostID, issue.Area)), host+" · "+issue.Area+": "+issue.Message).Padding(4, 8).FontSize(12).TextColor(c.Theme().TextMuted)
	}
	switch p.Empty {
	case "filtered":
		ui.Text(c, "No workspaces match these filters.").Padding(8).FontSize(12)
		if ui.Button(c, "Clear filters").Clicked() {
			s := &a.desk.sidebarDirectory
			s.prefs.HostFilters, s.prefs.ProjectFilters, s.prefs.Labels = nil, nil, nil
			a.saveSidebarDirectoryPreferences()
		}
	case "failed":
		ui.Text(c, "Could not load workspaces.").Padding(8).FontSize(12)
		if ui.Button(c, "Retry workspaces").Clicked() {
			a.refreshSidebarDirectory()
		}
	case "no-hosts":
		ui.Text(c, "Add a host to see its projects.").Padding(8).FontSize(12)
	}
}
