package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/getpaseo/paseo/packages/native-desktop/internal/client"
)

func TestNewWorkspaceContextKeepsOfflineHostAndProjectSource(t *testing.T) {
	hosts := []newWorkspaceHost{
		{ID: "online", Multiplicity: true, Projects: []Project{{ID: "other", Root: "/other", Kind: "git"}}},
		{ID: "context", Err: "offline", Multiplicity: true, Projects: []Project{{ID: "project", Root: "/repo", Kind: "git"}}},
	}
	var state newWorkspaceState
	state.openContext("context", "project", "/repo/source")
	state.acceptHosts(state.epoch, state.loadRevision, hosts)
	if state.hostID != "context" || state.projectID != "project" || state.sourceDirectory != "/repo/source" {
		t.Fatalf("lost context: %+v", state)
	}
	if _, err := state.beginCreate(); err == nil {
		t.Fatal("created on offline host")
	}
}

func TestNewWorkspaceWithoutContextChoosesOnlineEligibleProject(t *testing.T) {
	var state newWorkspaceState
	state.openContext("", "", "")
	state.acceptHosts(state.epoch, state.loadRevision, []newWorkspaceHost{
		{ID: "offline", Err: "offline", Multiplicity: true, Projects: []Project{{ID: "p1", Root: "/one", Kind: "git"}}},
		{ID: "legacy", Projects: []Project{{ID: "plain", Root: "/plain", Kind: "directory"}}},
		{ID: "ready", Multiplicity: true, Receipts: true, Projects: []Project{{ID: "p2", Root: "/two", Kind: "directory"}}},
	})
	state.launch = "shell"
	if state.hostID != "ready" || state.projectID != "p2" || state.sourceDirectory != "/two" {
		t.Fatalf("selection = %s/%s/%s", state.hostID, state.projectID, state.sourceDirectory)
	}
	request, err := state.beginCreate()
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Source         struct{ Kind, Path, ProjectID string }
		IdempotencyKey string
	}
	if err := json.Unmarshal(request.payload, &body); err != nil {
		t.Fatal(err)
	}
	if body.Source.Kind != "directory" || body.Source.Path != "/two" || body.Source.ProjectID != "p2" || body.IdempotencyKey == "" {
		t.Fatalf("request = %s", request.payload)
	}
}

func TestNewWorkspaceMultiplicityAndWorktreeCapability(t *testing.T) {
	for _, tc := range []struct {
		name, kind, isolation string
		multiplicity, allowed bool
		want                  string
	}{
		{"directory modern", "directory", "local", true, true, "directory"},
		{"directory legacy", "directory", "local", false, false, ""},
		{"git legacy", "git", "local", false, true, "worktree"},
		{"git modern local", "git", "local", true, true, "directory"},
		{"unknown modern worktree", "unknown", "worktree", true, true, "directory"},
		{"git modern worktree", "git", "worktree", true, true, "worktree"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var s newWorkspaceState
			s.openContext("host", "p", "/repo")
			s.acceptHosts(s.epoch, s.loadRevision, []newWorkspaceHost{{ID: "host", Multiplicity: tc.multiplicity, Projects: []Project{{ID: "p", Root: "/repo", Kind: tc.kind}}}})
			s.isolation = tc.isolation
			s.launch = "shell"
			r, err := s.beginCreate()
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%v err=%v", tc.allowed, err)
			}
			if !tc.allowed {
				return
			}
			var body struct {
				Source struct {
					Kind string `json:"kind"`
				} `json:"source"`
			}
			if err := json.Unmarshal(r.payload, &body); err != nil {
				t.Fatal(err)
			}
			if body.Source.Kind != tc.want {
				t.Fatalf("source=%s", r.payload)
			}
		})
	}
}

func TestNewWorkspaceDefaultRefUsesExactUpstream(t *testing.T) {
	ref := newWorkspaceDefaultRef("main", "refs/remotes/upstream/release")
	if ref.RefName != "refs/remotes/upstream/release" || ref.Label != "release (upstream)" {
		t.Fatalf("ref=%+v", ref)
	}
	local := newWorkspaceDefaultRef("feature", "")
	if local.RefName != "refs/heads/feature" {
		t.Fatalf("local=%+v", local)
	}
	if got := newWorkspaceDefaultRef("", ""); got.RefName != "" {
		t.Fatalf("detached HEAD=%+v", got)
	}
}

