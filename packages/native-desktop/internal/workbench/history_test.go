package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

type historyTestCall struct {
	Type   string
	Search string
	Cursor string
	Limit  int
	Sort   []struct{ Key, Direction string }
}

type historyTestCaller struct {
	mu    sync.Mutex
	pages map[string]string
	err   error
	calls []historyTestCall
}

func (c *historyTestCaller) Call(_ context.Context, typ string, raw json.RawMessage) (json.RawMessage, error) {
	var request struct {
		Search string `json:"search"`
		Page   struct {
			Limit  int    `json:"limit"`
			Cursor string `json:"cursor"`
		} `json:"page"`
		Sort []struct{ Key, Direction string } `json:"sort"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, historyTestCall{Type: typ, Search: request.Search, Cursor: request.Page.Cursor, Limit: request.Page.Limit, Sort: request.Sort})
	if c.err != nil {
		return nil, c.err
	}
	page, ok := c.pages[request.Page.Cursor]
	if !ok {
		return nil, errors.New("unexpected history cursor: " + request.Page.Cursor)
	}
	return json.RawMessage(page), nil
}

func historyTestSource(id string, c caller, supportsSearch bool) navigationSource {
	info := json.RawMessage(`{"features":{"agentHistorySearch":false}}`)
	if supportsSearch {
		info = json.RawMessage(`{"features":{"agentHistorySearch":true}}`)
	}
	return navigationSource{ID: id, Name: "Host " + id, Client: c, Info: info}
}

func historyTestEntry(id, title, updated string) string {
	raw, _ := json.Marshal(map[string]any{
		"agent": map[string]any{
			"id": id, "title": title, "workspaceId": "workspace-" + id,
			"provider": "codex", "cwd": "/repo", "model": nil,
			"createdAt": "2026-11-01T10:00:00Z", "updatedAt": updated,
			"lastUserMessageAt": nil, "status": "idle", "capabilities": map[string]any{},
			"currentModeId": nil, "availableModes": []any{}, "pendingPermissions": []any{},
			"persistence": nil, "labels": map[string]string{}, "archivedAt": nil,
		},
		"project": map[string]any{"projectKey": "project", "projectName": "Paseo", "workspaceName": "Main", "checkout": map[string]any{"isGit": false}},
	})
	return string(raw)
}

func historyTestPage(entries []string, cursor string, more, truncated bool) string {
	rows := make([]json.RawMessage, len(entries))
	for i, entry := range entries {
		rows[i] = json.RawMessage(entry)
	}
	var next any
	if cursor != "" {
		next = cursor
	}
	raw, _ := json.Marshal(map[string]any{"requestId": "history", "entries": rows, "pageInfo": map[string]any{"nextCursor": next, "prevCursor": nil, "hasMore": more}, "searchTruncated": truncated})
	return string(raw)
}

func historyTestLoad(t *testing.T, h *historyState, sources []navigationSource, more bool) historyBatch {
	t.Helper()
	request, ok := h.begin(more)
	if !ok {
		t.Fatal("history refused the requested load")
	}
	batch := fetchHistoryBatch(context.Background(), sources, request)
	if !h.apply(request, batch, time.Date(2026, 11, 12, 15, 0, 0, 0, time.UTC)) {
		t.Fatal("history discarded the current result")
	}
	return batch
}

func TestHistoryPaginationKeepsHostCursorsAndStopsAtExhaustion(t *testing.T) {
	a := &historyTestCaller{pages: map[string]string{
		"":       historyTestPage([]string{historyTestEntry("a-first", "First", "2026-11-12T12:00:00Z")}, "a-next", true, false),
		"a-next": historyTestPage([]string{historyTestEntry("a-last", "Last", "2026-11-10T12:00:00Z")}, "", false, false),
	}}
	b := &historyTestCaller{pages: map[string]string{"": historyTestPage([]string{historyTestEntry("b-only", "Other", "2026-11-11T12:00:00Z")}, "", false, false)}}
	sources := []navigationSource{historyTestSource("a", a, true), historyTestSource("b", b, true)}
	h := historyState{open: true}
	historyTestLoad(t, &h, sources, false)
	if !reflect.DeepEqual(h.cursors, map[string]string{"a": "a-next"}) {
		t.Fatalf("next host pages: %#v", h.cursors)
	}
	historyTestLoad(t, &h, sources, true)
	if len(h.entries) != 3 || h.hasMore() {
		t.Fatalf("entries=%d hasMore=%v", len(h.entries), h.hasMore())
	}
	if _, ok := h.begin(true); ok {
		t.Fatal("exhausted history started again at page one")
	}
	if len(a.calls) != 2 || a.calls[1].Cursor != "a-next" || len(b.calls) != 1 {
		t.Fatalf("host pagination: a=%+v b=%+v", a.calls, b.calls)
	}
	for _, call := range append(a.calls, b.calls...) {
		if call.Type != "fetch_agent_history_request" || call.Limit != 200 || len(call.Sort) != 1 || call.Sort[0].Key != "updated_at" || call.Sort[0].Direction != "desc" {
			t.Fatalf("history RPC contract: %+v", call)
		}
	}
}

func TestHistoryMergeDeduplicatesWithinHostAndKeepsOtherHostIdentity(t *testing.T) {
	a := &historyTestCaller{pages: map[string]string{
		"":     historyTestPage([]string{historyTestEntry("same", "Newest", "2026-11-12T12:00:00Z"), historyTestEntry("same", "Older duplicate", "2026-11-11T12:00:00Z")}, "next", true, false),
		"next": historyTestPage([]string{historyTestEntry("same", "Stale page", "2026-11-10T12:00:00Z"), historyTestEntry("older", "Older", "2026-11-09T12:00:00Z")}, "", false, false),
	}}
	b := &historyTestCaller{pages: map[string]string{"": historyTestPage([]string{historyTestEntry("same", "Other host", "2026-11-11T13:00:00Z")}, "", false, false)}}
	h := historyState{open: true}
	sources := []navigationSource{historyTestSource("a", a, true), historyTestSource("b", b, true)}
	historyTestLoad(t, &h, sources, false)
	historyTestLoad(t, &h, sources, true)
	if len(h.entries) != 3 {
		t.Fatalf("merged rows: %+v", h.entries)
	}
	if h.entries[0].HostID != "a" || h.entries[0].Title != "Newest" || h.entries[1].HostID != "b" || h.entries[1].ID != "same" || h.entries[1].WorkspaceID != "workspace-same" {
		t.Fatalf("wrong row identity/order: %+v", h.entries)
	}
}

func TestHistorySearchGatesWholeSelectedFleetAndSendsTrimmedQuery(t *testing.T) {
	a := &historyTestCaller{pages: map[string]string{"": historyTestPage(nil, "", false, false)}}
	b := &historyTestCaller{pages: map[string]string{"": historyTestPage(nil, "", false, false)}}
	sources := []navigationSource{historyTestSource("a", a, true), historyTestSource("b", b, false)}
	h := historyState{open: true, query: "  branch search  ", search: "  branch search  "}
	historyTestLoad(t, &h, sources, false)
	if h.searchSupported || a.calls[0].Search != "" || b.calls[0].Search != "" || h.query != "" {
		t.Fatalf("unsupported fleet searched: a=%+v b=%+v query=%q", a.calls, b.calls, h.query)
	}
	h.resetQuery("a", "  branch search  ")
	historyTestLoad(t, &h, sources, false)
	if !h.searchSupported || a.calls[1].Search != "branch search" || len(b.calls) != 1 {
		t.Fatalf("selected-host search: a=%+v b=%+v", a.calls, b.calls)
	}
}

func TestHistoryPartialErrorsPersistAcrossPagesAndRefreshClearsThem(t *testing.T) {
	a := &historyTestCaller{pages: map[string]string{"": historyTestPage([]string{historyTestEntry("one", "One", "2026-11-12T12:00:00Z")}, "next", true, false), "next": historyTestPage(nil, "", false, false)}}
	b := &historyTestCaller{err: errors.New("unreachable")}
	offline := navigationSource{ID: "offline", Name: "Offline laptop", Err: errors.New("connection refused")}
	sources := []navigationSource{historyTestSource("a", a, true), historyTestSource("b", b, true), offline}
	h := historyState{open: true}
	historyTestLoad(t, &h, sources, false)
	if h.loadErr != "" || len(h.entries) != 1 || len(h.hostErrors) != 2 || !h.searchSupported {
		t.Fatalf("partial result: %+v", h)
	}
	historyTestLoad(t, &h, sources, true)
	if len(h.hostErrors) != 2 || len(b.calls) != 1 {
		t.Fatalf("missing-host errors were lost: %+v", h.hostErrors)
	}
	b.err = nil
	b.pages = map[string]string{"": historyTestPage(nil, "", false, false)}
	historyTestLoad(t, &h, sources[:2], false)
	if len(h.hostErrors) != 0 || h.loadErr != "" || len(a.calls) != 4 || a.calls[2].Cursor != "" {
		t.Fatalf("refresh retained failed hosts/cursors: errors=%+v calls=%+v", h.hostErrors, a.calls)
	}
}

func TestHistoryAllFailedIsErrorRatherThanEmptySearch(t *testing.T) {
	h := historyState{open: true, search: "query", query: "query"}
	historyTestLoad(t, &h, []navigationSource{{ID: "offline", Name: "Laptop", Err: errors.New("offline")}}, false)
	if h.loadErr == "" || len(h.hostErrors) != 1 || h.hostErrors[0].HostID != "offline" || len(h.entries) != 0 || h.hasMore() {
		t.Fatalf("unreachable host presented as empty history: %+v", h)
	}
}

func TestHistoryRejectsResponsesForOldQueryHostAndClosedPage(t *testing.T) {
	h := historyState{open: true, epoch: 1, searchSupported: true}
	old, _ := h.begin(false)
	h.resetQuery("a", "new query")
	current, _ := h.begin(false)
	batch := historyBatch{searchSupported: true, query: "new query", selectedHost: "a", pages: []historyHostPage{{hostID: "a", entries: []historyAgent{{HostID: "a", ID: "current"}}}}}
	if h.apply(old, batch, time.Now()) || !h.loading {
		t.Fatal("old query changed the current request")
	}
	if !h.apply(current, batch, time.Now()) || len(h.entries) != 1 || h.entries[0].ID != "current" {
		t.Fatal("current query was not applied")
	}
	pending, _ := h.begin(false)
	h.resetQuery("b", "new query")
	if h.apply(pending, batch, time.Now()) {
		t.Fatal("old host populated another host")
	}
	pending, _ = h.begin(false)
	h.close()
	h.open = true
	if h.apply(pending, batch, time.Now()) {
		t.Fatal("closed page populated its reopened replacement")
	}
}

func TestHistoryTypingInvalidatesAnAnswerBeforeDebounce(t *testing.T) {
	h := historyState{open: true, searchSupported: true}
	request, _ := h.begin(false)
	h.search = "new text"
	h.invalidate()
	if h.apply(request, historyBatch{}, time.Now()) {
		t.Fatal("request accepted after search input changed")
	}
}

func TestHistoryTruncatedSearchNeverRestartsCursorPagination(t *testing.T) {
	c := &historyTestCaller{pages: map[string]string{"": historyTestPage([]string{historyTestEntry("one", "One", "2026-11-12T12:00:00Z")}, "ignored", true, true)}}
	h := historyState{open: true, query: "one", search: "one"}
	historyTestLoad(t, &h, []navigationSource{historyTestSource("a", c, true)}, false)
	if !h.searchTruncated || h.hasMore() {
		t.Fatalf("truncated search offered another page: %+v", h)
	}
	if _, ok := h.begin(true); ok {
		t.Fatal("truncated search started pagination")
	}
}

func TestHistoryMalformedAndRepeatedCursorsStopTheirHost(t *testing.T) {
	for _, cursor := range []string{"", "next"} {
		t.Run("cursor="+cursor, func(t *testing.T) {
			c := &historyTestCaller{pages: map[string]string{"": historyTestPage(nil, "next", true, false), "next": historyTestPage(nil, cursor, true, false)}}
			h := historyState{open: true}
			sources := []navigationSource{historyTestSource("a", c, true)}
			historyTestLoad(t, &h, sources, false)
			historyTestLoad(t, &h, sources, true)
			if h.hasMore() || len(h.hostErrors) != 1 {
				t.Fatalf("broken cursor repeated history forever: %+v", h)
			}
		})
	}
}

func TestHistoryMalformedPayloadNamesFailedHost(t *testing.T) {
	c := &historyTestCaller{pages: map[string]string{"": `{"entries":[],"pageInfo":null}`}}
	h := historyState{open: true}
	historyTestLoad(t, &h, []navigationSource{historyTestSource("broken", c, true)}, false)
	if h.loadErr == "" || len(h.hostErrors) != 1 || h.hostErrors[0].HostID != "broken" {
		t.Fatalf("invalid payload treated as successful empty history: %+v", h)
	}
}

func TestHistoryDateBucketsKeepSearchResultsChronological(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	cases := []struct{ at, want string }{
		{"2026-11-12T01:00:00Z", "Today"},
		{"2026-11-11T23:59:00Z", "Yesterday"},
		{"2026-11-05T12:00:00Z", "This week"},
		{"2026-10-20T12:00:00Z", "This month"},
		{"2026-09-01T12:00:00Z", "Older"},
	}
	for _, tc := range cases {
		at, _ := time.Parse(time.RFC3339, tc.at)
		if got := historyDateSection(at, now); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.at, got, tc.want)
		}
	}
}

func TestHistoryRefreshReplaysLoadedPagesUsingNewHostCursors(t *testing.T) {
	c := &historyTestCaller{pages: map[string]string{
		"":         historyTestPage([]string{historyTestEntry("one", "Old first", "2026-11-12T12:00:00Z")}, "old-next", true, false),
		"old-next": historyTestPage([]string{historyTestEntry("two", "Old second", "2026-11-11T12:00:00Z")}, "", false, false),
	}}
	h := historyState{open: true}
	sources := []navigationSource{historyTestSource("a", c, true)}
	historyTestLoad(t, &h, sources, false)
	historyTestLoad(t, &h, sources, true)
	c.pages = map[string]string{
		"":         historyTestPage([]string{historyTestEntry("one", "Updated first", "2026-11-12T14:00:00Z")}, "new-next", true, false),
		"new-next": historyTestPage([]string{historyTestEntry("two", "Updated second", "2026-11-11T14:00:00Z")}, "", false, false),
	}
	historyTestLoad(t, &h, sources, false)
	if len(h.entries) != 2 || h.entries[1].Title != "Updated second" || h.hasMore() {
		t.Fatalf("refresh lost loaded history: %+v", h.entries)
	}
	if len(c.calls) != 4 || c.calls[2].Cursor != "" || c.calls[3].Cursor != "new-next" {
		t.Fatalf("refresh reused old cursors: %+v", c.calls)
	}
}

func TestHistoryEmptyPageImportUsesExistingHomeAction(t *testing.T) {
	a := &App{}
	a.desk.history = historyState{open: true, loaded: true}
	tester := ui.NewTester(a.historyView, 700, 480)
	if err := tester.Click("Import session"); err != nil {
		t.Fatal(err)
	}
	if a.desk.homePending != "import" || !a.desk.shell.HostEditor.Open {
		t.Fatal("history did not open the existing host/import flow")
	}
}

func TestHistoryRefreshRechecksSearchCapabilityWithoutLosingInput(t *testing.T) {
	c := &historyTestCaller{pages: map[string]string{"": historyTestPage(nil, "", false, false)}}
	h := historyState{open: true, search: "  parser  ", query: "parser"}
	historyTestLoad(t, &h, []navigationSource{historyTestSource("a", c, false)}, false)
	if h.query != "" || h.search != "  parser  " || c.calls[0].Search != "" {
		t.Fatal("unsupported host changed the typed input or ran a search")
	}
	historyTestLoad(t, &h, []navigationSource{historyTestSource("a", c, true)}, false)
	if h.query != "parser" || c.calls[1].Search != "parser" {
		t.Fatal("newly supported host displayed an input that was not searched")
	}
}

func TestHistoryRemovedSelectedHostStartsAllHostsAtFirstPage(t *testing.T) {
	a := &historyTestCaller{pages: map[string]string{"": historyTestPage([]string{historyTestEntry("removed", "Removed host", "2026-11-12T12:00:00Z")}, "next", true, false)}}
	b := &historyTestCaller{pages: map[string]string{"": historyTestPage([]string{historyTestEntry("remaining", "Remaining host", "2026-11-11T12:00:00Z")}, "", false, false)}}
	h := historyState{open: true, selectedHost: "a"}
	historyTestLoad(t, &h, []navigationSource{historyTestSource("a", a, true), historyTestSource("b", b, true)}, false)
	historyTestLoad(t, &h, []navigationSource{historyTestSource("b", b, true)}, true)
	if h.selectedHost != "" || len(h.entries) != 1 || h.entries[0].HostID != "b" || h.entries[0].ID != "remaining" || h.hasMore() {
		t.Fatalf("removed host kept its selection or rows: %+v", h)
	}
	if len(b.calls) != 1 || b.calls[0].Cursor != "" {
		t.Fatalf("replacement all-host query did not start at page one: %+v", b.calls)
	}
}
