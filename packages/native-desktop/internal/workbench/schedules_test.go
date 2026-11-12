package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/getpaseo/paseo/packages/native-desktop/internal/client"
)

func TestScheduleFormFreshAndHeartbeatPatch(t *testing.T) {
	old := scheduleRecord{ID: "same", Name: "Old", Prompt: "old", Cadence: scheduleCadence{Type: "every", EveryMS: 60000}, Target: scheduleTarget{Type: "agent", AgentID: "agent"}}
	edit := newScheduleForm("host-a", &old)
	if _, _, err := edit.payload(); err == nil {
		t.Fatal("unchanged heartbeat must not submit")
	}
	edit.Expression = "0 9 * * 1"
	kind, patch, err := edit.payload()
	if err != nil || kind != "schedule/update" || patch["scheduleId"] != "same" {
		t.Fatalf("heartbeat patch: %s %#v %v", kind, patch, err)
	}
	if _, ok := patch["prompt"]; ok {
		t.Fatal("heartbeat edit rewrites prompt")
	}
	fresh := newScheduleForm("host-b", nil)
	if fresh.Name != "" || fresh.Prompt != "" || fresh.Host != "host-b" || fresh.Original != nil {
		t.Fatal("create inherited edit state")
	}
}

func TestScheduleFormPayloadAndValidation(t *testing.T) {
	f := newScheduleForm("h", nil)
	f.Name = " Weekly "
	f.Prompt = " Do work "
	f.CWD = "/project"
	f.Project = "Project"
	f.Provider = "codex"
	f.Model = "model"
	f.Expression = "0 9 * * 1"
	f.Timezone = "UTC"
	f.MaxRuns = "3"
	f.Archive = true
	f.Multiplicity = true
	f.IsGit = true
	f.Isolation = "worktree"
	_, p, err := f.payload()
	if err != nil {
		t.Fatal(err)
	}
	target := p["target"].(scheduleTarget)
	if p["prompt"] != "Do work" || p["maxRuns"] != 3 || target.Config.CWD != "/project" || target.Config.Isolation != "worktree" || !*target.Config.Archive {
		t.Fatalf("wrong create payload %#v", p)
	}
	for _, value := range []string{"0", "-1", "1.2", "bad"} {
		f.MaxRuns = value
		if _, _, err := f.payload(); err == nil {
			t.Fatalf("accepted max runs %q", value)
		}
	}
	f.MaxRuns = ""
	f.Expression = "99 9 * * 1"
	if _, _, err := f.payload(); err == nil {
		t.Fatal("accepted invalid cron")
	}
	f.Expression = "0 9 * * 1"
	f.Timezone = "Missing/Zone"
	if _, _, err := f.payload(); err == nil {
		t.Fatal("accepted invalid timezone")
	}
}

func TestScheduleIdentityAndBuckets(t *testing.T) {
	now := time.Now()
	row := scheduleRecord{ID: "same", Status: "active", Target: scheduleTarget{Type: "agent", AgentID: "a"}}
	a := scheduleHost{ID: "a", Agents: map[string]Agent{}, AgentsLoaded: false}
	if row.derived(a, now) != "active" {
		t.Fatal("unloaded agent list marked target gone")
	}
	a.AgentsLoaded = true
	if row.derived(a, now) != "targetGone" {
		t.Fatal("missing target not ended")
	}
	row.ExpiresAt = now.Add(-time.Hour).Format(time.RFC3339)
	if row.derived(a, now) != "expired" {
		t.Fatal("expiry priority")
	}
	if (scheduleKey{Host: "a", ID: "same"}) == (scheduleKey{Host: "b", ID: "same"}) {
		t.Fatal("host identity lost")
	}
}

type scheduleTestCaller struct {
	calls int
	raw   json.RawMessage
	err   error
}