func TestNewWorkspaceBranchPickerKeepsDivergedLocalAndRemote(t *testing.T) {
	local, remote, ahead, behind := true, true, 2, 0
	refs := newWorkspaceBranchOptions([]newWorkspaceBranch{{Name: "main", HasLocal: &local, HasRemote: &remote, LocalAhead: &ahead, LocalBehind: &behind}})
	if len(refs) != 2 || refs[0].RefName != "refs/remotes/origin/main" || refs[1].RefName != "refs/heads/main" {
		t.Fatalf("refs=%+v", refs)
	}
	ahead = 0
	refs = newWorkspaceBranchOptions([]newWorkspaceBranch{{Name: "main", HasLocal: &local, HasRemote: &remote, LocalAhead: &ahead, LocalBehind: &behind}})
	if len(refs) != 1 || refs[0].RefName != "refs/remotes/origin/main" {
		t.Fatalf("equal refs=%+v", refs)
	}
}

func TestNewWorkspaceChangeRequestUsesCheckout(t *testing.T) {
	for _, forge := range []string{"github", "gitlab"} {
		s := newWorkspaceReadyState()
		s.isolation = "worktree"
		s.ref = newWorkspaceRef{Label: "42", RefName: "topic", Forge: forge, Number: 42, ProjectPath: "owner/repo"}
		r, err := s.beginCreate()
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Source map[string]any `json:"source"`
		}
		if err := json.Unmarshal(r.payload, &body); err != nil {
			t.Fatal(err)
		}
		if body.Source["action"] != "checkout" || body.Source["refName"] != "topic" {
			t.Fatalf("request=%s", r.payload)
		}
		if _, exists := body.Source["githubPrNumber"]; exists != (forge == "github") {
			t.Fatalf("legacy forge field=%s", r.payload)
		}
		source := body.Source["checkoutSource"].(map[string]any)
		if source["kind"] != "change_request" || source["forge"] != forge || source["projectPath"] != "owner/repo" {
			t.Fatalf("checkout=%v", source)
		}
	}
}

func TestNewWorkspaceUnknownOutcomeNeverResendsAcrossReopen(t *testing.T) {
	s := newWorkspaceReadyState()
	r, err := s.beginCreate()
	if err != nil {
		t.Fatal(err)
	}
	s.finishCreate(r, newWorkspaceResult{unknown: true, err: "disconnected"})
	if _, err := s.beginCreate(); err == nil {
		t.Fatal("unknown operation resent")
	}
	s.close()
	s.openContext("host", "project", "/repo")
	s.acceptHosts(s.epoch, s.loadRevision, []newWorkspaceHost{{ID: "host", Multiplicity: true, Projects: []Project{{ID: "project", Root: "/repo", Kind: "git"}}}})
	if _, err := s.beginCreate(); err == nil {
		t.Fatal("reopening resent unknown operation")
	}
}

func TestNewWorkspaceClosedAndOlderResultsCannotReplaceCurrentSelection(t *testing.T) {
	s := newWorkspaceReadyState()
	epoch, revision := s.epoch, s.loadRevision
	r, err := s.beginCreate()
	if err != nil {
		t.Fatal(err)
	}
	s.close()
	s.openContext("other", "other-project", "/other")
	if s.acceptHosts(epoch, revision, []newWorkspaceHost{{ID: "wrong"}}) {
		t.Fatal("accepted old host result")
	}
	if s.finishCreate(r, newWorkspaceResult{workspaceID: "created"}) {
		t.Fatal("old create result navigated")
	}
	if s.hostID != "other" || s.projectID != "other-project" {
		t.Fatalf("selection overwritten: %+v", s)
	}
	s.loadRevision++
	if s.acceptHosts(s.epoch, s.loadRevision-1, nil) {
		t.Fatal("accepted older same-screen load")
	}
}

type newWorkspaceCaller struct {
	response string
	err      error
	kind     string
	payload  json.RawMessage
	calls    int
}

func (f *newWorkspaceCaller) Call(_ context.Context, kind string, payload json.RawMessage) (json.RawMessage, error) {
	f.kind, f.payload = kind, payload
	f.calls++
	return json.RawMessage(f.response), f.err
}

