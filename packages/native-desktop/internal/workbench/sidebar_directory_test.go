package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/getpaseo/paseo/packages/native-desktop/internal/desktopui"
)

// Collapsing identity to the daemon-local ID would open the wrong host here.
func TestSidebarDirectoryCollidingWorkspaceIDsKeepHostIdentity(t *testing.T) {
	hosts := []sidebarDirectoryHost{
		{ID: "host:a", Name: "A", WorkspacesLoaded: true, ProjectsLoaded: true, Projects: []sidebarDirectoryProject{{Project: Project{ID: "p", Name: "App"}}}, Workspaces: []sidebarDirectoryWorkspace{{Workspace: Workspace{ID: "same:id", ProjectID: "p", Name: "A chat"}, Status: "running"}}},
		{ID: "host", Name: "B", WorkspacesLoaded: true, ProjectsLoaded: true, Projects: []sidebarDirectoryProject{{Project: Project{ID: "p", Name: "App"}}}, Workspaces: []sidebarDirectoryWorkspace{{Workspace: Workspace{ID: "a:same:id", ProjectID: "p", Name: "B chat"}, Status: "done"}}},
	}
	p := projectSidebarDirectory(hosts, defaultSidebarDirectoryPreferences(), false)
	if len(p.Workspaces) != 2 || p.Workspaces[0].Key == p.Workspaces[1].Key {
		t.Fatalf("host identity lost: %+v", p.Workspaces)
	}
	for _, row := range p.Workspaces {
		ref, ok := p.WorkspaceTargets[row.Key]
		if !ok || ref.Host != row.HostID || ref.ID != row.ID {
			t.Fatalf("wrong route: %+v %+v", row, ref)
		}
	}
	if _, ok := p.WorkspaceTargets["same:id"]; ok {
		t.Fatal("unknown raw ID accepted as a route")
	}
	if len(p.Projects) != 2 || !p.ShowHostLabels {
		t.Fatalf("host-local projects merged: %+v", p.Projects)
	}
}

func TestSidebarDirectorySharedProjectGroupsAndPins(t *testing.T) {
	hosts := []sidebarDirectoryHost{
		{ID: "a", ProjectsLoaded: true, WorkspacesLoaded: true, Projects: []sidebarDirectoryProject{{Project: Project{ID: "pa", Name: "App"}, SharedKey: "remote:app"}}, Workspaces: []sidebarDirectoryWorkspace{
			{Workspace: Workspace{ID: "old", ProjectID: "pa", Name: "Old", PinnedAt: new("2026-01-01T00:00:00Z")}, Status: "done"},
			{Workspace: Workspace{ID: "run", ProjectID: "pa", Name: "Running"}, Status: "running"},
		}},
		{ID: "b", ProjectsLoaded: true, WorkspacesLoaded: true, Projects: []sidebarDirectoryProject{{Project: Project{ID: "pb", Name: "App"}, SharedKey: "remote:app"}}, Workspaces: []sidebarDirectoryWorkspace{
			{Workspace: Workspace{ID: "new", ProjectID: "pb", Name: "New", PinnedAt: new("2026-02-01T00:00:00Z")}, Status: "needs_input"},
			{Workspace: Workspace{ID: "review", ProjectID: "pb", Name: "Review"}, Status: "attention"},
		}},
	}
	prefs := defaultSidebarDirectoryPreferences()
	p := projectSidebarDirectory(hosts, prefs, false)
	if len(p.AllProjects) != 1 || len(p.AllProjects[0].Targets) != 2 {
		t.Fatalf("shared project not grouped: %+v", p.AllProjects)
	}
	if len(p.Pinned) != 2 || p.Pinned[0].ID != "new" || len(p.Projects[0].Rows) != 2 {
		t.Fatalf("pin split/order: %+v", p)
	}
	prefs.PinnedOrder = []string{p.Pinned[1].Key, p.Pinned[0].Key}
	prefs.GroupMode = "status"
	p = projectSidebarDirectory(hosts, prefs, false)
	if p.Pinned[0].ID != "old" {
		t.Fatal("saved pinned ordering ignored")
	}
	if len(p.Groups) != 2 || p.Groups[0].Status != "attention" || p.Groups[1].Status != "running" {
		t.Fatalf("status order: %+v", p.Groups)
	}
}