func (c *scheduleTestCaller) Call(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	c.calls++
	return c.raw, c.err
}
func TestScheduleAcknowledgementRequired(t *testing.T) {
	for _, raw := range []string{`{}`, `{"schedule":null,"error":null}`, `{"schedule":{"id":"other"},"error":null}`} {
		c := &scheduleTestCaller{raw: json.RawMessage(raw)}
		if _, err := scheduleMutation(context.Background(), c, "schedule/pause", map[string]any{"scheduleId": "s"}); !errors.Is(err, client.ErrOutcomeUnknown) {
			t.Fatalf("malformed acknowledgement must be uncertain: %s %v", raw, err)
		}
	}
	c := &scheduleTestCaller{raw: json.RawMessage(`{"error":"denied"}`)}
	if _, err := scheduleMutation(context.Background(), c, "schedule/pause", map[string]any{"scheduleId": "s"}); err == nil || errors.Is(err, client.ErrOutcomeUnknown) {
		t.Fatalf("explicit rejection treated as unknown: %v", err)
	}
}

func TestScheduleUnknownReconciliation(t *testing.T) {
	old := scheduleRecord{ID: "s", Status: "active"}
	p := schedulePending{Kind: "schedule/pause", Key: scheduleKey{Host: "a", ID: "s"}}
	if p.reconciled(scheduleHost{ID: "b", Rows: []scheduleRecord{{ID: "s", Status: "paused"}}}) {
		t.Fatal("another host resolved operation")
	}
	if p.reconciled(scheduleHost{ID: "a", Rows: []scheduleRecord{old}}) {
		t.Fatal("unchanged list resolved operation")
	}
	if !p.reconciled(scheduleHost{ID: "a", Rows: []scheduleRecord{{ID: "s", Status: "paused"}}}) {
		t.Fatal("acknowledged state did not resolve pause")
	}
	create := schedulePending{Kind: "schedule/create", Key: scheduleKey{Host: "a"}, Before: map[string]bool{"old": true}, Payload: map[string]any{"prompt": "task", "cadence": scheduleCadence{Type: "cron", Expression: "0 9 * * 1"}, "target": scheduleTarget{Type: "new-agent", Config: scheduleConfig{Provider: "codex", CWD: "/p"}}}}
	row := scheduleRecord{ID: "new", Prompt: "task", Cadence: scheduleCadence{Type: "cron", Expression: "0 9 * * 1"}, Target: scheduleTarget{Type: "new-agent", Config: scheduleConfig{Provider: "codex", CWD: "/p"}}}
	if !create.reconciled(scheduleHost{ID: "a", Rows: []scheduleRecord{row}}) {
		t.Fatal("matching new schedule did not resolve create")
	}
	row2 := row
	row2.ID = "new2"
	if create.reconciled(scheduleHost{ID: "a", Rows: []scheduleRecord{row, row2}}) {
		t.Fatal("ambiguous create resolved")
	}
}

func TestScheduleFormKeepsDraftAndSelectionAcrossHostRefresh(t *testing.T) {
	f := newScheduleForm("h", nil)
	f.Name = "Draft"
	f.Prompt = "unsaved"
	f.CWD = "/p"
	f.Project = "Captured name"
	f.Provider = "codex"
	f.Model = "m"
	h := scheduleHost{ID: "h", Multiplicity: true, Projects: []Project{{Name: "Renamed", Root: "/p", Kind: "git"}}, Providers: []scheduleProvider{{ID: "codex", Status: "ready", Models: []scheduleModel{{ID: "m"}}}}}
	applyScheduleFormHost(f, h)
	if f.Name != "Draft" || f.Prompt != "unsaved" || f.Project != "Captured name" || !f.IsGit || !f.Multiplicity {
		t.Fatal("background options replaced the draft or its captured display")
	}
	if err := f.selectionError(h); err != nil {
		t.Fatal(err)
	}
	h.Providers = nil
	if err := f.selectionError(h); err == nil {
		t.Fatal("missing provider data enabled submit")
	}
	if f.Model != "m" || f.Provider != "codex" {
		t.Fatal("late options cleared selected values")
	}
}