func TestNewWorkspaceRPCClassifiesAcknowledgementWithoutRetry(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		err            error
		unknown        bool
		workspace      string
	}{
		{"created", `{"workspace":{"id":"wks_created","projectId":"project","workspaceDirectory":"/repo"},"error":null}`, nil, false, "wks_created"},
		{"rejected", `{"workspace":null,"error":"not a repository"}`, nil, false, ""},
		{"unknown transport", "", client.ErrOutcomeUnknown, true, ""},
		{"malformed", "{", nil, true, ""},
		{"accepted pending", `{"workspace":null,"error":null,"creation":{"phase":"accepted","outcomeUnknown":false}}`, nil, true, ""},
		{"failed uncertain", `{"workspace":null,"error":"connection lost","creation":{"phase":"failed","outcomeUnknown":true}}`, nil, true, ""},
		{"failed definite", `{"workspace":null,"error":"rejected","creation":{"phase":"failed","outcomeUnknown":false}}`, nil, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &newWorkspaceCaller{response: tc.response, err: tc.err}
			result := newWorkspaceCreate(context.Background(), f, json.RawMessage(`{"source":{"kind":"directory","path":"/repo","projectId":"project"}}`))
			if result.unknown != tc.unknown || result.workspaceID != tc.workspace {
				t.Fatalf("result=%+v", result)
			}
			if f.calls != 1 || f.kind != "workspace.create.request" {
				t.Fatalf("RPC=%s calls=%d", f.kind, f.calls)
			}
			if tc.workspace == "" && result.err == "" {
				t.Fatal("missing failure message")
			}
		})
	}
}

func TestNewWorkspaceDefiniteFailureAllowsNewSubmission(t *testing.T) {
	s := newWorkspaceReadyState()
	r, err := s.beginCreate()
	if err != nil {
		t.Fatal(err)
	}
	s.finishCreate(r, newWorkspaceResult{err: errors.New("rejected").Error()})
	next, err := s.beginCreate()
	if err != nil {
		t.Fatal(err)
	}
	if string(next.payload) == string(r.payload) {
		t.Fatal("definitive failure reused old creation identity")
	}
}

func newWorkspaceReadyState() newWorkspaceState {
	var s newWorkspaceState
	s.openContext("host", "project", "/repo")
	s.acceptHosts(s.epoch, s.loadRevision, []newWorkspaceHost{{ID: "host", Multiplicity: true, Receipts: true, Projects: []Project{{ID: "project", Root: "/repo", Kind: "git"}}}})
	s.launch = "shell"
	return s
}

func TestNewWorkspaceChatSubmissionCarriesModelAndStableFirstMessage(t *testing.T) {
	s := newWorkspaceReadyState()
	s.launch = "chat"
	s.prompt = "Build a report"
	s.providers = []newWorkspaceProvider{{ID: "codex", Status: "ready", Models: []newWorkspaceModel{{newWorkspaceOption: newWorkspaceOption{ID: "gpt-test", Label: "GPT Test"}}}, Modes: []newWorkspaceOption{{ID: "plan", Label: "Plan"}}}}
	s.provider, s.model, s.mode, s.thinking = "codex", "gpt-test", "plan", "high"
	r, err := s.beginCreate()
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Agent struct {
			Config                         map[string]any
			InitialPrompt, ClientMessageID string
		}
		FirstAgentContext struct{ Prompt string }
	}
	if err := json.Unmarshal(r.payload, &body); err != nil {
		t.Fatal(err)
	}
	if body.Agent.Config["provider"] != "codex" || body.Agent.Config["model"] != "gpt-test" || body.Agent.Config["modeId"] != "plan" || body.Agent.Config["thinkingOptionId"] != "high" || body.Agent.Config["cwd"] != "/repo" {
		t.Fatalf("agent=%s", r.payload)
	}
	if body.Agent.InitialPrompt != "Build a report" || body.Agent.ClientMessageID != r.identity+":initial-message" || body.FirstAgentContext.Prompt != "Build a report" {
		t.Fatalf("first message=%s", r.payload)
	}
}

func TestNewWorkspaceTerminalProfileSubstitutionPreservesArgv(t *testing.T) {
	p := newWorkspaceTerminalProfile{Command: "tool", Args: []string{"--prompt={{{prompt}}}", "fixed", "prefix{{{prompt}}}suffix"}}
	command, args := newWorkspaceResolveTerminal(p, "hello; rm x")
	if command != "tool" || len(args) != 3 || args[0] != "--prompt=hello; rm x" || args[2] != "prefixhello; rm xsuffix" {
		t.Fatalf("argv=%q %q", command, args)
	}
	_, args = newWorkspaceResolveTerminal(p, "")
	if len(args) != 2 || args[0] != "fixed" || args[1] != "prefixsuffix" {
		t.Fatalf("empty argv=%q", args)
	}
}

