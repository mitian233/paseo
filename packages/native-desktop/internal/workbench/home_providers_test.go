package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/getpaseo/paseo/packages/native-desktop/internal/client"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/workbenchstate"
)

type homeProviderTestCall struct {
	Type    string
	Payload json.RawMessage
}

type homeProviderTestCaller struct {
	responses map[string]json.RawMessage
	errs      map[string]error
	calls     []homeProviderTestCall
}

func (f *homeProviderTestCaller) Call(_ context.Context, typ string, payload json.RawMessage) (json.RawMessage, error) {
	f.calls = append(f.calls, homeProviderTestCall{Type: typ, Payload: append(json.RawMessage(nil), payload...)})
	if err := f.errs[typ]; err != nil {
		return nil, err
	}
	response, ok := f.responses[typ]
	if !ok {
		return nil, errors.New("unexpected request " + typ)
	}
	return response, nil
}

func TestHomeProviderSnapshotReportsStatusModelsAndEnabled(t *testing.T) {
	caller := &homeProviderTestCaller{responses: map[string]json.RawMessage{
		"get_providers_snapshot_request": json.RawMessage(`{"requestId":"r","generatedAt":"now","entries":[
			{"provider":"claude","status":"ready","enabled":true,"label":"Claude Code","models":[{"id":"a"},{"id":"hidden","isSelectable":false},{"id":"b"}]},
			{"provider":"omp","status":"unavailable","enabled":false,"source":"builtin","error":null},
			{"provider":"my-claude","status":"error","error":"binary not found","source":"custom"}
		]}`),
	}}
	entries, err := loadHomeProviderSnapshot(context.Background(), caller)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d", len(entries))
	}
	claude := entries[0]
	if claude.Label != "Claude Code" || claude.Models != 2 || !claude.Enabled || !claude.Toggle {
		t.Fatalf("ready provider = %+v", claude)
	}
	omp := entries[1]
	if omp.Enabled || omp.Toggle || omp.Label != "omp" {
		t.Fatalf("disabled provider = %+v", omp)
	}
	broken := entries[2]
	if !broken.Enabled || broken.Error != "binary not found" || broken.Source != "custom" {
		t.Fatalf("broken provider = %+v", broken)
	}
	if len(caller.calls) != 1 || caller.calls[0].Type != "get_providers_snapshot_request" || string(caller.calls[0].Payload) != "{}" {
		t.Fatalf("requests = %+v", caller.calls)
	}
}