func TestScheduleCronMatchesCanonicalSyntax(t *testing.T) {
	for _, expression := range []string{"*/5 * * * *", "0 9 * * 1-5", "0,30 9-17/2 * * 0,6"} {
		if err := validateScheduleCron(expression); err != nil {
			t.Fatalf("rejected %q: %v", expression, err)
		}
	}
	for _, expression := range []string{"0 9 * * 7", "5/2 * * * *", "*/01 * * * *", "+1 * * * *", "0 9 * * MON", "0 9 * *", "0 9 0 * *"} {
		if err := validateScheduleCron(expression); err == nil {
			t.Fatalf("accepted %q", expression)
		}
	}
}

func TestScheduleListRejectsMissingDataAndKeepsHostErrors(t *testing.T) {
	c := &scheduleTestCaller{raw: json.RawMessage(`{"schedules":[],"error":null}`)}
	rows, err := scheduleList(context.Background(), c)
	if err != nil || rows == nil {
		t.Fatalf("loaded empty: %#v %v", rows, err)
	}
	c.raw = json.RawMessage(`{"error":"unsupported"}`)
	if _, err := scheduleList(context.Background(), c); err == nil {
		t.Fatal("unsupported host became an empty success")
	}
	c.raw = json.RawMessage(`{}`)
	if _, err := scheduleList(context.Background(), c); err == nil {
		t.Fatal("missing list became an empty success")
	}
	offline := loadScheduleHost(context.Background(), navigationSource{ID: "offline", Name: "Remote", Err: errors.New("unreachable")})
	if offline.ID != "offline" || offline.Name != "Remote" || offline.Error != "unreachable" {
		t.Fatalf("offline identity lost: %#v", offline)
	}
}

func TestScheduleRunUnknownNeedsNewHostObservation(t *testing.T) {
	p := schedulePending{Kind: "schedule/run-once", Key: scheduleKey{Host: "h", ID: "s"}, LastRun: "2026-11-10T10:00:00Z"}
	h := scheduleHost{ID: "h", Rows: []scheduleRecord{{ID: "s", LastRunAt: p.LastRun}}}
	if p.reconciled(h) {
		t.Fatal("old run resolved an unknown new run")
	}
	h.Rows[0].LastRunAt = "2026-11-12T10:00:00Z"
	if !p.reconciled(h) {
		t.Fatal("new run did not resolve the requested effect")
	}
}

func TestScheduleEditWorktreeToNonGitSendsLocalIsolation(t *testing.T) {
	original := scheduleRecord{ID: "schedule", Prompt: "task", Cadence: scheduleCadence{Type: "cron", Expression: "0 9 * * 1", Timezone: "UTC"}, Target: scheduleTarget{Type: "new-agent", Config: scheduleConfig{Provider: "codex", CWD: "/git-project", Isolation: "worktree"}}}
	form := newScheduleForm("host", &original)
	form.CWD = "/directory-project"
	form.Project = "Directory project"
	applyScheduleFormHost(form, scheduleHost{ID: "host", Multiplicity: true, Projects: []Project{{Name: "Directory project", Root: "/directory-project", Kind: "directory"}}})
	kind, payload, err := form.payload()
	if err != nil {
		t.Fatal(err)
	}
	if kind != "schedule/update" {
		t.Fatalf("wrong request: %s", kind)
	}
	config := payload["newAgentConfig"].(map[string]any)
	if config["cwd"] != "/directory-project" || config["isolation"] != "local" {
		t.Fatalf("non-git project retained worktree isolation: %#v", config)
	}
	form.Multiplicity = false
	_, payload, err = form.payload()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["newAgentConfig"].(map[string]any)["isolation"]; ok {
		t.Fatal("unsupported host received isolation")
	}
}