func TestNewWorkspaceTerminalProfileAdoptionSkipsSubcommands(t *testing.T) {
	profiles := newWorkspaceAdoptProfiles([]newWorkspaceTerminalProfile{{ID: "one", Command: "/usr/bin/codex", Args: []string{"--profile", "work"}}, {ID: "two", Command: "codex", Args: []string{"--profile", "work", "login"}}})
	if len(profiles[0].Args) != 3 || profiles[0].Args[2] != "{{{prompt}}}" {
		t.Fatalf("interactive=%+v", profiles[0])
	}
	if len(profiles[1].Args) != 3 || profiles[1].Args[2] != "login" {
		t.Fatalf("subcommand=%+v", profiles[1])
	}
}

func TestNewWorkspaceTerminalSubmissionDoesNotCreateAgent(t *testing.T) {
	s := newWorkspaceReadyState()
	s.launch, s.terminalPrompt, s.providers, s.provider, s.model = "shell", "npm run dev | tee run.log", []newWorkspaceProvider{{ID: "codex", Status: "ready", Models: []newWorkspaceModel{{newWorkspaceOption: newWorkspaceOption{ID: "gpt-test", Label: "GPT Test"}}}}}, "codex", "gpt-test"
	r, err := s.beginCreate()
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(r.payload, &body); err != nil {
		t.Fatal(err)
	}
	if _, exists := body["agent"]; exists {
		t.Fatal("terminal launch created chat agent")
	}
	var naming struct{ Prompt string }
	if err := json.Unmarshal(body["firstAgentContext"], &naming); err != nil {
		t.Fatal(err)
	}
	if naming.Prompt != "npm run dev | tee run.log" {
		t.Fatalf("naming=%s", body["firstAgentContext"])
	}
}

func TestNewWorkspaceHostChangePreservesDraftButClearsHostAttachments(t *testing.T) {
	s := newWorkspaceReadyState()
	s.prompt = "keep my draft"
	s.attachments = []json.RawMessage{json.RawMessage(`{"type":"uploaded_file","id":"old-host-file"}`)}
	s.files = []newWorkspaceFile{{Name: "local.txt", Data: []byte("local")}}
	s.selectHost("other")
	if s.prompt != "keep my draft" || len(s.attachments) != 0 || len(s.files) != 1 {
		t.Fatalf("draft lost or remote attachment leaked: %+v", s)
	}
}

func TestNewWorkspacePartialLaunchCannotCreateSecondWorkspace(t *testing.T) {
	s := newWorkspaceReadyState()
	r, err := s.beginCreate()
	if err != nil {
		t.Fatal(err)
	}
	s.finishCreate(r, newWorkspaceResult{workspaceID: "created", err: "terminal failed", partial: true})
	if _, err := s.beginCreate(); err == nil {
		t.Fatal("partial launch created a second workspace")
	}
}

func TestNewWorkspaceFailedAgentKeepsCreatedWorkspace(t *testing.T) {
	result := newWorkspaceDecodeResult(json.RawMessage(`{"workspace":{"id":"created","workspaceDirectory":"/created"},"error":"agent unavailable","creation":{"phase":"failed","workspaceId":"created","agentId":null,"outcomeUnknown":false}}`), false)
	if result.workspaceID != "created" || !result.partial || result.unknown {
		t.Fatalf("result=%+v", result)
	}
}

func TestNewWorkspaceLateFailureCannotUnlockNewRequest(t *testing.T) {
	s := newWorkspaceReadyState()
	old, err := s.beginCreate()
	if err != nil {
		t.Fatal(err)
	}
	s.finishCreate(old, newWorkspaceResult{err: "rejected"})
	next, err := s.beginCreate()
	if err != nil {
		t.Fatal(err)
	}
	if s.finishCreate(old, newWorkspaceResult{workspaceID: "old"}) {
		t.Fatal("older request navigated")
	}
	if !s.busy || s.blocked[next.key].identity != next.identity {
		t.Fatal("older request changed current submission")
	}
}

