package workbench

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/client"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/desktopui"
)

type newWorkspaceState struct {
	open, loading, busy, refsLoading, launchLoading                 bool
	epoch, loadRevision, refRevision, launchRevision                uint64
	preferences                                                     map[string]string
	hostID, projectID, sourceDirectory, isolation                   string
	contextHost, contextProject, contextSource                      string
	hosts                                                           []newWorkspaceHost
	ref                                                             newWorkspaceRef
	refs                                                            []newWorkspaceRef
	search, err, refErr, launchErr                                  string
	launch, prompt, terminalPrompt, provider, model, mode, thinking string
	providers                                                       []newWorkspaceProvider
	profiles                                                        []newWorkspaceTerminalProfile
	features                                                        []newWorkspaceFeature
	featureValues                                                   map[string]any
	files                                                           []newWorkspaceFile
	attachments                                                     []json.RawMessage
	cancel                                                          context.CancelFunc
	blocked                                                         map[string]newWorkspaceRequest
	partial                                                         newWorkspaceResult
}

type newWorkspaceHost struct {
	ID, Name, Err                                          string
	Multiplicity, ForgeSearch, CreationLifecycle, Receipts bool
	Projects                                               []Project
}
type newWorkspaceRef struct {
	Label, RefName, Forge, ProjectPath string
	Number                             int
}
type newWorkspaceBranch struct {
	Name        string `json:"name"`
	HasLocal    *bool  `json:"hasLocal"`
	HasRemote   *bool  `json:"hasRemote"`
	LocalAhead  *int   `json:"localAhead"`
	LocalBehind *int   `json:"localBehind"`
}
type newWorkspaceRequest struct {
	epoch                 uint64
	hostID, key, identity string
	payload               json.RawMessage
	lifecycle             bool
	launch, prompt        string
	profile               *newWorkspaceTerminalProfile
	files                 []newWorkspaceFile
	result                newWorkspaceResult
}
type newWorkspaceResult struct {
	workspaceID, err, agentID, terminalID, directory string
	unknown, partial                                 bool
}
type newWorkspaceOption struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Default bool   `json:"isDefault"`
}
type newWorkspaceModel struct {
	newWorkspaceOption
	Selectable      *bool                `json:"isSelectable"`
	Thinking        []newWorkspaceOption `json:"thinkingOptions"`
	DefaultThinking string               `json:"defaultThinkingOptionId"`
}
type newWorkspaceProvider struct {
	ID          string               `json:"provider"`
	Label       string               `json:"label"`
	Status      string               `json:"status"`
	Error       string               `json:"error"`
	Enabled     *bool                `json:"enabled"`
	Models      []newWorkspaceModel  `json:"models"`
	Modes       []newWorkspaceOption `json:"modes"`
	DefaultMode string               `json:"defaultModeId"`
}
type newWorkspaceFeature struct {
	Type    string               `json:"type"`
	ID      string               `json:"id"`
	Label   string               `json:"label"`
	Value   any                  `json:"value"`
	Options []newWorkspaceOption `json:"options"`
}
type newWorkspaceTerminalProfile struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}
type newWorkspaceFile struct {
	Name, MIME string
	Data       []byte
}