func TestSidebarDirectoryFiltersRetainStoredProjectsAcrossHosts(t *testing.T) {
	hosts := []sidebarDirectoryHost{
		{ID: "a", ProjectsLoaded: true, WorkspacesLoaded: true, Projects: []sidebarDirectoryProject{{Project: Project{ID: "p", Name: "A"}}, {Project: Project{ID: "empty", Name: "Empty"}}}, Workspaces: []sidebarDirectoryWorkspace{{Workspace: Workspace{ID: "w", ProjectID: "p", Name: "Chat"}, Labels: []string{"  Needs   Review  "}}, {Workspace: Workspace{ID: "bg", ProjectID: "p"}, Background: true}}},
		{ID: "b", ProjectsLoaded: true, WorkspacesLoaded: true, Projects: []sidebarDirectoryProject{{Project: Project{ID: "p", Name: "B"}}}, Workspaces: []sidebarDirectoryWorkspace{{Workspace: Workspace{ID: "w", ProjectID: "p"}}}},
	}
	prefs := defaultSidebarDirectoryPreferences()
	base := projectSidebarDirectory(hosts, prefs, false)
	keyA, keyEmpty := base.AllProjects[0].Key, base.AllProjects[2].Key
	prefs.ProjectFilters = []string{keyA}
	prefs.HostFilters = []string{"b"}
	p := projectSidebarDirectory(hosts, prefs, false)
	if len(p.Projects) != 1 || p.Projects[0].Name != "B" || len(p.ResolvedProjectFilters) != 0 {
		t.Fatalf("away project filter must become inert: %+v", p)
	}
	if !reflect.DeepEqual(prefs.ProjectFilters, []string{keyA}) {
		t.Fatal("project preference destroyed while host away")
	}
	prefs.HostFilters = nil
	prefs.Labels = []string{"needs review"}
	p = projectSidebarDirectory(hosts, prefs, false)
	if len(p.Workspaces) != 1 || p.Workspaces[0].ID != "w" || p.Workspaces[0].HostID != "a" {
		t.Fatalf("normalized any-label filtering: %+v", p.Workspaces)
	}
	prefs.ProjectFilters = []string{keyEmpty}
	prefs.Labels = nil
	p = projectSidebarDirectory(hosts, prefs, false)
	if len(p.Projects) != 1 || len(p.Workspaces) != 0 || p.Empty != "" {
		t.Fatalf("selected empty project lost: %+v", p)
	}
	prefs.ProjectFilters = nil
	prefs.Labels = []string{""}
	p = projectSidebarDirectory(hosts, prefs, false)
	if len(p.Workspaces) != 1 || p.Workspaces[0].HostID != "b" {
		t.Fatalf("unlabelled matched hidden background or labelled chat: %+v", p.Workspaces)
	}
}

func TestSidebarDirectoryRejectsStaleAndClosedLoads(t *testing.T) {
	var s sidebarDirectoryState
	s.initialize(nil)
	r1 := s.begin("hosts-one", context.Background())
	r2 := s.begin("hosts-two", context.Background())
	if s.accept(r1, []sidebarDirectoryHost{{ID: "old"}}) {
		t.Fatal("old request replaced newer hosts")
	}
	if !s.accept(r2, []sidebarDirectoryHost{{ID: "new", ProjectsLoaded: true, WorkspacesLoaded: true}}) {
		t.Fatal("current request rejected")
	}
	r3 := s.begin("hosts-two", context.Background())
	s.close()
	if s.accept(r3, []sidebarDirectoryHost{{ID: "late"}}) || s.loading {
		t.Fatal("closed load repopulated state")
	}
	if len(s.hosts) != 1 || s.hosts[0].ID != "new" {
		t.Fatalf("close discarded last directory: %+v", s.hosts)
	}
}

func TestSidebarDirectoryPartialFailuresPreserveSuccessfulAndCachedData(t *testing.T) {
	var s sidebarDirectoryState
	s.initialize(nil)
	r := s.begin("hosts", context.Background())
	s.accept(r, []sidebarDirectoryHost{{ID: "a", ProjectsLoaded: true, WorkspacesLoaded: true, Projects: []sidebarDirectoryProject{{Project: Project{ID: "p", Name: "App"}}}, Workspaces: []sidebarDirectoryWorkspace{{Workspace: Workspace{ID: "w", ProjectID: "p"}}}}, {ID: "b", Error: "offline"}})
	p := s.projection()
	if len(p.Workspaces) != 1 || len(p.Errors) != 1 || p.Empty != "" {
		t.Fatalf("partial failure erased data: %+v", p)
	}
	r = s.begin("hosts", context.Background())
	s.accept(r, []sidebarDirectoryHost{{ID: "a", WorkspaceError: "request failed", ProjectError: "request failed"}, {ID: "b", Error: "offline"}})
	p = s.projection()
	if len(p.Workspaces) != 1 || !p.Workspaces[0].Stale || p.Empty != "" {
		t.Fatalf("revalidation lost cached rows: %+v", p)
	}
	failed := projectSidebarDirectory([]sidebarDirectoryHost{{ID: "x", Error: "offline"}}, defaultSidebarDirectoryPreferences(), false)
	if failed.Empty != "failed" {
		t.Fatalf("failed host shown as no projects: %q", failed.Empty)
	}
	empty := projectSidebarDirectory([]sidebarDirectoryHost{{ID: "x", ProjectsLoaded: true, WorkspacesLoaded: true}}, defaultSidebarDirectoryPreferences(), false)
	if empty.Empty != "no-projects" {
		t.Fatalf("real empty host: %q", empty.Empty)
	}
}