func TestNewWorkspaceAttachmentsReachAgentAndNaming(t *testing.T) {
	s := newWorkspaceReadyState()
	s.launch = "chat"
	s.provider = "codex"
	s.model = "model"
	s.providers = []newWorkspaceProvider{{ID: "codex", Models: []newWorkspaceModel{{newWorkspaceOption: newWorkspaceOption{ID: "model"}}}}}
	s.attachments = []json.RawMessage{json.RawMessage(`{"type":"text","text":"review this"}`)}
	r, err := s.beginCreate()
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Agent             struct{ Attachments []json.RawMessage }
		FirstAgentContext struct{ Attachments []json.RawMessage }
	}
	if err := json.Unmarshal(r.payload, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Agent.Attachments) != 1 || len(body.FirstAgentContext.Attachments) != 1 {
		t.Fatalf("attachments=%s", r.payload)
	}
}

type newWorkspaceSequenceCaller struct {
	responses []string
	kinds     []string
	payloads  []json.RawMessage
	failure   error
}

func (f *newWorkspaceSequenceCaller) Call(_ context.Context, kind string, payload json.RawMessage) (json.RawMessage, error) {
	f.kinds = append(f.kinds, kind)
	f.payloads = append(f.payloads, append(json.RawMessage(nil), payload...))
	i := len(f.kinds) - 1
	if i >= len(f.responses) {
		return nil, f.failure
	}
	return json.RawMessage(f.responses[i]), nil
}

func TestNewWorkspaceLegacyAgentUsesCreatedDirectory(t *testing.T) {
	f := &newWorkspaceSequenceCaller{responses: []string{`{"workspace":{"id":"new","workspaceDirectory":"/worktree/new"},"error":null}`, `{"status":"agent_created","agent":{"id":"agent-new"}}`}}
	r := newWorkspaceCreateForHost(context.Background(), f, json.RawMessage(`{"source":{"kind":"directory","path":"/repo"},"agent":{"config":{"provider":"codex","cwd":"/repo"},"initialPrompt":"Build","clientMessageId":"message-1"}}`), false)
	if r.err != "" || r.workspaceID != "new" || r.agentID != "agent-new" || len(f.kinds) != 2 || f.kinds[1] != "create_agent_request" {
		t.Fatalf("result=%+v calls=%v", r, f.kinds)
	}
	var workspace map[string]json.RawMessage
	_ = json.Unmarshal(f.payloads[0], &workspace)
	if _, exists := workspace["agent"]; exists {
		t.Fatal("legacy workspace request carried agent")
	}
	var agent struct {
		Config                                      struct{ CWD string }
		WorkspaceID, InitialPrompt, ClientMessageID string
	}
	if err := json.Unmarshal(f.payloads[1], &agent); err != nil {
		t.Fatal(err)
	}
	if agent.Config.CWD != "/worktree/new" || agent.WorkspaceID != "new" || agent.InitialPrompt != "Build" || agent.ClientMessageID != "message-1" {
		t.Fatalf("agent=%s", f.payloads[1])
	}
}

func TestNewWorkspaceLegacyAgentUnknownKeepsWorkspace(t *testing.T) {
	f := &newWorkspaceSequenceCaller{responses: []string{`{"workspace":{"id":"created","workspaceDirectory":"/created"},"error":null}`}, failure: client.ErrOutcomeUnknown}
	r := newWorkspaceCreateForHost(context.Background(), f, json.RawMessage(`{"source":{"kind":"directory","path":"/repo"},"agent":{"config":{"provider":"codex","cwd":"/repo"}}}`), false)
	if !r.partial || !r.unknown || r.workspaceID != "created" || len(f.kinds) != 2 {
		t.Fatalf("result=%+v calls=%v", r, f.kinds)
	}
}

func TestNewWorkspaceImagesEncodeWithoutUploadCapability(t *testing.T) {
	r := newWorkspaceRequest{launch: "chat", payload: json.RawMessage(`{"agent":{"config":{"provider":"codex"}},"source":{"kind":"directory","path":"/repo"}}`), files: []newWorkspaceFile{{Name: "a.png", MIME: "image/png", Data: []byte{0, 255}}}}
	f := &newWorkspaceCaller{}
	raw, err := newWorkspacePrepareFiles(context.Background(), f, r)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Agent struct {
			Images []struct{ Data, MimeType string }
		}
	}
	if err = json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Agent.Images) != 1 || body.Agent.Images[0].Data != "AP8=" || body.Agent.Images[0].MimeType != "image/png" || f.calls != 0 {
		t.Fatalf("payload=%s calls=%d", raw, f.calls)
	}
	r.files = []newWorkspaceFile{{Name: "a.txt", MIME: "text/plain", Data: []byte("a")}}
	if _, err = newWorkspacePrepareFiles(context.Background(), f, r); err == nil {
		t.Fatal("non-image attachment silently omitted")
	}
}