func (s *newWorkspaceState) openContext(host, project, source string) {
	previousHost := s.hostID
	s.close()
	s.open, s.loading, s.launchLoading = true, true, true
	s.hostID, s.projectID, s.sourceDirectory = host, project, source
	s.contextHost, s.contextProject, s.contextSource = host, project, source
	s.isolation, s.err, s.refErr, s.launchErr, s.search = "local", "", "", "", ""
	s.hosts, s.refs, s.ref = nil, nil, newWorkspaceRef{}
	s.providers, s.profiles, s.features = nil, nil, nil
	if previousHost != host {
		s.attachments = nil
	}
	s.partial = newWorkspaceResult{}
	s.featureValues = make(map[string]any)
	if s.launch == "" {
		s.launch = "chat"
	}
	s.restorePreferences(host)
}
func (s *newWorkspaceState) close() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.open, s.loading, s.busy, s.refsLoading, s.launchLoading = false, false, false, false, false
	s.epoch++
	s.loadRevision++
	s.refRevision++
	s.launchRevision++
}
func (s *newWorkspaceState) preferenceKey(field string) string {
	return "newWorkspace:" + s.hostID + ":" + field
}
func (s *newWorkspaceState) restorePreferences(host string) {
	if s.hostID != host {
		return
	}
	for field, target := range map[string]*string{"isolation": &s.isolation, "launch": &s.launch, "provider": &s.provider, "model": &s.model, "mode": &s.mode, "thinking": &s.thinking} {
		if v := s.preferences[s.preferenceKey(field)]; v != "" {
			*target = v
		}
	}
}
func (a *App) saveNewWorkspacePreference(field, value string) {
	s := &a.desk.newWorkspace
	if s.preferences == nil {
		s.preferences = map[string]string{}
	}
	s.preferences[s.preferenceKey(field)] = value
	if a.desk.prefs.NewWorkspacePreferences == nil {
		a.desk.prefs.NewWorkspacePreferences = map[string]string{}
	}
	a.desk.prefs.NewWorkspacePreferences[s.preferenceKey(field)] = value
	a.persistSoon()
}
func (s *newWorkspaceState) selectedHost() *newWorkspaceHost {
	for i := range s.hosts {
		if s.hosts[i].ID == s.hostID {
			return &s.hosts[i]
		}
	}
	return nil
}
func (s *newWorkspaceState) selectedProject() *Project {
	h := s.selectedHost()
	if h != nil {
		for i := range h.Projects {
			if h.Projects[i].ID == s.projectID {
				return &h.Projects[i]
			}
		}
	}
	return nil
}
func newWorkspaceProjectAllowed(h newWorkspaceHost, p Project) bool {
	return p.ID != "" && p.Root != "" && (h.Multiplicity || p.Kind == "git" || p.Kind == "unknown" || p.Kind == "")
}
func (s *newWorkspaceState) selectProjectForHost() {
	h := s.selectedHost()
	if h == nil {
		return
	}
	if p := s.selectedProject(); p != nil && newWorkspaceProjectAllowed(*h, *p) {
		if s.sourceDirectory == "" {
			s.sourceDirectory = p.Root
		}
		return
	}
	if h.Err != "" && s.projectID != "" {
		return
	}
	s.projectID, s.sourceDirectory = "", ""
	for _, p := range h.Projects {
		if newWorkspaceProjectAllowed(*h, p) {
			s.projectID, s.sourceDirectory = p.ID, p.Root
			break
		}
	}
}
func (s *newWorkspaceState) acceptHosts(epoch, revision uint64, hosts []newWorkspaceHost) bool {
	if !s.open || s.epoch != epoch || s.loadRevision != revision {
		return false
	}
	s.loading = false
	s.hosts = hosts
	if s.hostID == "" {
		for _, h := range hosts {
			if h.Err != "" {
				continue
			}
			for _, p := range h.Projects {
				if newWorkspaceProjectAllowed(h, p) {
					s.hostID = h.ID
					break
				}
			}
			if s.hostID != "" {
				break
			}
		}
		if s.hostID == "" {
			for _, h := range hosts {
				if h.Err == "" {
					s.hostID = h.ID
					break
				}
			}
		}
		if s.hostID == "" && len(hosts) > 0 {
			s.hostID = hosts[0].ID
		}
	}
	s.selectProjectForHost()
	s.restorePreferences(s.hostID)
	s.restorePartial()
	return true
}
func (s *newWorkspaceState) selectHost(id string) {
	if s.hostID == id {
		return
	}
	s.hostID, s.projectID, s.sourceDirectory = id, "", ""
	s.ref, s.refs, s.search, s.refErr = newWorkspaceRef{}, nil, "", ""
	s.refRevision++
	if id == s.contextHost {
		s.projectID, s.sourceDirectory = s.contextProject, s.contextSource
	}
	s.selectProjectForHost()
	s.restorePreferences(id)
	s.restorePartial()
	s.providers, s.features, s.attachments = nil, nil, nil
	s.provider, s.model, s.mode, s.thinking = "", "", "", ""
	s.restorePreferences(id)
	s.featureValues = map[string]any{}
	s.launchRevision++
}
func (s *newWorkspaceState) selectProject(id string) {
	if s.projectID == id {
		return
	}
	s.projectID, s.sourceDirectory = id, ""
	s.selectProjectForHost()
	s.ref, s.refs, s.search, s.refErr = newWorkspaceRef{}, nil, "", ""
	s.refRevision++
	s.restorePartial()
}
func (s *newWorkspaceState) restorePartial() {
	s.partial = newWorkspaceResult{}
	if r, ok := s.blocked[s.operationKey()]; ok {
		s.partial = r.result
	}
}
func (s *newWorkspaceState) createsWorktree() bool {
	h, p := s.selectedHost(), s.selectedProject()
	return h != nil && p != nil && (!h.Multiplicity || (s.isolation == "worktree" && p.Kind == "git"))
}
func (s *newWorkspaceState) selectedProvider() *newWorkspaceProvider {
	for i := range s.providers {
		if s.providers[i].ID == s.provider {
			return &s.providers[i]
		}
	}
	return nil
}
func (s *newWorkspaceState) selectedModel() *newWorkspaceModel {
	if p := s.selectedProvider(); p != nil {
		for i := range p.Models {
			if p.Models[i].ID == s.model {
				return &p.Models[i]
			}
		}
	}
	return nil
}
func (s *newWorkspaceState) selectedProfile() *newWorkspaceTerminalProfile {
	id := strings.TrimPrefix(s.launch, "profile:")
	for i := range s.profiles {
		if s.profiles[i].ID == id {
			return &s.profiles[i]
		}
	}
	return nil
}
func (s *newWorkspaceState) reconcileLaunchOptions() {
	p := s.selectedProvider()
	if p == nil || p.Enabled != nil && !*p.Enabled || p.Status == "unavailable" || p.Status == "error" {
		s.provider, s.model, s.mode, s.thinking = "", "", "", ""
		for i := range s.providers {
			candidate := &s.providers[i]
			if (candidate.Enabled == nil || *candidate.Enabled) && candidate.Status != "unavailable" && candidate.Status != "error" {
				p, s.provider = candidate, candidate.ID
				break
			}
		}
	}
	if p == nil || s.provider == "" {
		return
	}
	if m := s.selectedModel(); m == nil || m.Selectable != nil && !*m.Selectable {
		s.model, s.thinking = "", ""
		for _, m := range p.Models {
			if m.Selectable != nil && !*m.Selectable {
				continue
			}
			if s.model == "" || m.Default {
				s.model = m.ID
			}
			if m.Default {
				break
			}
		}
	}
	valid := false
	for _, mode := range p.Modes {
		if mode.ID == s.mode {
			valid = true
			break
		}
	}
	if !valid {
		s.mode = p.DefaultMode
		if s.mode == "" && len(p.Modes) > 0 {
			s.mode = p.Modes[0].ID
		}
	}
	if m := s.selectedModel(); m != nil {
		valid = false
		for _, option := range m.Thinking {
			if option.ID == s.thinking {
				valid = true
				break
			}
		}
		if !valid {
			s.thinking = m.DefaultThinking
			if s.thinking == "" {
				for _, option := range m.Thinking {
					if option.Default {
						s.thinking = option.ID
						break
					}
				}
			}
		}
	}
}
func (s *newWorkspaceState) operationKey() string {
	return s.hostID + "\x00" + s.projectID + "\x00" + strings.TrimSpace(s.sourceDirectory)
}
func (s *newWorkspaceState) beginCreate() (newWorkspaceRequest, error) {
	if !s.open || s.loading || s.busy {
		return newWorkspaceRequest{}, errors.New("Workspace creation is already running or still loading.")
	}
	h, p := s.selectedHost(), s.selectedProject()
	if h == nil {
		return newWorkspaceRequest{}, errors.New("Choose a host.")
	}
	if h.Err != "" {
		return newWorkspaceRequest{}, fmt.Errorf("Host unavailable: %s", h.Err)
	}
	if p == nil || !newWorkspaceProjectAllowed(*h, *p) {
		return newWorkspaceRequest{}, errors.New("Choose a project that supports new workspaces on this host.")
	}
	path := strings.TrimSpace(s.sourceDirectory)
	if path == "" {
		return newWorkspaceRequest{}, errors.New("Choose a source directory.")
	}
	key := s.operationKey()
	if _, ok := s.blocked[key]; ok {
		return newWorkspaceRequest{}, errors.New("The previous creation may have succeeded. Open its result before creating another workspace here.")
	}
	launch, prompt := s.launch, strings.TrimSpace(s.prompt)
	identity := rand.Text()
	source := map[string]any{"kind": "directory", "path": path, "projectId": p.ID}
	if s.createsWorktree() {
		source = map[string]any{"kind": "worktree", "cwd": path, "projectId": p.ID, "worktreeSlug": "workspace-" + strings.ToLower(rand.Text()[:10])}
		if s.ref.Number > 0 {
			forge := s.ref.Forge
			if forge == "" {
				forge = "github"
			}
			checkout := map[string]any{"kind": "change_request", "forge": forge, "number": s.ref.Number}
			if s.ref.ProjectPath != "" {
				checkout["projectPath"] = s.ref.ProjectPath
			}
			source["action"], source["checkoutSource"] = "checkout", checkout
			if s.ref.RefName != "" {
				source["refName"] = s.ref.RefName
			}
			if forge == "github" {
				source["githubPrNumber"] = s.ref.Number
			}
		} else if s.ref.RefName != "" {
			source["action"], source["refName"] = "branch-off", s.ref.RefName
		}
	}
	body := map[string]any{"source": source}
	if h.Receipts || h.CreationLifecycle {
		body["idempotencyKey"] = identity
	}
	if h.CreationLifecycle {
		body["subscribe"] = false
	}
	if launch == "chat" {
		provider, model := s.selectedProvider(), s.selectedModel()
		if provider == nil || provider.Enabled != nil && !*provider.Enabled || provider.Status == "unavailable" || provider.Status == "error" || model == nil || model.Selectable != nil && !*model.Selectable {
			return newWorkspaceRequest{}, errors.New("Choose an available provider and model.")
		}
		config := map[string]any{"provider": s.provider, "cwd": path}
		if s.model != "" {
			config["model"] = s.model
		}
		if s.mode != "" {
			config["modeId"] = s.mode
		}
		if s.thinking != "" {
			config["thinkingOptionId"] = s.thinking
		}
		if len(s.featureValues) > 0 {
			config["featureValues"] = s.featureValues
		}
		agent := map[string]any{"config": config}
		if prompt != "" {
			agent["initialPrompt"] = prompt
		}
		if prompt != "" || len(s.attachments) > 0 || len(s.files) > 0 {
			agent["clientMessageId"] = identity + ":initial-message"
		}
		if len(s.attachments) > 0 {
			agent["attachments"] = s.attachments
		}
		body["agent"] = agent
		if prompt != "" || len(s.attachments) > 0 {
			naming := map[string]any{}
			if prompt != "" {
				naming["prompt"] = prompt
			}
			if len(s.attachments) > 0 {
				naming["attachments"] = s.attachments
			}
			body["firstAgentContext"] = naming
		}
	} else if launch == "shell" || strings.HasPrefix(launch, "profile:") {
		if launch != "shell" && s.selectedProfile() == nil {
			return newWorkspaceRequest{}, errors.New("Choose an available terminal profile.")
		}
		prompt = strings.TrimSpace(s.terminalPrompt)
		if profile := s.selectedProfile(); profile != nil && !newWorkspaceProfileTakesPrompt(*profile) {
			prompt = ""
		}
		if prompt != "" {
			body["firstAgentContext"] = map[string]any{"prompt": prompt}
		}
	} else {
		return newWorkspaceRequest{}, errors.New("Choose a launch option.")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return newWorkspaceRequest{}, err
	}
	r := newWorkspaceRequest{epoch: s.epoch, hostID: s.hostID, key: key, identity: identity, payload: raw, lifecycle: h.CreationLifecycle, launch: launch, prompt: prompt, profile: s.selectedProfile(), files: append([]newWorkspaceFile(nil), s.files...)}
	if s.blocked == nil {
		s.blocked = map[string]newWorkspaceRequest{}
	}
	s.blocked[key], s.busy, s.err = r, true, ""
	return r, nil
}
func (s *newWorkspaceState) finishCreate(r newWorkspaceRequest, result newWorkspaceResult) bool {
	previous, ok := s.blocked[r.key]
	if !ok || previous.identity != r.identity {
		return false
	}
	if result.partial {
		previous.result = result
		s.blocked[r.key] = previous
	}
	if !result.unknown && !result.partial {
		delete(s.blocked, r.key)
	}
	if !s.open || r.epoch != s.epoch || r.hostID != s.hostID {
		return false
	}
	s.busy, s.err = false, result.err
	if result.unknown {
		s.err = "Creation result unknown. Open the workspace list or refresh the result before creating another workspace. " + result.err
	}
	if result.partial {
		s.partial = result
		return false
	}
	s.partial = newWorkspaceResult{}
	return result.workspaceID != "" && result.err == "" && !result.unknown
}
func newWorkspaceDecodeResult(raw json.RawMessage, subscription bool) newWorkspaceResult {
	type progress struct {
		Phase          string          `json:"phase"`
		Workspace      *Workspace      `json:"workspace"`
		WorkspaceID    string          `json:"workspaceId"`
		Agent          json.RawMessage `json:"agent"`
		AgentID        string          `json:"agentId"`
		Error          *string         `json:"error"`
		OutcomeUnknown bool            `json:"outcomeUnknown"`
	}
	var ack struct {
		Workspace *Workspace      `json:"workspace"`
		Agent     json.RawMessage `json:"agent"`
		Error     *string         `json:"error"`
		Creation  *progress       `json:"creation"`
		Snapshot  *progress       `json:"snapshot"`
	}
	if json.Unmarshal(raw, &ack) != nil {
		return newWorkspaceResult{err: "Invalid workspace creation response.", unknown: true}
	}
	p := ack.Creation
	if subscription {
		p = ack.Snapshot
	}
	result := newWorkspaceResult{}
	if p != nil {
		if p.Workspace != nil {
			ack.Workspace = p.Workspace
		}
		if ack.Workspace == nil && p.WorkspaceID != "" {
			ack.Workspace = &Workspace{ID: p.WorkspaceID}
		}
		result.agentID = p.AgentID
		if len(p.Agent) > 0 {
			ack.Agent = p.Agent
		}
		result.unknown = p.OutcomeUnknown || p.Phase != "completed" && p.Phase != "failed"
		if p.Error != nil {
			result.err = *p.Error
		}
		if p.Phase == "failed" && result.err == "" {
			result.err = "Workspace creation failed."
		}
	}
	if ack.Workspace != nil {
		result.workspaceID, result.directory = ack.Workspace.ID, ack.Workspace.Directory
	}
	var agent struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(ack.Agent, &agent) == nil && agent.ID != "" {
		result.agentID = agent.ID
	}
	if ack.Error != nil && *ack.Error != "" {
		result.err = *ack.Error
	}
	if subscription && p == nil {
		result.unknown = true
	}
	if result.workspaceID == "" && result.err == "" {
		result.unknown = true
		result.err = "The host has not returned a created workspace."
	}
	if result.unknown && result.err == "" {
		result.err = "Workspace creation is still in progress."
	}
	result.partial = result.workspaceID != "" && result.err != "" && !result.unknown
	return result
}
func newWorkspaceDefaultRef(branch, upstream string) newWorkspaceRef {
	if branch == "" {
		return newWorkspaceRef{}
	}
	ref, label := upstream, branch+" (local)"
	if ref == "" {
		ref = "refs/heads/" + branch
	} else {
		label = strings.TrimPrefix(ref, "refs/remotes/")
		if remote, rest, ok := strings.Cut(label, "/"); ok {
			label = rest + " (" + remote + ")"
		}
	}
	return newWorkspaceRef{Label: label, RefName: ref}
}
func newWorkspaceBranchOptions(branches []newWorkspaceBranch) []newWorkspaceRef {
	var refs []newWorkspaceRef
	for _, b := range branches {
		if b.HasLocal == nil && b.HasRemote == nil {
			refs = append(refs, newWorkspaceRef{Label: b.Name, RefName: b.Name})
			continue
		}
		local, remote := b.HasLocal != nil && *b.HasLocal, b.HasRemote != nil && *b.HasRemote
		if remote {
			refs = append(refs, newWorkspaceRef{Label: b.Name + " (origin)", RefName: "refs/remotes/origin/" + b.Name})
		}
		equal := b.LocalAhead != nil && b.LocalBehind != nil && *b.LocalAhead == 0 && *b.LocalBehind == 0
		if local && (!remote || !equal) {
			refs = append(refs, newWorkspaceRef{Label: b.Name + " (local)", RefName: "refs/heads/" + b.Name})
		}
	}
	return refs
}
func newWorkspaceReadHosts(ctx context.Context, sources []navigationSource) []newWorkspaceHost {
	hosts := make([]newWorkspaceHost, 0, len(sources))
	for _, src := range sources {
		h := newWorkspaceHost{ID: src.ID, Name: src.Name}
		var info struct {
			Features struct {
				WorkspaceMultiplicity    bool `json:"workspaceMultiplicity"`
				ForgeSearch              bool `json:"forgeSearch"`
				CreationLifecycle        bool `json:"creationLifecycle"`
				WorkspaceRequestReceipts bool `json:"workspaceRequestReceipts"`
			} `json:"features"`
		}
		_ = json.Unmarshal(src.Info, &info)
		h.Multiplicity, h.ForgeSearch, h.CreationLifecycle, h.Receipts = info.Features.WorkspaceMultiplicity, info.Features.ForgeSearch, info.Features.CreationLifecycle, info.Features.WorkspaceRequestReceipts
		if src.Err != nil {
			h.Err = src.Err.Error()
		} else if src.Client == nil {
			h.Err = "Host disconnected."
		} else {
			raw, err := src.Client.Call(ctx, "project.list.request", json.RawMessage(`{}`))
			var response struct {
				Projects []Project `json:"projects"`
				Error    *string   `json:"error"`
			}
			if err == nil {
				err = json.Unmarshal(raw, &response)
			}
			if err == nil && response.Error != nil {
				err = errors.New(*response.Error)
			}
			if err == nil && response.Projects == nil {
				err = errors.New("Invalid project list response.")
			}
			if err != nil {
				h.Err = err.Error()
			} else {
				h.Projects = response.Projects
			}
		}
		hosts = append(hosts, h)
	}
	return hosts
}
func newWorkspaceCreate(ctx context.Context, c caller, payload json.RawMessage) newWorkspaceResult {
	var body map[string]json.RawMessage
	if json.Unmarshal(payload, &body) != nil {
		return newWorkspaceResult{err: "Invalid workspace request."}
	}
	var source map[string]any
	if json.Unmarshal(body["source"], &source) != nil {
		return newWorkspaceResult{err: "Invalid workspace source."}
	}
	if source["kind"] == "worktree" && source["refName"] == nil && source["checkoutSource"] == nil {
		statusBody, _ := json.Marshal(map[string]any{"cwd": source["cwd"]})
		raw, err := c.Call(ctx, "checkout_status_request", statusBody)
		var status struct {
			CurrentBranch *string `json:"currentBranch"`
			UpstreamRef   *string `json:"upstreamRef"`
			Error         *string `json:"error"`
		}
		if err == nil {
			err = json.Unmarshal(raw, &status)
		}
		if err == nil && status.Error != nil {
			err = errors.New(*status.Error)
		}
		if err != nil {
			return newWorkspaceResult{err: "Could not read the starting branch: " + err.Error()}
		}
		if status.CurrentBranch != nil {
			upstream := ""
			if status.UpstreamRef != nil {
				upstream = *status.UpstreamRef
			}
			base := newWorkspaceDefaultRef(*status.CurrentBranch, upstream)
			if base.RefName != "" {
				source["action"], source["refName"] = "branch-off", base.RefName
			}
		}
		body["source"], _ = json.Marshal(source)
		payload, _ = json.Marshal(body)
	}
	raw, err := c.Call(ctx, "workspace.create.request", payload)
	if err != nil {
		return newWorkspaceResult{err: err.Error(), unknown: errors.Is(err, client.ErrOutcomeUnknown)}
	}
	return newWorkspaceDecodeResult(raw, false)
}