func TestHomeProviderSnapshotRejectsMalformedEntries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response string
	}{
		{"missing entries", `{"requestId":"r"}`},
		{"missing provider id", `{"entries":[{"status":"ready"}]}`},
		{"not an object", `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller := &homeProviderTestCaller{responses: map[string]json.RawMessage{
				"get_providers_snapshot_request": json.RawMessage(tc.response),
			}}
			if _, err := loadHomeProviderSnapshot(context.Background(), caller); err == nil {
				t.Fatal("malformed snapshot accepted")
			}
		})
	}
}

func TestHomeProviderConfigReadsKnownFieldsWithoutTouchingUnknownOnes(t *testing.T) {
	caller := &homeProviderTestCaller{responses: map[string]json.RawMessage{
		"get_daemon_config_request": json.RawMessage(`{"requestId":"r","config":{
			"providers":{
				"claude":{"enabled":true,"command":["/opt/claude","--verbose"],"env":{"ANTHROPIC_API_KEY":"secret"},"options":{"future":true},"thisClientDoesNotKnow":1},
				"my-claude":{"extends":"claude","label":"My Claude","models":[{"id":"m","label":"M"}]}
			},
			"mcp":{"injectIntoAgents":true}
		}}`),
	}}
	configs, err := loadHomeProviderConfig(context.Background(), caller)
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 2 {
		t.Fatalf("providers = %+v", configs)
	}
	claude := configs["claude"]
	if strings.Join(claude.Command, " ") != "/opt/claude --verbose" || claude.Extends != "" || claude.Label != "" {
		t.Fatalf("claude config = %+v", claude)
	}
	profile := configs["my-claude"]
	if profile.Extends != "claude" || profile.Label != "My Claude" {
		t.Fatalf("profile config = %+v", profile)
	}
}

func TestHomeProviderConfigRejectsMissingConfig(t *testing.T) {
	for _, raw := range []string{`{"requestId":"r"}`, `{"requestId":"r","config":{"providers":{"claude":[]}}}`} {
		caller := &homeProviderTestCaller{responses: map[string]json.RawMessage{
			"get_daemon_config_request": json.RawMessage(raw),
		}}
		if _, err := loadHomeProviderConfig(context.Background(), caller); err == nil {
			t.Fatalf("malformed config accepted: %s", raw)
		}
	}
}

func TestHomeProviderSaveFieldsKeepsUnchangedConfigurationOutOfPatch(t *testing.T) {
	config := homeProviderConfig{Extends: "claude", Label: "My Claude", Command: []string{"/opt/claude"}}
	fields, err := homeProviderSaveFields(config, "My Claude", "/opt/claude\n--verbose")
	if err != nil {
		t.Fatal(err)
	}
	request, err := homeProviderConfigRequest("my-claude", fields)
	if err != nil {
		t.Fatal(err)
	}
	var patch map[string]any
	if err := json.Unmarshal(request, &patch); err != nil {
		t.Fatal(err)
	}
	configs := patch["config"].(map[string]any)["providers"].(map[string]any)
	provider := configs["my-claude"].(map[string]any)
	if len(provider) != 1 {
		t.Fatalf("patch rewrote unchanged fields: %s", request)
	}
	command := provider["command"].([]any)
	if len(command) != 2 || command[0] != "/opt/claude" || command[1] != "--verbose" {
		t.Fatalf("command = %#v", command)
	}

	labelFields, err := homeProviderSaveFields(config, "Claude Work", "/opt/claude")
	if err != nil {
		t.Fatal(err)
	}
	labelRequest, err := homeProviderConfigRequest("my-claude", labelFields)
	if err != nil {
		t.Fatal(err)
	}
	if string(labelRequest) != `{"config":{"providers":{"my-claude":{"label":"Claude Work"}}}}` {
		t.Fatalf("label patch = %s", labelRequest)
	}

	enableRequest, err := homeProviderEnabledRequest("claude", false)
	if err != nil {
		t.Fatal(err)
	}
	if string(enableRequest) != `{"config":{"providers":{"claude":{"enabled":false}}}}` {
		t.Fatalf("enable patch = %s", enableRequest)
	}
}

func TestHomeProviderSaveRejectsEmptyOrIncompleteEdits(t *testing.T) {
	for _, tc := range []struct {
		name    string
		config  homeProviderConfig
		label   string
		command string
		want    error
	}{
		{"no changes", homeProviderConfig{}, "", "", errHomeProviderChanges},
		{"blank command lines only", homeProviderConfig{}, "", "\n  \n", errHomeProviderChanges},
		{"clearing a binary override", homeProviderConfig{Command: []string{"/usr/bin/claude"}}, "", "", errHomeProviderCommand},
		{"blank lines around a command", homeProviderConfig{Command: []string{"/usr/bin/claude"}}, "", "\n \n", errHomeProviderCommand},
		{"missing profile label", homeProviderConfig{Extends: "claude", Label: "Claude"}, "", "/usr/bin/claude", errHomeProviderLabel},
		{"blank profile label", homeProviderConfig{Extends: "claude", Label: "Claude"}, "   ", "/usr/bin/claude", errHomeProviderLabel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields, err := homeProviderSaveFields(tc.config, tc.label, tc.command)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if fields != nil {
				t.Fatalf("rejected edit produced fields %+v", fields)
			}
		})
	}
	if _, err := homeProviderConfigRequest("", map[string]any{"enabled": false}); err == nil {
		t.Fatal("empty provider ID accepted")
	}
	if _, err := homeProviderConfigRequest("claude", nil); !errors.Is(err, errHomeProviderChanges) {
		t.Fatalf("empty patch error = %v", err)
	}
}

func TestHomeProviderSavePreservesAcknowledgementErrors(t *testing.T) {
	rejected := &homeProviderTestCaller{errs: map[string]error{
		"set_daemon_config_request": errors.New("Request failed: Provider command must not be empty"),
	}}
	if _, err := saveHomeProviderConfig(context.Background(), rejected, json.RawMessage(`{"config":{}}`)); err == nil ||
		!strings.Contains(err.Error(), "Provider command must not be empty") {
		t.Fatalf("ack error = %v", err)
	}

	missing := &homeProviderTestCaller{responses: map[string]json.RawMessage{
		"set_daemon_config_request": json.RawMessage(`{"requestId":"r"}`),
	}}
	if _, err := saveHomeProviderConfig(context.Background(), missing, json.RawMessage(`{"config":{}}`)); err == nil ||
		!strings.Contains(err.Error(), "missing config") {
		t.Fatalf("malformed ack error = %v", err)
	}
}

func TestHomeProviderRefreshWaitsForDaemonAcknowledgement(t *testing.T) {
	acknowledged := &homeProviderTestCaller{responses: map[string]json.RawMessage{
		"refresh_providers_snapshot_request": json.RawMessage(`{"requestId":"r","acknowledged":true}`),
	}}
	if err := refreshHomeProviderSnapshot(context.Background(), acknowledged, "my-claude"); err != nil {
		t.Fatal(err)
	}
	if len(acknowledged.calls) != 1 || string(acknowledged.calls[0].Payload) != `{"providers":["my-claude"]}` {
		t.Fatalf("refresh request = %+v", acknowledged.calls)
	}

	for _, tc := range []struct {
		name     string
		response string
		err      error
	}{
		{"missing acknowledgement", `{"requestId":"r"}`, nil},
		{"rejected acknowledgement", `{"requestId":"r","acknowledged":false}`, nil},
		{"daemon error", ``, errors.New("Request failed: provider refresh timed out")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller := &homeProviderTestCaller{
				responses: map[string]json.RawMessage{"refresh_providers_snapshot_request": json.RawMessage(tc.response)},
				errs:      map[string]error{"refresh_providers_snapshot_request": tc.err},
			}
			if err := refreshHomeProviderSnapshot(context.Background(), caller, "claude"); err == nil {
				t.Fatal("unacknowledged refresh accepted")
			}
		})
	}
}

func TestHomeProviderDiagnosticReportsDaemonTextAndRejections(t *testing.T) {
	caller := &homeProviderTestCaller{responses: map[string]json.RawMessage{
		"provider_diagnostic_request": json.RawMessage(`{"provider":"claude","requestId":"r","diagnostic":"Command: claude\nStatus: ready"}`),
	}}
	text, err := loadHomeProviderDiagnostic(context.Background(), caller, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if text != "Command: claude\nStatus: ready" {
		t.Fatalf("diagnostic = %q", text)
	}
	if len(caller.calls) != 1 || string(caller.calls[0].Payload) != `{"provider":"claude"}` {
		t.Fatalf("request = %+v", caller.calls)
	}

	for _, tc := range []struct {
		name     string
		response string
	}{
		{"other provider", `{"provider":"codex","diagnostic":"x"}`},
		{"missing diagnostic", `{"provider":"claude"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller := &homeProviderTestCaller{responses: map[string]json.RawMessage{
				"provider_diagnostic_request": json.RawMessage(tc.response),
			}}
			if _, err := loadHomeProviderDiagnostic(context.Background(), caller, "claude"); err == nil {
				t.Fatal("stale diagnostic accepted")
			}
		})
	}
}