func TestSidebarDirectoryFetchUsesRealPagesAndIndependentProjectResults(t *testing.T) {
	calls := map[string]int{}
	c := directoryCaller(func(_ context.Context, typ string, payload json.RawMessage) (json.RawMessage, error) {
		calls[typ]++
		switch typ {
		case "project.list.request":
			return json.RawMessage(`{"projects":[{"projectId":"empty","projectDisplayName":"Empty"}],"error":null}`), nil
		case "fetch_workspaces_request":
			var req struct {
				Page struct {
					Cursor string `json:"cursor"`
				} `json:"page"`
			}
			_ = json.Unmarshal(payload, &req)
			if req.Page.Cursor == "" {
				return json.RawMessage(`{"entries":[{"id":"one","projectId":"p","name":"One","labels":["Ready"],"status":"attention"}],"pageInfo":{"hasMore":true,"nextCursor":"next"}}`), nil
			}
			if req.Page.Cursor != "next" {
				t.Fatalf("wrong cursor %q", req.Page.Cursor)
			}
			return json.RawMessage(`{"entries":[{"id":"two","projectId":"p","name":"Two","status":"done"}],"pageInfo":{"hasMore":false,"nextCursor":null}}`), nil
		case "fetch_agents_request":
			return nil, errors.New("agents offline")
		case "workspace.label.list.request":
			return json.RawMessage(`{"labels":[{"name":"Ready","color":"sky"}]}`), nil
		default:
			t.Fatalf("unexpected RPC %s", typ)
			return nil, nil
		}
	})
	h := fetchSidebarDirectoryHost(context.Background(), navigationSource{ID: "h", Client: c, Info: json.RawMessage(`{"features":{"workspaceLabels":true,"workspaceSortTimes":true}}`)})
	if !h.WorkspacesLoaded || !h.ProjectsLoaded || len(h.Workspaces) != 2 || len(h.Workspaces[0].Labels) != 1 || h.AgentError == "" || !h.LabelsLoaded {
		t.Fatalf("independent fetch lost successful data: %+v", h)
	}
	if calls["fetch_workspaces_request"] != 2 {
		t.Fatal("directory stopped before final page")
	}
	p := projectSidebarDirectory([]sidebarDirectoryHost{h}, defaultSidebarDirectoryPreferences(), false)
	if len(p.Projects) != 2 || len(p.Workspaces) != 2 || len(p.Errors) != 1 {
		t.Fatalf("empty project or partial workspace data lost: %+v", p)
	}
}

func TestSidebarDirectoryPersistenceReconcilesOnlyAuthoritativeFilters(t *testing.T) {
	var s sidebarDirectoryState
	s.initialize(json.RawMessage(`{"hostFilters":["gone","a"],"projectFilters":["remember"],"labels":["  NEEDS   Review ","", "deleted"]}`))
	s.reconcileHosts([]string{"a"})
	if !reflect.DeepEqual(s.prefs.HostFilters, []string{"a"}) {
		t.Fatalf("removed hosts not pruned: %+v", s.prefs)
	}
	s.hosts = []sidebarDirectoryHost{{ID: "a", LabelSupported: true, LabelError: "offline"}}
	s.reconcileLabels()
	if len(s.prefs.Labels) != 3 {
		t.Fatal("failed label catalog destroyed filters")
	}
	s.hosts[0].LabelError = ""
	s.hosts[0].LabelsLoaded = true
	s.hosts[0].Labels = []string{"Needs Review"}
	s.reconcileLabels()
	if !reflect.DeepEqual(s.prefs.Labels, []string{"needs review", ""}) || !reflect.DeepEqual(s.prefs.ProjectFilters, []string{"remember"}) {
		t.Fatalf("filter reconciliation: %+v", s.prefs)
	}
	var restored sidebarDirectoryState
	restored.initialize(s.persisted())
	if !reflect.DeepEqual(restored.prefs, s.prefs) {
		t.Fatalf("preferences did not survive reopen: %+v", restored.prefs)
	}
}