// Older hosts create the workspace first, then create its agent in the returned
// directory. Keep the workspace if the second operation fails or disconnects.
// COMPAT(creationLifecycle): mirror the RN separate agent launch until 2027-03-11.
func newWorkspaceCreateForHost(ctx context.Context, daemon caller, payload json.RawMessage, lifecycle bool) newWorkspaceResult {
	if lifecycle {
		return newWorkspaceCreate(ctx, daemon, payload)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(payload, &body); err != nil {
		return newWorkspaceResult{err: err.Error()}
	}
	agentPayload, hasAgent := body["agent"]
	if !hasAgent {
		return newWorkspaceCreate(ctx, daemon, payload)
	}
	delete(body, "agent")
	workspacePayload, err := json.Marshal(body)
	if err != nil {
		return newWorkspaceResult{err: err.Error()}
	}
	result := newWorkspaceCreate(ctx, daemon, workspacePayload)
	if result.workspaceID == "" || result.err != "" || result.unknown {
		return result
	}
	var agent map[string]json.RawMessage
	if err = json.Unmarshal(agentPayload, &agent); err != nil {
		result.err = err.Error()
		result.partial = true
		return result
	}
	var config map[string]json.RawMessage
	if err = json.Unmarshal(agent["config"], &config); err != nil {
		result.err = err.Error()
		result.partial = true
		return result
	}
	if result.directory == "" {
		result.err = "Workspace created, but the host returned no launch directory."
		result.partial = true
		return result
	}
	config["cwd"] = mustJSON(result.directory)
	agent["config"] = mustJSON(config)
	agent["workspaceId"] = mustJSON(result.workspaceID)
	raw, err := daemon.Call(ctx, "create_agent_request", mustJSON(agent))
	if err != nil {
		result.err = "Workspace created; agent launch failed: " + err.Error()
		result.partial = true
		result.unknown = errors.Is(err, client.ErrOutcomeUnknown)
		return result
	}
	var ack struct {
		Agent *Agent  `json:"agent"`
		Error *string `json:"error"`
	}
	if err = json.Unmarshal(raw, &ack); err != nil {
		result.err = "Workspace created; invalid agent response."
		result.partial, result.unknown = true, true
		return result
	}
	if ack.Error != nil && *ack.Error != "" {
		result.err = *ack.Error
		result.partial = true
		return result
	}
	if ack.Agent == nil || ack.Agent.ID == "" {
		result.err = "Workspace created; the agent result is unknown."
		result.partial, result.unknown = true, true
		return result
	}
	result.agentID = ack.Agent.ID
	return result
}
func newWorkspaceResolveTerminal(p newWorkspaceTerminalProfile, prompt string) (string, []string) {
	args := make([]string, 0, len(p.Args))
	for _, arg := range p.Args {
		_, v, assignment := strings.Cut(arg, "=")
		if prompt == "" && (arg == "{{{prompt}}}" || assignment && v == "{{{prompt}}}") {
			continue
		}
		args = append(args, strings.ReplaceAll(arg, "{{{prompt}}}", prompt))
	}
	return strings.ReplaceAll(p.Command, "{{{prompt}}}", prompt), args
}
func newWorkspaceProfileTakesPrompt(p newWorkspaceTerminalProfile) bool {
	if strings.Contains(p.Command, "{{{prompt}}}") {
		return true
	}
	for _, a := range p.Args {
		if strings.Contains(a, "{{{prompt}}}") {
			return true
		}
	}
	return false
}
func newWorkspaceAdoptProfiles(ps []newWorkspaceTerminalProfile) []newWorkspaceTerminalProfile {
	options := map[string]string{"claude": "--add-dir --agent --agents --allowed-tools --allowedTools --append-system-prompt --betas --debug-file --disallowed-tools --disallowedTools --effort --fallback-model --file --input-format --json-schema --max-budget-usd --mcp-config --model --name --output-format --permission-mode --plugin-dir --plugin-url --remote-control-session-name-prefix --settings --system-prompt --system-prompt-file --tools -n", "codex": "--add-dir --ask-for-approval --cd --config --disable --enable --image --local-provider --model --profile --remote --remote-auth-token-env --sandbox -C -a -c -i -m -p -s", "opencode": "--agent --cors --hostname --log-level --mdns-domain --model --port --prompt --replay-limit --session -m -s", "pi": "--api-key --append-system-prompt --exclude-tools --export --extension --fork --mode --model --models --name --prompt-template --provider --session --session-dir --session-id --skill --system-prompt --theme --thinking --tools -e -n -t -xt"}
	out := append([]newWorkspaceTerminalProfile(nil), ps...)
	for i, p := range out {
		cmd := strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(filepath.Base(strings.ReplaceAll(p.Command, "\\", "/"))), ".exe"), ".cmd")
		allowed, ok := options[cmd]
		if !ok || newWorkspaceProfileTakesPrompt(p) {
			continue
		}
		pos, expect := false, false
		for _, a := range p.Args {
			if expect {
				expect = false
				continue
			}
			if a == "--" || !strings.HasPrefix(a, "-") {
				pos = true
				break
			}
			option, _, inline := strings.Cut(a, "=")
			expect = !inline && strings.Contains(" "+allowed+" ", " "+option+" ")
		}
		if pos {
			continue
		}
		arg := "{{{prompt}}}"
		if cmd == "opencode" {
			arg = "--prompt=" + arg
		}
		out[i].Args = append(append([]string(nil), p.Args...), arg)
	}
	return out
}
func defaultNewWorkspaceProfiles() []newWorkspaceTerminalProfile {
	return []newWorkspaceTerminalProfile{{ID: "claude", Name: "Claude Code", Command: "claude", Args: []string{"{{{prompt}}}"}}, {ID: "codex", Name: "Codex", Command: "codex", Args: []string{"{{{prompt}}}"}}, {ID: "opencode", Name: "OpenCode", Command: "opencode", Args: []string{"--prompt={{{prompt}}}"}}, {ID: "pi", Name: "Pi", Command: "pi", Args: []string{"{{{prompt}}}"}}}
}