func TestHomeProviderOperationEpochRejectsStaleResults(t *testing.T) {
	s := &homeProvidersState{host: "host-a", epoch: 3}
	if !s.owned(3, "host-a", "host-a") {
		t.Fatal("current operation rejected")
	}
	if s.owned(2, "host-a", "host-a") {
		t.Fatal("older operation accepted")
	}
	if s.owned(3, "host-a", "host-b") {
		t.Fatal("result from a previous active host accepted")
	}
	if s.owned(3, "host-b", "host-b") {
		t.Fatal("result for another host accepted")
	}
}

func TestHomeProvidersOpenAndCloseResetOwnership(t *testing.T) {
	a := &App{}
	a.openHomeProviders()
	s := a.desk.homeProviders
	if !s.open || s.err == "" || s.loading {
		t.Fatalf("disconnected open = %+v", s)
	}
	openedEpoch := s.epoch
	a.closeHomeProviders()
	if a.desk.homeProviders.open || a.desk.homeProviders.epoch <= openedEpoch {
		t.Fatalf("close kept state = %+v", a.desk.homeProviders)
	}
	a.desk.homeProviders.providers = []homeProviderEntry{{ID: "claude", Enabled: true, Toggle: false}}
	a.openHomeProviders()
	if len(a.desk.homeProviders.providers) != 0 || a.desk.homeProviders.selected != "" {
		t.Fatalf("reopen reused previous state = %+v", a.desk.homeProviders)
	}
}

