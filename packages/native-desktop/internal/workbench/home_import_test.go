package workbench

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/getpaseo/paseo/packages/native-desktop/internal/client"
)

func TestHomeImportListsActualProviderSessions(t *testing.T) {
	var request map[string]any
	call := directoryCaller(func(_ context.Context, typ string, raw json.RawMessage) (json.RawMessage, error) {
		if typ != "fetch_recent_provider_sessions_request" {
			t.Fatalf("request %s", typ)
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Fatal(err)
		}
		return json.RawMessage(`{
			"requestId":"listed",
			"entries":[
				{"providerId":"claude","providerLabel":"Claude Code","providerHandleId":"session-a","cwd":"/repo/app","title":null,"firstPromptPreview":"first prompt","lastPromptPreview":"last prompt","lastActivityAt":"2026-11-05T10:00:00Z"},
				{"providerId":"codex","providerLabel":"Codex","providerHandleId":"session-b","cwd":"/repo/lib","title":"Fix the parser","firstPromptPreview":"older","lastPromptPreview":"newer","lastActivityAt":"2026-11-05T12:00:00Z"}
			],
			"filteredAlreadyImportedCount":2,
			"providerErrors":[{"provider":"gemini","message":"not installed"}]
		}`), nil
	})
	listing, err := fetchHomeImportSessions(context.Background(), call, 15, "  parser ", nil)
	if err != nil {
		t.Fatal(err)
	}
	if request["query"] != "parser" || request["limit"] != float64(15) {
		t.Fatalf("request = %#v", request)
	}
	if _, ok := request["cwd"]; ok {
		t.Fatal("home listing narrowed to a workspace directory")
	}
	if _, ok := request["providers"]; ok {
		t.Fatal("home listing silently dropped providers")
	}
	if len(listing.entries) != 2 {
		t.Fatalf("entries = %#v", listing.entries)
	}
	if listing.entries[0].ProviderID != "codex" || listing.entries[0].ProviderHandleID != "session-b" {
		t.Fatalf("most recent session not first: %#v", listing.entries)
	}
	if listing.entries[1].ProviderID != "claude" || listing.entries[1].ProviderHandleID != "session-a" ||
		listing.entries[1].CWD != "/repo/app" || listing.entries[1].ProviderLabel != "Claude Code" {
		t.Fatalf("listed identity lost: %#v", listing.entries[1])
	}
	if listing.alreadyImported != 2 || len(listing.providerErrors) != 1 || listing.providerErrors[0].Provider != "gemini" {
		t.Fatalf("listing metadata = %#v", listing)
	}
}

func TestHomeImportRejectsMalformedSessionLists(t *testing.T) {
	for _, payload := range []string{
		`{}`,
		`{"entries":null}`,
		`{"entries":[{"providerId":"claude","cwd":"/repo"}]}`,
		`{"entries":[{"providerHandleId":"session-a","cwd":"/repo"}]}`,
		`not json`,
	} {
		call := directoryCaller(func(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(payload), nil
		})
		if _, err := fetchHomeImportSessions(context.Background(), call, 15, "", nil); err == nil {
			t.Fatalf("accepted malformed listing %s", payload)
		}
	}
}

func TestHomeImportUsesListedProviderSessionAsTarget(t *testing.T) {
	var request map[string]string
	call := directoryCaller(func(_ context.Context, typ string, raw json.RawMessage) (json.RawMessage, error) {
		if typ != "import_agent_request" {
			t.Fatalf("request %s", typ)
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Fatal(err)
		}
		return json.RawMessage(`{
			"requestId":"imported",
			"status":"agent_resumed",
			"agentId":"agent-9",
			"agent":{"id":"agent-9","workspaceId":"workspace-2","cwd":"/repo/app"}
		}`), nil
	})
	entry := homeImportEntry{
		ProviderID:       "claude",
		ProviderHandleID: "session-7",
		CWD:              "/repo/app",
		Title:            "Imported work",
	}
	result, err := importHomeSessionRequest(context.Background(), call, entry)
	if err != nil {
		t.Fatal(err)
	}
	if request["providerId"] != "claude" || request["providerHandleId"] != "session-7" || request["cwd"] != "/repo/app" {
		t.Fatalf("import request = %#v", request)
	}
	if _, invented := request["workspaceId"]; invented {
		t.Fatal("home import invented a workspace target")
	}
	if result.unknown || result.failure != "" || result.agentID != "agent-9" || result.workspaceID != "workspace-2" {
		t.Fatalf("result = %#v", result)
	}
}

func TestHomeImportRefusesSessionWithoutWorkingDirectory(t *testing.T) {
	called := false
	call := directoryCaller(func(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		called = true
		return nil, nil
	})
	if _, err := importHomeSessionRequest(context.Background(), call, homeImportEntry{ProviderID: "claude", ProviderHandleID: "session-7"}); err == nil {
		t.Fatal("accepted a session without a working directory")
	}
	if called {
		t.Fatal("sent an import without a working directory")
	}
}