func (a *App) openNewWorkspace() {
	if a.owner != nil {
		a.owner.openNewWorkspace()
		return
	}
	project, source := "", ""
	for _, w := range a.workspaces {
		if w.ID == a.desk.workspace {
			project, source = w.ProjectID, w.Root
			break
		}
	}
	a.openNewWorkspaceContext(a.desk.prefs.ActiveHost, project, source)
}
func (a *App) openNewWorkspaceContext(host, project, source string) {
	if a.owner != nil {
		a.owner.openNewWorkspaceContext(host, project, source)
		return
	}
	a.showDestination("new-workspace")
	s := &a.desk.newWorkspace
	s.preferences = a.desk.prefs.NewWorkspacePreferences
	s.openContext(host, project, source)
	a.loadNewWorkspaceHosts()
}
func (a *App) closeNewWorkspace() { a.desk.newWorkspace.close() }
func (a *App) loadNewWorkspaceHosts() {
	s := &a.desk.newWorkspace
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	s.cancel, s.loading = cancel, true
	s.loadRevision++
	gen, epoch, revision := a.generation, s.epoch, s.loadRevision
	go func() {
		defer cancel()
		sources, cleanup := a.navigationSources(ctx)
		defer cleanup()
		hosts := newWorkspaceReadHosts(ctx, sources)
		a.update(gen, func() {
			if a.desk.newWorkspace.acceptHosts(epoch, revision, hosts) {
				a.loadNewWorkspaceRefs()
				a.loadNewWorkspaceLaunchOptions()
			}
		})
	}()
}
func (a *App) loadNewWorkspaceLaunchOptions() {
	s := &a.desk.newWorkspace
	s.launchRevision++
	gen, epoch, revision, host, cwd := a.generation, s.epoch, s.launchRevision, s.hostID, s.sourceDirectory
	s.launchLoading = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
		defer cancel()
		sources, cleanup := a.navigationSources(ctx)
		defer cleanup()
		var daemon caller
		for _, src := range sources {
			if src.ID == host && src.Err == nil && src.Client != nil {
				daemon = src.Client
				break
			}
		}
		var profiles []newWorkspaceTerminalProfile
		var providers []newWorkspaceProvider
		var messages []string
		configLoaded := false
		if daemon == nil {
			messages = append(messages, "Host disconnected.")
		} else {
			raw, err := daemon.Call(ctx, "get_daemon_config_request", json.RawMessage(`{}`))
			if err != nil {
				messages = append(messages, "Terminal profiles: "+err.Error())
			} else {
				var response struct {
					Config *struct {
						TerminalProfiles []newWorkspaceTerminalProfile `json:"terminalProfiles"`
					} `json:"config"`
					Error *string `json:"error"`
				}
				if err = json.Unmarshal(raw, &response); err != nil {
					messages = append(messages, err.Error())
				} else if response.Error != nil {
					messages = append(messages, *response.Error)
				} else if response.Config == nil {
					messages = append(messages, "The host returned no terminal profile configuration.")
				} else {
					profiles = response.Config.TerminalProfiles
					configLoaded = true
				}
			}
			raw, err = daemon.Call(ctx, "get_providers_snapshot_request", mustJSON(map[string]any{"cwd": cwd}))
			if err != nil {
				messages = append(messages, "Providers: "+err.Error())
			} else {
				var response struct {
					Entries []newWorkspaceProvider `json:"entries"`
					Error   *string                `json:"error"`
				}
				if err = json.Unmarshal(raw, &response); err != nil {
					messages = append(messages, err.Error())
				} else if response.Error != nil {
					messages = append(messages, *response.Error)
				} else {
					providers = response.Entries
				}
			}
		}
		if configLoaded && profiles == nil {
			profiles = defaultNewWorkspaceProfiles()
		}
		profiles = newWorkspaceAdoptProfiles(profiles)
		a.update(gen, func() {
			s := &a.desk.newWorkspace
			if !s.open || s.epoch != epoch || s.launchRevision != revision || s.hostID != host || s.sourceDirectory != cwd {
				return
			}
			s.launchLoading = false
			s.profiles, s.providers = profiles, providers
			s.launchErr = strings.Join(messages, "\n")
			if strings.HasPrefix(s.launch, "profile:") && s.selectedProfile() == nil {
				s.launch = "chat"
			}
			s.reconcileLaunchOptions()
			if s.provider != "" {
				a.loadNewWorkspaceProviderOptions()
			}
		})
	}()
}
func (a *App) loadNewWorkspaceProviderOptions() {
	s := &a.desk.newWorkspace
	if s.selectedProvider() == nil {
		return
	}
	s.launchRevision++
	s.launchLoading = true
	gen, host, provider, cwd, epoch, revision := a.generation, s.hostID, s.provider, s.sourceDirectory, s.epoch, s.launchRevision
	draft := map[string]any{"provider": provider, "cwd": cwd}
	if s.model != "" {
		draft["model"] = s.model
	}
	if s.mode != "" {
		draft["modeId"] = s.mode
	}
	if s.thinking != "" {
		draft["thinkingOptionId"] = s.thinking
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
		defer cancel()
		sources, cleanup := a.navigationSources(ctx)
		defer cleanup()
		var daemon caller
		for _, src := range sources {
			if src.ID == host && src.Err == nil && src.Client != nil {
				daemon = src.Client
				break
			}
		}
		var models struct {
			Models []newWorkspaceModel `json:"models"`
			Error  *string             `json:"error"`
		}
		var modes struct {
			Modes []newWorkspaceOption `json:"modes"`
			Error *string              `json:"error"`
		}
		var features struct {
			Features []newWorkspaceFeature `json:"features"`
			Error    *string               `json:"error"`
		}
		var modelErr, modeErr, featureErr error
		if daemon == nil {
			modelErr = errors.New("Host disconnected.")
		} else {
			body := mustJSON(map[string]any{"provider": provider, "cwd": cwd})
			raw, err := daemon.Call(ctx, "list_provider_models_request", body)
			modelErr = err
			if err == nil {
				modelErr = json.Unmarshal(raw, &models)
			}
			if models.Error != nil {
				modelErr = errors.New(*models.Error)
			}
			raw, err = daemon.Call(ctx, "list_provider_modes_request", body)
			modeErr = err
			if err == nil {
				modeErr = json.Unmarshal(raw, &modes)
			}
			if modes.Error != nil {
				modeErr = errors.New(*modes.Error)
			}
			raw, err = daemon.Call(ctx, "list_provider_features_request", mustJSON(map[string]any{"draftConfig": draft}))
			featureErr = err
			if err == nil {
				featureErr = json.Unmarshal(raw, &features)
			}
			if features.Error != nil {
				featureErr = errors.New(*features.Error)
			}
		}
		a.update(gen, func() {
			s := &a.desk.newWorkspace
			if !s.open || s.epoch != epoch || s.launchRevision != revision || s.hostID != host || s.provider != provider || s.sourceDirectory != cwd {
				return
			}
			s.launchLoading = false
			if p := s.selectedProvider(); p != nil {
				if modelErr == nil {
					p.Models = models.Models
				}
				if modeErr == nil {
					p.Modes = modes.Modes
				}
			}
			if featureErr == nil {
				s.features = features.Features
				values := map[string]any{}
				for _, f := range s.features {
					value, ok := s.featureValues[f.ID]
					if !ok {
						value = f.Value
					}
					values[f.ID] = value
				}
				s.featureValues = values
			}
			s.launchErr = strings.Join(errorStrings(modelErr, modeErr, featureErr), "\n")
			s.reconcileLaunchOptions()
		})
	}()
}
func errorStrings(es ...error) []string {
	var out []string
	for _, e := range es {
		if e != nil {
			out = append(out, e.Error())
		}
	}
	return out
}
func (a *App) loadNewWorkspaceRefs() {
	s := &a.desk.newWorkspace
	s.refRevision++
	s.refs, s.refErr, s.refsLoading = nil, "", false
	if !s.open || !s.createsWorktree() || strings.TrimSpace(s.sourceDirectory) == "" {
		return
	}
	h := s.selectedHost()
	if h == nil || h.Err != "" {
		return
	}
	gen, epoch, revision, host, cwd, query, forge := a.generation, s.epoch, s.refRevision, s.hostID, strings.TrimSpace(s.sourceDirectory), strings.TrimSpace(s.search), h.ForgeSearch
	s.refsLoading = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		sources, cleanup := a.navigationSources(ctx)
		defer cleanup()
		var daemon caller
		for _, source := range sources {
			if source.ID == host && source.Err == nil && source.Client != nil {
				daemon = source.Client
				break
			}
		}
		var refs []newWorkspaceRef
		var base newWorkspaceRef
		var messages []string
		if daemon == nil {
			messages = append(messages, "Host disconnected.")
		} else {
			raw, err := daemon.Call(ctx, "checkout_status_request", mustJSON(map[string]any{"cwd": cwd}))
			var status struct {
				CurrentBranch *string `json:"currentBranch"`
				UpstreamRef   *string `json:"upstreamRef"`
				Error         *string `json:"error"`
			}
			if err == nil {
				err = json.Unmarshal(raw, &status)
			}
			if status.Error != nil {
				err = errors.New(*status.Error)
			}
			if err != nil {
				messages = append(messages, err.Error())
			} else if status.CurrentBranch != nil {
				upstream := ""
				if status.UpstreamRef != nil {
					upstream = *status.UpstreamRef
				}
				base = newWorkspaceDefaultRef(*status.CurrentBranch, upstream)
			}
			raw, err = daemon.Call(ctx, "branch_suggestions_request", mustJSON(map[string]any{"cwd": cwd, "query": query, "limit": 20}))
			var branches struct {
				Branches []string             `json:"branches"`
				Details  []newWorkspaceBranch `json:"branchDetails"`
				Error    *string              `json:"error"`
			}
			if err == nil {
				err = json.Unmarshal(raw, &branches)
			}
			if branches.Error != nil {
				err = errors.New(*branches.Error)
			}
			if err != nil {
				messages = append(messages, err.Error())
			} else {
				if len(branches.Details) == 0 {
					for _, name := range branches.Branches {
						branches.Details = append(branches.Details, newWorkspaceBranch{Name: name})
					}
				}
				refs = newWorkspaceBranchOptions(branches.Details)
			}
			kind, kinds := "forge.search.request", []string{"change_request"}
			// COMPAT(forgeSearch): matches the RN GitHub search path until 2027-01-17.
			if !forge {
				kind, kinds = "github_search_request", []string{"github-pr"}
			}
			raw, err = daemon.Call(ctx, kind, mustJSON(map[string]any{"cwd": cwd, "query": query, "limit": 20, "kinds": kinds}))
			var response struct {
				Items []struct {
					Kind        string  `json:"kind"`
					Forge       string  `json:"forge"`
					Title       string  `json:"title"`
					HeadRefName *string `json:"headRefName"`
					ProjectPath string  `json:"projectPath"`
					Number      int     `json:"number"`
				} `json:"items"`
				AuthState             string  `json:"authState"`
				FeaturesEnabled       *bool   `json:"featuresEnabled"`
				GithubFeaturesEnabled *bool   `json:"githubFeaturesEnabled"`
				Error                 *string `json:"error"`
			}
			if err == nil {
				err = json.Unmarshal(raw, &response)
			}
			if response.Error != nil {
				err = errors.New(*response.Error)
			}
			authenticated := response.AuthState == "authenticated" || !forge && response.AuthState == "" && (response.FeaturesEnabled == nil || *response.FeaturesEnabled) && (response.GithubFeaturesEnabled == nil || *response.GithubFeaturesEnabled)
			if err != nil {
				messages = append(messages, err.Error())
			} else if !authenticated {
				messages = append(messages, "Sign in to the host forge to search change requests.")
			} else {
				for _, item := range response.Items {
					if item.Kind != "change_request" && item.Kind != "pr" {
						continue
					}
					forgeName := item.Forge
					if forgeName == "" {
						forgeName = "github"
					}
					ref := ""
					if item.HeadRefName != nil {
						ref = *item.HeadRefName
					}
					refs = append(refs, newWorkspaceRef{Label: fmt.Sprintf("%s #%d %s", forgeName, item.Number, item.Title), RefName: ref, Forge: forgeName, Number: item.Number, ProjectPath: item.ProjectPath})
				}
			}
		}
		a.update(gen, func() {
			s := &a.desk.newWorkspace
			if !s.open || s.epoch != epoch || s.refRevision != revision || s.hostID != host || strings.TrimSpace(s.sourceDirectory) != cwd {
				return
			}
			s.refsLoading, s.refs, s.refErr = false, refs, strings.Join(messages, "\n")
			if s.ref.RefName == "" && s.ref.Number == 0 {
				s.ref = base
			}
		})
	}()
}
func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func (a *App) pickNewWorkspaceFiles() {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: "Attach files", Multiple: true})
	if err != nil {
		a.desk.newWorkspace.err = err.Error()
		return
	}
	s := &a.desk.newWorkspace
	for _, path := range paths {
		data, e := os.ReadFile(path)
		if e != nil {
			s.err = e.Error()
			continue
		}
		mimeType := mime.TypeByExtension(filepath.Ext(path))
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		s.files = append(s.files, newWorkspaceFile{Name: filepath.Base(path), MIME: mimeType, Data: data})
	}
}