func TestHomeProviderToggleIgnoresAChangedHostAndRevertsTheSwitch(t *testing.T) {
	a := &App{
		connected: true,
		client:    &client.Client{},
		desk: desktopState{
			prefs: workbenchstate.State{ActiveHost: "host-b"},
			homeProviders: homeProvidersState{
				open:      true,
				host:      "host-a",
				epoch:     1,
				providers: []homeProviderEntry{{ID: "claude", Label: "Claude", Enabled: true}},
			},
		},
	}
	a.toggleHomeProvider(0, false)
	s := a.desk.homeProviders
	if s.actionErr == "" || s.toggling != "" || s.providers[0].Toggle != s.providers[0].Enabled {
		t.Fatalf("stale host toggle = %+v", s)
	}
	if len(s.providers) != 1 || s.providers[0].Enabled != true {
		t.Fatalf("provider changed: %+v", s.providers)
	}
}

func TestHomeProviderSelectionLoadsConfigurationIntoTheEditor(t *testing.T) {
	a := &App{desk: desktopState{
		prefs: workbenchstate.State{ActiveHost: "host-a"},
		homeProviders: homeProvidersState{
			open:  true,
			host:  "host-a",
			epoch: 2,
			providers: []homeProviderEntry{
				{ID: "my-claude", Label: "My Claude", Enabled: true, Toggle: true},
			},
			config: map[string]homeProviderConfig{
				"my-claude": {Extends: "claude", Label: "My Claude", Command: []string{"/opt/claude", "--verbose"}},
			},
		},
	}}
	a.selectHomeProvider("my-claude")
	s := a.desk.homeProviders
	if s.selected != "my-claude" || s.label != "My Claude" || s.binary != "/opt/claude\n--verbose" {
		t.Fatalf("selection editor = %+v", s)
	}
	if s.diagnosticErr != "Connect to a host to load provider diagnostics." {
		t.Fatalf("diagnostic error = %q", s.diagnosticErr)
	}

	a.selectHomeProvider("missing")
	if a.desk.homeProviders.selected != "my-claude" {
		t.Fatal("unknown provider replaced the selection")
	}
}

func TestHomeProviderCommandPreservesExactArgumentContents(t *testing.T) {
	want := []string{"/new/provider", "--prompt", "  exact text  ", ""}
	fields, err := homeProviderSaveFields(homeProviderConfig{Command: []string{"/old/provider", "--prompt", "  exact text  ", ""}}, "", strings.Join(want, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := fields["command"].([]string)
	if len(got) != len(want) {
		t.Fatalf("arguments = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argument %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestHomeProviderMalformedMutationAckIsUnknown(t *testing.T) {
	c := &homeProviderTestCaller{responses: map[string]json.RawMessage{"set_daemon_config_request": json.RawMessage(`{"requestId":"r"}`)}}
	_, err := saveHomeProviderConfig(context.Background(), c, json.RawMessage(`{"config":{"providers":{"claude":{"enabled":false}}}}`))
	if !errors.Is(err, client.ErrOutcomeUnknown) {
		t.Fatalf("malformed mutation acknowledgement classified as definite rejection: %v", err)
	}
}

func TestHomeProviderUnknownMutationBlocksUntilReconciled(t *testing.T) {
	s := homeProvidersState{toggling: "claude", providers: []homeProviderEntry{{ID: "claude", Enabled: true, Toggle: false}}}
	if !s.mutationFailed("claude", client.ErrOutcomeUnknown) || !s.loading || s.uncertain != "claude" {
		t.Fatalf("unknown mutation remained editable: %+v", s)
	}
	if s.providers[0].Toggle {
		t.Fatal("unknown mutation presented as a rejected toggle")
	}
}

func TestHomeProviderOlderDiagnosticsCannotOverwriteSavedResult(t *testing.T) {
	s := homeProvidersState{selected: "claude", diagnosticEpoch: 2}
	s.applyDiagnosticResult("claude", 2, "/new/provider", nil)
	s.applyDiagnosticResult("claude", 1, "/old/provider", nil)
	if s.diagnostic != "/new/provider" {
		t.Fatalf("old diagnostic won: %q", s.diagnostic)
	}
}