func TestSidebarDirectoryStatusUsesWorkspaceStateAndStableTimeOrdering(t *testing.T) {
	h := sidebarDirectoryHost{ID: "h", WorkspacesLoaded: true, ProjectsLoaded: true, Workspaces: []sidebarDirectoryWorkspace{
		{Workspace: Workspace{ID: "old", ProjectID: "p", Name: "A"}, Status: "attention", StatusEnteredAt: new("2026-01-01T00:00:00Z")},
		{Workspace: Workspace{ID: "new", ProjectID: "p", Name: "Z"}, Status: "attention", StatusEnteredAt: new("2026-02-01T00:00:00Z")},
	}}
	prefs := defaultSidebarDirectoryPreferences()
	prefs.GroupMode = "status"
	p := projectSidebarDirectory([]sidebarDirectoryHost{h}, prefs, false)
	if len(p.Groups) != 1 || p.Groups[0].Rows[0].ID != "new" {
		t.Fatalf("status time order: %+v", p.Groups)
	}
	if p.Groups[0].Rows[0].StatusAt.IsZero() || p.Groups[0].Rows[0].StatusAt.Before(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("status timestamp lost")
	}
}

func TestSidebarDirectoryShellActionsRouteExactHostAndRejectUnknownKeys(t *testing.T) {
	a := &App{connected: true, workspaces: []Workspace{{ID: "w", ProjectID: "p"}}}
	a.desk.prefs.ActiveHost = "b"
	a.desk.sidebarDirectory.initialize(nil)
	a.desk.sidebarDirectory.hosts = []sidebarDirectoryHost{
		{ID: "a", ProjectsLoaded: true, WorkspacesLoaded: true, Workspaces: []sidebarDirectoryWorkspace{{Workspace: Workspace{ID: "w", ProjectID: "p", Name: "Other"}}}},
		{ID: "b", ProjectsLoaded: true, WorkspacesLoaded: true, Workspaces: []sidebarDirectoryWorkspace{{Workspace: Workspace{ID: "w", ProjectID: "p", Name: "Current"}}}},
	}
	var props desktopui.ShellProps
	var called string
	actions := desktopui.ShellActions{RenameWorkspace: func(id string) { called = id }}
	a.applySidebarDirectory(&props, &actions)
	actions.RenameWorkspace("w")
	if called != "" {
		t.Fatal("raw local ID accepted")
	}
	var currentKey string
	for _, w := range props.Workspaces {
		if w.Name == "Current · b" {
			currentKey = w.ID
		}
	}
	if currentKey == "" {
		t.Fatalf("host identity missing from shell: %+v", props.Workspaces)
	}
	actions.RenameWorkspace(currentKey)
	if called != "w" {
		t.Fatalf("shell key leaked into daemon operation: %q", called)
	}
}

func TestSidebarDirectoryReorderKeepsHostQualifiedWorkspaceSlots(t *testing.T) {
	var s sidebarDirectoryState
	s.initialize(nil)
	s.hosts = []sidebarDirectoryHost{{ID: "h", ProjectsLoaded: true, WorkspacesLoaded: true, Workspaces: []sidebarDirectoryWorkspace{
		{Workspace: Workspace{ID: "one", ProjectID: "p", Name: "One"}},
		{Workspace: Workspace{ID: "two", ProjectID: "p", Name: "Two"}},
	}}}
	p := s.projection()
	if !s.moveWorkspace(p.Workspaces[1].Key, p.Workspaces[0].Key) {
		t.Fatal("valid reorder rejected")
	}
	p = s.projection()
	if p.Workspaces[0].ID != "two" || s.prefs.WorkspaceSort != "manual" {
		t.Fatalf("manual ordering not applied: %+v", p.Workspaces)
	}
	if s.moveWorkspace("one", "two") {
		t.Fatal("unknown IDs accepted for reorder")
	}
}

func TestSidebarDirectoryProjectChoiceRequiresExplicitHost(t *testing.T) {
	a := &App{}
	targets := []sidebarDirectoryTarget{{Host: "a", ID: "p"}, {Host: "b", ID: "p"}}
	var selected string
	a.sidebarDirectoryProjectTarget(targets, func(target sidebarDirectoryTarget) { selected = target.Host })
	if selected != "" || len(a.desk.sidebarDirectory.projectChoice) != 2 {
		t.Fatal("project action silently chose a host")
	}
	a.desk.sidebarDirectory.projectChoiceAction(targets[1])
	if selected != "b" {
		t.Fatal("explicit host choice was not routed")
	}
}