type newWorkspaceUploader interface {
	UploadFile(context.Context, string, string, []byte) (json.RawMessage, error)
}

func newWorkspacePrepareFiles(ctx context.Context, daemon caller, r newWorkspaceRequest) (json.RawMessage, error) {
	if r.launch != "chat" || len(r.files) == 0 {
		return r.payload, nil
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(r.payload, &body); err != nil {
		return nil, err
	}
	var agent map[string]json.RawMessage
	if err := json.Unmarshal(body["agent"], &agent); err != nil {
		return nil, err
	}
	var attachments []json.RawMessage
	if len(agent["attachments"]) > 0 {
		if err := json.Unmarshal(agent["attachments"], &attachments); err != nil {
			return nil, err
		}
	}
	var images []map[string]string
	for _, file := range r.files {
		if strings.HasPrefix(file.MIME, "image/") {
			images = append(images, map[string]string{"data": base64.StdEncoding.EncodeToString(file.Data), "mimeType": file.MIME})
			continue
		}
		uploader, ok := daemon.(newWorkspaceUploader)
		if !ok {
			return nil, errors.New("This host connection cannot upload files.")
		}
		attachment, err := uploader.UploadFile(ctx, file.Name, file.MIME, file.Data)
		if err != nil {
			return nil, fmt.Errorf("Could not attach %s: %w", file.Name, err)
		}
		attachments = append(attachments, attachment)
	}
	if len(images) > 0 {
		agent["images"] = mustJSON(images)
	}
	if len(attachments) > 0 {
		agent["attachments"] = mustJSON(attachments)
	}
	var naming map[string]json.RawMessage
	if len(body["firstAgentContext"]) > 0 {
		if err := json.Unmarshal(body["firstAgentContext"], &naming); err != nil {
			return nil, err
		}
	}
	if naming == nil {
		naming = map[string]json.RawMessage{}
	}
	if len(attachments) > 0 {
		naming["attachments"] = mustJSON(attachments)
	}
	if len(naming) > 0 {
		body["firstAgentContext"] = mustJSON(naming)
	}
	body["agent"] = mustJSON(agent)
	return json.Marshal(body)
}

func (a *App) submitNewWorkspace(refresh bool) {
	s := &a.desk.newWorkspace
	var r newWorkspaceRequest
	if refresh {
		var ok bool
		r, ok = s.blocked[s.operationKey()]
		if !ok || s.busy {
			return
		}
		if !r.lifecycle {
			s.err = "This host cannot look up creation results. Open its workspace list before another submission."
			return
		}
		r.epoch = s.epoch
		s.busy = true
	} else {
		var err error
		r, err = s.beginCreate()
		if err != nil {
			s.err = err.Error()
			return
		}
	}
	gen := a.generation
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		sources, cleanup := a.navigationSources(ctx)
		defer cleanup()
		result := newWorkspaceResult{err: "Host disconnected.", unknown: refresh}
		for _, src := range sources {
			if src.ID != r.hostID {
				continue
			}
			if src.Err != nil {
				result.err = src.Err.Error()
				break
			}
			if src.Client == nil {
				break
			}
			if refresh {
				raw, err := src.Client.Call(ctx, "creation.subscribe.request", mustJSON(map[string]any{"kind": "workspace", "idempotencyKey": r.identity, "subscribe": false}))
				if err != nil {
					result = newWorkspaceResult{err: err.Error(), unknown: true}
				} else {
					result = newWorkspaceDecodeResult(raw, true)
				}
			} else {
				payload, err := newWorkspacePrepareFiles(ctx, src.Client, r)
				if err != nil {
					result = newWorkspaceResult{err: err.Error()}
				} else {
					result = newWorkspaceCreateForHost(ctx, src.Client, payload, r.lifecycle)
				}
			}
			break
		}
		if result.workspaceID != "" && result.err == "" && !result.unknown && r.launch != "chat" {
			result = a.launchCreatedWorkspace(ctx, sources, r, result)
		}
		a.update(gen, func() {
			if a.desk.newWorkspace.finishCreate(r, result) {
				a.openNewWorkspaceResult(r.hostID, result)
			}
		})
	}()
}
func (a *App) openNewWorkspaceResult(host string, result newWorkspaceResult) {
	if result.terminalID != "" {
		a.openCreatedWorkspaceTarget(host, result.workspaceID, "terminal", result.terminalID)
	} else if result.agentID != "" {
		a.openCreatedWorkspaceTarget(host, result.workspaceID, "agent", result.agentID)
	} else {
		a.openCreatedWorkspace(host, result.workspaceID)
	}
}
func (a *App) launchCreatedWorkspace(ctx context.Context, sources []navigationSource, r newWorkspaceRequest, result newWorkspaceResult) newWorkspaceResult {
	var daemon *client.Client
	for _, source := range sources {
		if source.ID == r.hostID && source.Err == nil {
			daemon, _ = source.Client.(*client.Client)
			break
		}
	}
	if daemon == nil {
		result.err = "Workspace created; host disconnected before launch."
		result.partial = true
		return result
	}
	var terminalID string
	var err error
	if result.directory == "" {
		result.err = "Workspace created, but the host returned no launch directory."
		result.partial = true
		return result
	}
	if r.profile != nil {
		command, args := newWorkspaceResolveTerminal(*r.profile, strings.TrimSpace(r.prompt))
		terminalID, err = daemon.CreateTerminalWithCommand(ctx, result.workspaceID, result.directory, r.profile.Name, command, args)
	} else {
		terminalID, err = daemon.CreateTerminalWithCommand(ctx, result.workspaceID, result.directory, "", "", nil)
	}
	if err != nil {
		result.err = "Workspace created; terminal launch failed: " + err.Error()
		result.partial = true
		result.unknown = errors.Is(err, client.ErrOutcomeUnknown)
		return result
	}
	result.terminalID = terminalID
	if r.profile == nil && strings.TrimSpace(r.prompt) != "" {
		stream, attachErr := daemon.SubscribeTerminal(ctx, terminalID)
		if attachErr != nil {
			result.err = "Terminal created; could not send command: " + attachErr.Error()
			result.partial = true
			return result
		}
		_, err = stream.Write([]byte(strings.TrimSpace(r.prompt) + "\r"))
		_ = stream.Close()
		if err != nil {
			result.err = "Terminal created; command delivery is unknown. Open the terminal before sending it again. " + err.Error()
			result.partial = true
			result.unknown = true
			return result
		}
	}
	return result
}
func (a *App) newWorkspaceView(c *ui.Context) {
	s := &a.desk.newWorkspace
	ui.Column(c).Grow(1).Children(func() {
		ui.Row(c).Padding(12, 16).Gap(12).Children(func() {
			if ui.Button(c, "Navigation").Clicked() {
				a.toggleNavigation()
			}
			ui.Text(c, "New workspace").Bold().Grow(1)
			if ui.Button(c, "Back").Clicked() {
				a.showDestination("home")
			}
		})
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).WidthPercent(100).MaxWidth(800).Padding(24).Gap(14).Children(func() {
				if s.loading {
					ui.Text(c, "Loading hosts and projects…")
					return
				}
				if len(s.hosts) == 0 {
					ui.Text(c, "Add a host to create a workspace.")
					return
				}
				ui.Row(c).Gap(12).Wrap().Children(func() {
					ui.Column(c).Gap(4).Children(func() {
						ui.Text(c, "Project").TextColor(c.Theme().TextMuted)
						var labels, ids []string
						selected := "Choose project"
						if h := s.selectedHost(); h != nil {
							for _, p := range h.Projects {
								if newWorkspaceProjectAllowed(*h, p) {
									label := p.Name
									if label == "" {
										label = p.ID
									}
									label += " · " + p.Root
									labels = append(labels, label)
									ids = append(ids, p.ID)
									if p.ID == s.projectID {
										selected = label
									}
								}
							}
						}
						if ui.Select(c, &selected, labels).Disabled(s.busy || len(labels) == 0).Changed() {
							for i, label := range labels {
								if label == selected {
									s.selectProject(ids[i])
									a.loadNewWorkspaceRefs()
									a.loadNewWorkspaceLaunchOptions()
									break
								}
							}
						}
					})
					ui.Column(c).Gap(4).Children(func() {
						ui.Text(c, "Host").TextColor(c.Theme().TextMuted)
						var labels []string
						selected := "Choose host"
						for _, h := range s.hosts {
							label := h.Name
							if label == "" {
								label = h.ID
							}
							label += " · " + h.ID
							if h.Err != "" {
								label += " (offline)"
							}
							labels = append(labels, label)
							if h.ID == s.hostID {
								selected = label
							}
						}
						if ui.Select(c, &selected, labels).Disabled(s.busy).Changed() {
							for i, label := range labels {
								if label == selected {
									s.selectHost(s.hosts[i].ID)
									a.loadNewWorkspaceRefs()
									a.loadNewWorkspaceLaunchOptions()
									break
								}
							}
						}
					})
					if h, p := s.selectedHost(), s.selectedProject(); h != nil && p != nil && h.Multiplicity && p.Kind == "git" {
						ui.Column(c).Gap(4).Children(func() {
							ui.Text(c, "Isolation").TextColor(c.Theme().TextMuted)
							selected := "Local directory"
							if s.isolation == "worktree" {
								selected = "New worktree"
							}
							if ui.Select(c, &selected, []string{"Local directory", "New worktree"}).Disabled(s.busy).Changed() {
								s.isolation = "local"
								if selected == "New worktree" {
									s.isolation = "worktree"
								}
								a.saveNewWorkspacePreference("isolation", s.isolation)
								a.loadNewWorkspaceRefs()
							}
						})
					}
				})
				if h := s.selectedHost(); h != nil && h.Err != "" {
					ui.Text(c, "Host unavailable: "+h.Err)
				}
				if s.projectID == "" {
					ui.Text(c, "No projects available for new workspaces on this host.")
				}
				if ui.Button(c, "Add project").Disabled(s.busy).Clicked() {
					a.addProjectFromNewWorkspace()
				}
				if ui.Button(c, "Add host").Disabled(s.busy).Clicked() {
					a.desk.editingHost = ""
					a.desk.shell.HostEditor = desktopui.HostEditorState{Open: true}
				}
				if ui.TextInput(c, &s.sourceDirectory).Label("Source directory").Disabled(s.busy).Changed() {
					s.ref, s.refs = newWorkspaceRef{}, nil
					s.refRevision++
					s.restorePartial()
					a.loadNewWorkspaceRefs()
					a.loadNewWorkspaceLaunchOptions()
				}
				if s.createsWorktree() {
					ui.Text(c, "Starting branch or change request").TextColor(c.Theme().TextMuted)
					ui.Row(c).Gap(8).Children(func() {
						ui.TextInput(c, &s.search).Placeholder("Search branches or change requests").Disabled(s.busy).Grow(1)
						if ui.Button(c, "Search").Disabled(s.busy || s.refsLoading).Clicked() {
							a.loadNewWorkspaceRefs()
						}
					})
					labels := []string{"Current checkout"}
					selected := "Current checkout"
					if s.ref.Label != "" {
						selected = s.ref.Label
						labels = append(labels, selected)
					}
					for _, ref := range s.refs {
						if ref.Label != selected {
							labels = append(labels, ref.Label)
						}
					}
					if ui.Select(c, &selected, labels).Disabled(s.busy || s.refsLoading).Changed() {
						if selected == "Current checkout" {
							s.ref = newWorkspaceRef{}
						} else {
							for _, ref := range s.refs {
								if ref.Label == selected {
									s.ref = ref
									break
								}
							}
						}
					}
					if s.refsLoading {
						ui.Text(c, "Loading branches…")
					}
					if s.refErr != "" {
						ui.Text(c, s.refErr)
					}
				}
				if s.launchLoading {
					ui.Text(c, "Loading providers and launch options…")
				}
				if s.launchErr != "" {
					ui.Text(c, s.launchErr)
				}
				labels, ids := []string{"Chat agent", "Shell"}, []string{"chat", "shell"}
				selected := "Chat agent"
				if s.launch == "shell" {
					selected = "Shell"
				}
				for _, p := range s.profiles {
					label := p.Name + " · " + p.ID
					labels = append(labels, label)
					ids = append(ids, "profile:"+p.ID)
					if s.launch == "profile:"+p.ID {
						selected = label
					}
				}
				ui.Text(c, "Launch").TextColor(c.Theme().TextMuted)
				if ui.Select(c, &selected, labels).Disabled(s.busy || s.launchLoading).Changed() {
					for i, label := range labels {
						if label == selected {
							s.launch = ids[i]
							a.saveNewWorkspacePreference("launch", s.launch)
							break
						}
					}
				}
				if s.launch == "chat" {
					a.newWorkspaceChatForm(c)
					if ui.Button(c, "Add files").Disabled(s.busy).Clicked() {
						a.pickNewWorkspaceFiles()
					}
					for i, file := range s.files {
						i, file := i, file
						ui.Row(c).Gap(8).Children(func() {
							ui.Text(c, file.Name).Grow(1)
							if ui.Button(c, "Remove").Disabled(s.busy).Clicked() {
								s.files = append(s.files[:i], s.files[i+1:]...)
							}
						})
					}
				} else {
					ui.TextArea(c, &s.terminalPrompt).Label("Terminal command or prompt").Placeholder("Optional command or prompt").MinHeight(72).Disabled(s.busy)
				}
				if s.err != "" {
					ui.Text(c, s.err)
				}
				if s.partial.workspaceID != "" {
					ui.Text(c, "The workspace was created. Open it to continue.")
					if ui.Button(c, "Open created workspace").Disabled(s.busy).Clicked() {
						a.openNewWorkspaceResult(s.hostID, s.partial)
					}
					if !s.partial.unknown && s.partial.terminalID == "" && s.launch != "chat" {
						if ui.Button(c, "Retry terminal launch").Disabled(s.busy).Clicked() {
							a.retryNewWorkspaceTerminal()
						}
					}
				} else if request, blocked := s.blocked[s.operationKey()]; blocked {
					if request.lifecycle {
						if ui.Button(c, "Refresh creation result").Disabled(s.busy).Clicked() {
							a.submitNewWorkspace(true)
						}
					} else {
						ui.Text(c, "Open this host's workspace list to find the earlier creation. Its result cannot be requested on this host.")
					}
				} else if ui.PrimaryButton(c, "Create workspace").Disabled(s.busy || s.launchLoading || s.refsLoading || s.projectID == "" || strings.TrimSpace(s.sourceDirectory) == "").Clicked() {
					a.submitNewWorkspace(false)
				}
				if ui.Button(c, "Reload hosts and projects").Disabled(s.busy).Clicked() {
					a.loadNewWorkspaceHosts()
				}
			})
		})
	})
}