func TestNewWorkspaceUnavailableProviderAndProfileCannotSubmit(t *testing.T) {
	s := newWorkspaceReadyState()
	s.launch = "profile:missing"
	if _, err := s.beginCreate(); err == nil {
		t.Fatal("missing terminal profile submitted")
	}
	s.launch, s.provider, s.model = "chat", "codex", "model"
	s.providers = []newWorkspaceProvider{{ID: "codex", Status: "unavailable", Models: []newWorkspaceModel{{newWorkspaceOption: newWorkspaceOption{ID: "model"}}}}}
	if _, err := s.beginCreate(); err == nil {
		t.Fatal("unavailable provider submitted")
	}
}

func TestNewWorkspacePartialResultSurvivesReopen(t *testing.T) {
	s := newWorkspaceReadyState()
	r, err := s.beginCreate()
	if err != nil {
		t.Fatal(err)
	}
	s.finishCreate(r, newWorkspaceResult{workspaceID: "created", partial: true, unknown: true, err: "terminal delivery unknown"})
	s.openContext("host", "project", "/repo")
	s.acceptHosts(s.epoch, s.loadRevision, []newWorkspaceHost{{ID: "host", Multiplicity: true, Projects: []Project{{ID: "project", Root: "/repo", Kind: "git"}}}})
	if s.partial.workspaceID != "created" || !s.partial.unknown {
		t.Fatalf("partial=%+v", s.partial)
	}
	if _, err := s.beginCreate(); err == nil {
		t.Fatal("reopening created a second workspace")
	}
}

type newWorkspaceUploadCaller struct {
	newWorkspaceCaller
	uploaded  int
	uploadErr error
}

func (f *newWorkspaceUploadCaller) UploadFile(_ context.Context, name, mime string, data []byte) (json.RawMessage, error) {
	f.uploaded++
	if name != "notes.txt" || mime != "text/plain" || string(data) != "notes" {
		return nil, errors.New("wrong upload")
	}
	if f.uploadErr != nil {
		return nil, f.uploadErr
	}
	return json.RawMessage(`{"type":"uploaded_file","id":"file","path":"/uploads/notes.txt","fileName":"notes.txt","mimeType":"text/plain","size":5}`), nil
}

func TestNewWorkspaceUploadFailureNeverStartsWorkspaceCreation(t *testing.T) {
	f := &newWorkspaceUploadCaller{uploadErr: client.ErrOutcomeUnknown}
	r := newWorkspaceRequest{launch: "chat", payload: json.RawMessage(`{"source":{"kind":"directory","path":"/repo"},"agent":{"config":{"provider":"codex"}}}`), files: []newWorkspaceFile{{Name: "notes.txt", MIME: "text/plain", Data: []byte("notes")}}}
	if _, err := newWorkspacePrepareFiles(context.Background(), f, r); !errors.Is(err, client.ErrOutcomeUnknown) {
		t.Fatalf("err=%v", err)
	}
	if f.calls != 0 || f.uploaded != 1 {
		t.Fatalf("create calls=%d uploads=%d", f.calls, f.uploaded)
	}
}

func TestNewWorkspaceUploadedFileReachesAgentAndWorkspaceNaming(t *testing.T) {
	f := &newWorkspaceUploadCaller{}
	r := newWorkspaceRequest{launch: "chat", payload: json.RawMessage(`{"source":{"kind":"directory","path":"/repo"},"agent":{"config":{"provider":"codex"}},"firstAgentContext":{"prompt":"Review"}}`), files: []newWorkspaceFile{{Name: "notes.txt", MIME: "text/plain", Data: []byte("notes")}}}
	raw, err := newWorkspacePrepareFiles(context.Background(), f, r)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Agent             struct{ Attachments []struct{ ID, Path string } }
		FirstAgentContext struct {
			Prompt      string
			Attachments []struct{ ID, Path string }
		}
	}
	if err = json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if f.uploaded != 1 || len(body.Agent.Attachments) != 1 || len(body.FirstAgentContext.Attachments) != 1 || body.Agent.Attachments[0].ID != "file" || body.FirstAgentContext.Attachments[0].Path != "/uploads/notes.txt" || body.FirstAgentContext.Prompt != "Review" {
		t.Fatalf("payload=%s uploads=%d", raw, f.uploaded)
	}
}