func TestHomeImportClassifiesDaemonAcknowledgements(t *testing.T) {
	for _, tc := range []struct {
		name        string
		ack         string
		wantFailure bool
		wantUnknown bool
		wantAgent   string
	}{
		{"resumed", `{"status":"agent_resumed","agentId":"agent-2","agent":{"id":"agent-2","workspaceId":"w2"}}`, false, false, "agent-2"},
		{"resumed with only agent payload", `{"status":"agent_resumed","agent":{"id":"agent-3","workspaceId":"w3"}}`, false, false, "agent-3"},
		{"conflicting agent identities", `{"status":"agent_resumed","agentId":"agent-2","agent":{"id":"agent-3"}}`, false, true, ""},
		{"rejected", `{"status":"agent_create_failed","error":"provider rejected the session"}`, true, false, ""},
		{"rejected without reason", `{"status":"agent_create_failed"}`, true, false, ""},
		{"missing status", `{}`, false, true, ""},
		{"unknown status", `{"status":"queued"}`, false, true, ""},
		{"resumed without agent", `{"status":"agent_resumed"}`, false, true, ""},
		{"malformed", `{"status":`, false, true, ""},
		{"empty", ``, false, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := resolveHomeImportAck(json.RawMessage(tc.ack))
			if result.unknown != tc.wantUnknown {
				t.Fatalf("unknown = %v", result.unknown)
			}
			if (result.failure != "") != tc.wantFailure {
				t.Fatalf("failure = %q", result.failure)
			}
			if result.agentID != tc.wantAgent {
				t.Fatalf("agent = %q", result.agentID)
			}
			if result.unknown && (result.failure != "" || result.agentID != "") {
				t.Fatalf("unknown result also claimed a definite outcome: %#v", result)
			}
		})
	}
}

func TestHomeImportPlacementUsesImportedAgentsWorkspace(t *testing.T) {
	agents := []Agent{
		{ID: "current", WorkspaceID: "w1"},
		{ID: "imported", WorkspaceID: "w2"},
	}
	workspaces := []Workspace{{ID: "w1"}, {ID: "w2"}}
	agent, ok := homeImportPlacement(agents, workspaces, "imported", "w2")
	if !ok || agent.ID != "imported" || agent.WorkspaceID != "w2" {
		t.Fatalf("placement = %#v %v", agent, ok)
	}
	if _, ok := homeImportPlacement(agents, []Workspace{{ID: "w1"}}, "imported", "w2"); ok {
		t.Fatal("opened the imported agent in a workspace the directory does not know")
	}
	if _, ok := homeImportPlacement(agents, workspaces, "missing", "w2"); ok {
		t.Fatal("placed an agent that is not in the directory")
	}
	fallback := []Agent{{ID: "imported"}}
	agent, ok = homeImportPlacement(fallback, workspaces, "imported", "w2")
	if !ok || agent.WorkspaceID != "w2" {
		t.Fatalf("acknowledged workspace not used: %#v %v", agent, ok)
	}
}

func TestHomeImportListingFilteringAndProviderRetry(t *testing.T) {
	state := homeImportState{limit: 15}
	state.applyListing(homeImportListing{
		entries: []homeImportEntry{
			{ProviderID: "claude", ProviderLabel: "Claude Code", ProviderHandleID: "a"},
			{ProviderID: "codex", ProviderLabel: "Codex", ProviderHandleID: "b"},
		},
		alreadyImported: 1,
	})
	if len(state.filters) != 2 || len(state.visibleEntries()) != 2 {
		t.Fatalf("listing = %#v", state)
	}
	state.filter = "codex"
	visible := state.visibleEntries()
	if len(visible) != 1 || visible[0].ProviderHandleID != "b" {
		t.Fatalf("filter = %#v", visible)
	}
	state.mergeProviderListing("codex", homeImportListing{
		entries: []homeImportEntry{{ProviderID: "codex", ProviderLabel: "Codex", ProviderHandleID: "c"}},
	})
	if len(state.visibleEntries()) != 1 || state.visibleEntries()[0].ProviderHandleID != "c" {
		t.Fatalf("retry did not replace that provider's rows: %#v", state.entries)
	}
	if len(state.entries) != 2 || state.entries[0].ProviderHandleID == "b" {
		t.Fatalf("retry disturbed other providers: %#v", state.entries)
	}
	state.filter = "gemini"
	state.applyListing(homeImportListing{entries: []homeImportEntry{{ProviderID: "claude", ProviderHandleID: "d"}}})
	if state.filter != "" {
		t.Fatalf("filter survived its provider: %q", state.filter)
	}
}

func TestHomeImportBlockedOutcomeDoesNotRetry(t *testing.T) {
	entry := homeImportEntry{ProviderID: "claude", ProviderHandleID: "session-7", CWD: "/repo/app"}
	a := &App{connected: true, client: &client.Client{}}
	a.desk.homeImport.blocked = map[string]bool{entry.key(): true}
	a.importHomeSession(entry)
	if a.desk.homeImport.importing != "" {
		t.Fatal("resent an import whose outcome is unknown")
	}
	if a.desk.homeImport.importErr == "" {
		t.Fatal("unknown outcome was not surfaced")
	}
}

func TestHomeImportOpensAcknowledgedWorkspaceWhenDirectoryOmitsIt(t *testing.T) {
	a := &App{}
	a.desk.prefs.ActiveWorkspace = make(map[string]string)
	a.desk.workspace = "old"
	a.applyImportedHomeDirectory([]Workspace{{ID: "old"}, {ID: "new"}}, []Agent{{ID: "imported"}}, homeImportResult{agentID: "imported", workspaceID: "new"})
	if a.desk.workspace != "new" {
		t.Fatalf("imported tab opened in %q", a.desk.workspace)
	}
	tab, ok := a.activeTab()
	if !ok || tab.Target != "imported" {
		t.Fatalf("imported agent was not active: %+v", tab)
	}
}