func (a *App) newWorkspaceChatForm(c *ui.Context) {
	s := &a.desk.newWorkspace
	var labels, ids []string
	selected := "Choose provider"
	for _, p := range s.providers {
		if p.Status == "unavailable" || p.Status == "error" || p.Enabled != nil && !*p.Enabled {
			continue
		}
		label := p.Label
		if label == "" {
			label = p.ID
		}
		labels = append(labels, label)
		ids = append(ids, p.ID)
		if p.ID == s.provider {
			selected = label
		}
	}
	if ui.Select(c, &selected, labels).Disabled(s.busy || len(labels) == 0).Changed() {
		for i, label := range labels {
			if label == selected {
				s.provider, s.model, s.mode, s.thinking = ids[i], "", "", ""
				s.features = nil
				s.featureValues = map[string]any{}
				s.reconcileLaunchOptions()
				a.saveNewWorkspacePreference("provider", s.provider)
				a.loadNewWorkspaceProviderOptions()
				break
			}
		}
	}
	if p := s.selectedProvider(); p != nil {
		labels, ids = nil, nil
		selected = "Choose model"
		for _, m := range p.Models {
			if m.Selectable != nil && !*m.Selectable {
				continue
			}
			label := m.Label
			if label == "" {
				label = m.ID
			}
			labels = append(labels, label)
			ids = append(ids, m.ID)
			if m.ID == s.model {
				selected = label
			}
		}
		if ui.Select(c, &selected, labels).Disabled(s.busy || len(labels) == 0).Changed() {
			for i, label := range labels {
				if label == selected {
					s.model = ids[i]
					s.reconcileLaunchOptions()
					a.saveNewWorkspacePreference("model", s.model)
					a.loadNewWorkspaceProviderOptions()
					break
				}
			}
		}
		if len(p.Modes) > 0 {
			labels, ids = nil, nil
			selected = s.mode
			for _, m := range p.Modes {
				labels = append(labels, m.Label)
				ids = append(ids, m.ID)
				if m.ID == s.mode {
					selected = m.Label
				}
			}
			if ui.Select(c, &selected, labels).Disabled(s.busy).Changed() {
				for i, label := range labels {
					if label == selected {
						s.mode = ids[i]
						a.saveNewWorkspacePreference("mode", s.mode)
						a.loadNewWorkspaceProviderOptions()
						break
					}
				}
			}
		}
		if m := s.selectedModel(); m != nil && len(m.Thinking) > 0 {
			labels, ids = nil, nil
			selected = s.thinking
			for _, option := range m.Thinking {
				labels = append(labels, option.Label)
				ids = append(ids, option.ID)
				if option.ID == s.thinking {
					selected = option.Label
				}
			}
			if ui.Select(c, &selected, labels).Disabled(s.busy).Changed() {
				for i, label := range labels {
					if label == selected {
						s.thinking = ids[i]
						a.saveNewWorkspacePreference("thinking", s.thinking)
						a.loadNewWorkspaceProviderOptions()
						break
					}
				}
			}
		}
		for _, f := range s.features {
			if f.Type == "toggle" {
				value, _ := s.featureValues[f.ID].(bool)
				if ui.Checkbox(c, &value, f.Label).Disabled(s.busy).Changed() {
					s.featureValues[f.ID] = value
				}
			} else if f.Type == "select" && len(f.Options) > 0 {
				labels, ids = nil, nil
				selected = ""
				for _, o := range f.Options {
					labels = append(labels, o.Label)
					ids = append(ids, o.ID)
					if s.featureValues[f.ID] == o.ID {
						selected = o.Label
					}
				}
				ui.Text(c, f.Label)
				if ui.Select(c, &selected, labels).Disabled(s.busy).Changed() {
					for i, label := range labels {
						if label == selected {
							s.featureValues[f.ID] = ids[i]
							break
						}
					}
				}
			}
		}
	}
	ui.TextArea(c, &s.prompt).Label("First message").Placeholder("Describe what you want the agent to do.").MinHeight(88).Disabled(s.busy)
}