func TestNewWorkspaceHostListRetainsOfflineAndCapabilities(t *testing.T) {
	f := &newWorkspaceCaller{response: `{"projects":[{"projectId":"p","projectRootPath":"/repo","projectKind":"git"}],"error":null}`}
	hosts := newWorkspaceReadHosts(context.Background(), []navigationSource{{ID: "ready", Client: f, Info: json.RawMessage(`{"features":{"workspaceMultiplicity":true,"forgeSearch":true,"creationLifecycle":true,"workspaceRequestReceipts":true}}`)}, {ID: "offline", Err: errors.New("connection refused")}})
	if len(hosts) != 2 || !hosts[0].Multiplicity || !hosts[0].ForgeSearch || !hosts[0].CreationLifecycle || !hosts[0].Receipts || len(hosts[0].Projects) != 1 || hosts[1].Err != "connection refused" || f.kind != "project.list.request" {
		t.Fatalf("hosts=%+v RPC=%s", hosts, f.kind)
	}
}

func TestNewWorkspaceLegacyHostOmitsUnsupportedReceiptFields(t *testing.T) {
	s := newWorkspaceReadyState()
	s.hosts[0].Receipts = false
	s.hosts[0].CreationLifecycle = false
	r, err := s.beginCreate()
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	if err = json.Unmarshal(r.payload, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["idempotencyKey"]; ok {
		t.Fatal("legacy host received unsupported receipt key")
	}
	if _, ok := body["subscribe"]; ok {
		t.Fatal("legacy host received subscription request")
	}
}

func TestNewWorkspaceModelRecoverySkipsUnselectableModels(t *testing.T) {
	s := newWorkspaceReadyState()
	disabled := false
	s.provider, s.model = "codex", "retired"
	s.providers = []newWorkspaceProvider{{ID: "codex", Models: []newWorkspaceModel{{newWorkspaceOption: newWorkspaceOption{ID: "retired"}, Selectable: &disabled}, {newWorkspaceOption: newWorkspaceOption{ID: "first"}}, {newWorkspaceOption: newWorkspaceOption{ID: "last"}}}}}
	s.reconcileLaunchOptions()
	if s.model != "first" {
		t.Fatalf("model=%q", s.model)
	}
}

func TestNewWorkspaceAttachmentOnlySubmissionKeepsMessageIdentity(t *testing.T) {
	s := newWorkspaceReadyState()
	s.launch = "chat"
	s.provider, s.model = "codex", "model"
	s.providers = []newWorkspaceProvider{{ID: "codex", Models: []newWorkspaceModel{{newWorkspaceOption: newWorkspaceOption{ID: "model"}}}}}
	s.files = []newWorkspaceFile{{Name: "a.png", MIME: "image/png", Data: []byte{1}}}
	r, err := s.beginCreate()
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Agent struct {
			ClientMessageID string
			InitialPrompt   *string
		}
	}
	if err = json.Unmarshal(r.payload, &body); err != nil {
		t.Fatal(err)
	}
	if body.Agent.ClientMessageID != r.identity+":initial-message" || body.Agent.InitialPrompt != nil {
		t.Fatalf("payload=%s", r.payload)
	}
}

func TestNewWorkspaceReturningKeepsDraftFiles(t *testing.T) {
	s := newWorkspaceReadyState()
	s.prompt = "unfinished"
	s.files = []newWorkspaceFile{{Name: "notes.txt", MIME: "text/plain", Data: []byte("notes")}}
	s.openContext("host", "project", "/repo")
	if s.prompt != "unfinished" || len(s.files) != 1 || string(s.files[0].Data) != "notes" {
		t.Fatalf("draft=%q files=%+v", s.prompt, s.files)
	}
}

func TestNewWorkspaceSuccessfulSubscriptionReturnsAgent(t *testing.T) {
	r := newWorkspaceDecodeResult(json.RawMessage(`{"snapshot":{"phase":"completed","workspaceId":"workspace","agentId":"agent","workspace":{"id":"workspace","workspaceDirectory":"/created"},"error":null}}`), true)
	if r.err != "" || r.unknown || r.partial || r.workspaceID != "workspace" || r.agentID != "agent" || r.directory != "/created" {
		t.Fatalf("result=%+v", r)
	}
}