func (a *App) retryNewWorkspaceTerminal() {
	s := &a.desk.newWorkspace
	r, ok := s.blocked[s.operationKey()]
	if !ok || s.busy || s.partial.workspaceID == "" || s.partial.unknown || s.partial.terminalID != "" || r.launch == "chat" {
		return
	}
	r.epoch = s.epoch
	result := s.partial
	result.err, result.partial = "", false
	s.busy = true
	gen := a.generation
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		sources, cleanup := a.navigationSources(ctx)
		defer cleanup()
		result = a.launchCreatedWorkspace(ctx, sources, r, result)
		a.update(gen, func() {
			if a.desk.newWorkspace.finishCreate(r, result) {
				a.openNewWorkspaceResult(r.hostID, result)
			}
		})
	}()
}

func (a *App) addProjectFromNewWorkspace() {
	a.requestHostHomeAction(a.desk.newWorkspace.hostID, "project")
}

func (a *App) resumeNewWorkspacePicker() {
	if a.desk.destination != "new-workspace" {
		return
	}
	s := &a.desk.newWorkspace
	if !s.open {
		s.open = true
		s.epoch++
	}
	if host := a.desk.prefs.ActiveHost; host != "" && host != s.hostID {
		s.selectHost(host)
	}
	a.loadNewWorkspaceHosts()
}
