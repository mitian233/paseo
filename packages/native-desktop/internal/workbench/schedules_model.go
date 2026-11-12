package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/getpaseo/paseo/packages/native-desktop/internal/client"
)

type scheduleKey struct{ Host, ID string }
type scheduleCadence struct {
	Type       string `json:"type"`
	Expression string `json:"expression,omitempty"`
	Timezone   string `json:"timezone,omitempty"`
	EveryMS    int64  `json:"everyMs,omitempty"`
}
type scheduleConfig struct {
	Provider  string `json:"provider"`
	CWD       string `json:"cwd"`
	Model     string `json:"model,omitempty"`
	Mode      string `json:"modeId,omitempty"`
	Thinking  string `json:"thinkingOptionId,omitempty"`
	Archive   *bool  `json:"archiveOnFinish,omitempty"`
	Isolation string `json:"isolation,omitempty"`
	Title     string `json:"title,omitempty"`
}
type scheduleTarget struct {
	Type    string         `json:"type"`
	AgentID string         `json:"agentId,omitempty"`
	Config  scheduleConfig `json:"config,omitempty"`
}
type scheduleRecord struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Prompt    string          `json:"prompt"`
	Status    string          `json:"status"`
	CreatedAt string          `json:"createdAt"`
	NextRunAt string          `json:"nextRunAt"`
	LastRunAt string          `json:"lastRunAt"`
	ExpiresAt string          `json:"expiresAt"`
	MaxRuns   *int            `json:"maxRuns"`
	Cadence   scheduleCadence `json:"cadence"`
	Target    scheduleTarget  `json:"target"`
}
type scheduleOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}
type scheduleModel struct {
	ID         string           `json:"id"`
	Label      string           `json:"label"`
	Selectable *bool            `json:"isSelectable"`
	Thinking   []scheduleOption `json:"thinkingOptions"`
}
type scheduleProvider struct {
	ID      string           `json:"provider"`
	Label   string           `json:"label"`
	Status  string           `json:"status"`
	Error   string           `json:"error"`
	Enabled *bool            `json:"enabled"`
	Models  []scheduleModel  `json:"models"`
	Modes   []scheduleOption `json:"modes"`
}
type scheduleHost struct {
	ID, Name, Error, FormError string
	Rows                       []scheduleRecord
	Projects                   []Project
	Providers                  []scheduleProvider
	Agents                     map[string]Agent
	AgentsLoaded, Multiplicity bool
}

func (r scheduleRecord) derived(h scheduleHost, now time.Time) string {
	if expiry, err := time.Parse(time.RFC3339, r.ExpiresAt); err == nil && !expiry.After(now) {
		return "expired"
	}
	if r.Target.Type == "agent" && h.AgentsLoaded {
		if _, ok := h.Agents[r.Target.AgentID]; !ok {
			return "targetGone"
		}
	}
	if r.Status == "completed" {
		return "finished"
	}
	if r.Status == "paused" {
		return "paused"
	}
	return "active"
}
func (r scheduleRecord) title() string {
	if strings.TrimSpace(r.Name) != "" {
		return r.Name
	}
	if r.Target.Type == "agent" {
		return "Heartbeat"
	}
	return r.Prompt
}

type scheduleForm struct {
	Host, Name, Prompt, Project, CWD, Provider, Model, Mode, Thinking, Expression, Timezone, MaxRuns, Isolation, Error string
	Archive, Multiplicity, IsGit                                                                                       bool
	Original                                                                                                           *scheduleRecord
}

func newScheduleForm(host string, record *scheduleRecord) *scheduleForm {
	f := &scheduleForm{Host: host, Expression: "0 * * * *", Timezone: "UTC", Isolation: "local", Archive: true}
	if record == nil {
		return f
	}
	r := *record
	f.Original = &r
	f.Name = r.Name
	f.Prompt = r.Prompt
	f.Expression = r.Cadence.Expression
	f.Timezone = r.Cadence.Timezone
	if f.Timezone == "" {
		f.Timezone = "UTC"
	}
	if r.MaxRuns != nil {
		f.MaxRuns = strconv.Itoa(*r.MaxRuns)
	}
	cfg := r.Target.Config
	f.CWD = cfg.CWD
	f.Project = cfg.CWD
	f.Provider = cfg.Provider
	f.Model = cfg.Model
	f.Mode = cfg.Mode
	f.Thinking = cfg.Thinking
	if cfg.Archive != nil {
		f.Archive = *cfg.Archive
	}
	if cfg.Isolation != "" {
		f.Isolation = cfg.Isolation
	}
	return f
}
func validateScheduleCron(expression string) error {
	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return errors.New("Enter a five-field cron expression.")
	}
	bounds := [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
	for i, field := range fields {
		for _, part := range strings.Split(field, ",") {
			step := strings.Split(part, "/")
			if len(step) > 2 {
				return errors.New("Invalid cron step.")
			}
			if len(step) == 2 {
				v, e := strconv.Atoi(step[1])
				if e != nil || v < 1 || strconv.Itoa(v) != step[1] {
					return errors.New("Invalid cron step.")
				}
			}
			base := step[0]
			if base == "*" {
				continue
			}
			pair := strings.Split(base, "-")
			if len(pair) > 2 || len(step) == 2 && len(pair) != 2 {
				return errors.New("Use a wildcard or range before a cron step.")
			}
			prev := -1
			for _, item := range pair {
				for _, r := range item {
					if r < '0' || r > '9' {
						return errors.New("Invalid cron value.")
					}
				}
				v, e := strconv.Atoi(item)
				if e != nil || v < bounds[i][0] || v > bounds[i][1] || v < prev {
					return errors.New("Cron value is out of range.")
				}
				prev = v
			}
		}
	}
	return nil
}
func nullableScheduleString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func (f *scheduleForm) payload() (string, map[string]any, error) {
	edit := f.Original != nil
	kind := "schedule/create"
	if edit {
		kind = "schedule/update"
	}
	p := map[string]any{}
	if edit {
		p["scheduleId"] = f.Original.ID
	}
	cadenceChanged := !edit || f.Expression != f.Original.Cadence.Expression || (f.Original.Cadence.Type == "cron" && f.Timezone != f.Original.Cadence.Timezone && !(f.Timezone == "UTC" && f.Original.Cadence.Timezone == ""))
	if cadenceChanged {
		if err := validateScheduleCron(f.Expression); err != nil {
			return "", nil, err
		}
		if _, err := time.LoadLocation(strings.TrimSpace(f.Timezone)); err != nil {
			return "", nil, errors.New("Choose a valid timezone.")
		}
		p["cadence"] = scheduleCadence{Type: "cron", Expression: strings.TrimSpace(f.Expression), Timezone: strings.TrimSpace(f.Timezone)}
	}
	if edit && f.Original.Target.Type == "agent" {
		if !cadenceChanged {
			return "", nil, errors.New("Change the cadence before saving.")
		}
		return kind, p, nil
	}
	if strings.TrimSpace(f.Prompt) == "" || f.Host == "" || f.CWD == "" || f.Provider == "" {
		return "", nil, errors.New("Choose a host, project and model, and enter a prompt.")
	}
	if !edit && f.Project == "" {
		return "", nil, errors.New("Choose a project.")
	}
	var max any
	if value := strings.TrimSpace(f.MaxRuns); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 {
			return "", nil, errors.New("Max runs must be a positive whole number.")
		}
		max = n
	}
	p["prompt"] = strings.TrimSpace(f.Prompt)
	if edit {
		p["name"] = nullableScheduleString(strings.TrimSpace(f.Name))
		p["maxRuns"] = max
		cfg := map[string]any{"provider": f.Provider, "cwd": f.CWD, "model": nullableScheduleString(f.Model), "modeId": nullableScheduleString(f.Mode), "thinkingOptionId": nullableScheduleString(f.Thinking)}
		if f.Multiplicity {
			cfg["archiveOnFinish"] = f.Archive
			cfg["isolation"] = "local"
			if f.IsGit && f.Isolation == "worktree" {
				cfg["isolation"] = "worktree"
			}
		}
		p["newAgentConfig"] = cfg
	} else {
		if name := strings.TrimSpace(f.Name); name != "" {
			p["name"] = name
		}
		if max != nil {
			p["maxRuns"] = max
		}
		cfg := scheduleConfig{Provider: f.Provider, CWD: f.CWD, Model: f.Model, Mode: f.Mode, Thinking: f.Thinking, Title: strings.TrimSpace(f.Name)}
		if f.Multiplicity {
			archive := f.Archive
			cfg.Archive = &archive
			cfg.Isolation = "local"
			if f.IsGit && f.Isolation == "worktree" {
				cfg.Isolation = "worktree"
			}
		}
		p["target"] = scheduleTarget{Type: "new-agent", Config: cfg}
	}
	return kind, p, nil
}

func scheduleList(ctx context.Context, c caller) ([]scheduleRecord, error) {
	raw, err := c.Call(ctx, "schedule/list", json.RawMessage(`{}`))
	if err != nil {
		return nil, err
	}
	var result struct {
		Schedules *[]scheduleRecord `json:"schedules"`
		Error     string            `json:"error"`
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	if result.Error != "" {
		return nil, errors.New(result.Error)
	}
	if result.Schedules == nil {
		return nil, errors.New("Host does not support schedules. Update the daemon.")
	}
	seen := map[string]bool{}
	rows := make([]scheduleRecord, 0, len(*result.Schedules))
	for _, r := range *result.Schedules {
		if r.ID == "" {
			return nil, errors.New("Host returned a schedule without an ID.")
		}
		if !seen[r.ID] {
			rows = append(rows, r)
			seen[r.ID] = true
		}
	}
	return rows, nil
}
func scheduleMutation(ctx context.Context, c caller, kind string, payload map[string]any) (*scheduleRecord, error) {
	request, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	raw, err := c.Call(ctx, kind, request)
	if err != nil {
		return nil, err
	}
	var result struct {
		Schedule   *scheduleRecord `json:"schedule"`
		ScheduleID string          `json:"scheduleId"`
		Error      string          `json:"error"`
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("%w: unreadable schedule response", client.ErrOutcomeUnknown)
	}
	if result.Error != "" {
		return nil, errors.New(result.Error)
	}
	if kind == "schedule/delete" {
		if result.ScheduleID == payload["scheduleId"] {
			return nil, nil
		}
	} else if result.Schedule != nil && result.Schedule.ID != "" && (kind == "schedule/create" || result.Schedule.ID == payload["scheduleId"]) {
		return result.Schedule, nil
	}
	return nil, fmt.Errorf("%w: schedule acknowledgement is missing", client.ErrOutcomeUnknown)
}

type schedulePending struct {
	Kind    string
	Key     scheduleKey
	Payload map[string]any
	Before  map[string]bool
	LastRun string
}

func scheduleJSONEqual(a, b any) bool {
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	var av, bv any
	_ = json.Unmarshal(aa, &av)
	_ = json.Unmarshal(bb, &bv)
	return reflect.DeepEqual(av, bv)
}
func (p schedulePending) matches(r scheduleRecord) bool {
	if v, ok := p.Payload["prompt"]; ok && v != r.Prompt {
		return false
	}
	if v, ok := p.Payload["name"]; ok && nullableScheduleString(r.Name) != v {
		return false
	}
	if v, ok := p.Payload["cadence"]; ok && !scheduleJSONEqual(v, r.Cadence) {
		return false
	}
	if v, ok := p.Payload["target"]; ok {
		want := v.(scheduleTarget)
		if want.Type != r.Target.Type || want.AgentID != r.Target.AgentID {
			return false
		}
		raw, _ := json.Marshal(want.Config)
		actualRaw, _ := json.Marshal(r.Target.Config)
		var desired, actual map[string]any
		_ = json.Unmarshal(raw, &desired)
		_ = json.Unmarshal(actualRaw, &actual)
		for k, value := range desired {
			if !scheduleJSONEqual(value, actual[k]) {
				return false
			}
		}
	}
	if v, ok := p.Payload["maxRuns"]; ok {
		var max any
		if r.MaxRuns != nil {
			max = *r.MaxRuns
		}
		if !scheduleJSONEqual(v, max) {
			return false
		}
	}
	if v, ok := p.Payload["newAgentConfig"]; ok {
		raw, _ := json.Marshal(r.Target.Config)
		var cfg map[string]any
		_ = json.Unmarshal(raw, &cfg)
		for key, value := range v.(map[string]any) {
			actual := cfg[key]
			if actual == nil && (key == "model" || key == "modeId" || key == "thinkingOptionId") {
				actual = nil
			}
			if !scheduleJSONEqual(value, actual) {
				return false
			}
		}
	}
	return true
}
func (p schedulePending) reconciled(h scheduleHost) bool {
	if h.ID != p.Key.Host || h.Error != "" {
		return false
	}
	count := 0
	for _, r := range h.Rows {
		if p.Kind == "schedule/create" {
			if !p.Before[r.ID] && p.matches(r) {
				count++
			}
			continue
		}
		if r.ID != p.Key.ID {
			continue
		}
		switch p.Kind {
		case "schedule/delete":
			return false
		case "schedule/pause":
			return r.Status == "paused"
		case "schedule/resume":
			return r.Status == "active"
		case "schedule/update":
			return p.matches(r)
		case "schedule/run-once":
			return r.LastRunAt != "" && r.LastRunAt != p.LastRun
		}
	}
	if p.Kind == "schedule/create" {
		return count == 1
	}
	return p.Kind == "schedule/delete"
}

// Reuse directory pagination while retaining archived heartbeat targets.
type scheduleAgentCaller struct{ caller }

func (c scheduleAgentCaller) Call(ctx context.Context, kind string, raw json.RawMessage) (json.RawMessage, error) {
	var request map[string]any
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	request["filter"] = map[string]any{"includeArchived": true}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	return c.caller.Call(ctx, kind, data)
}

func (f *scheduleForm) selectionError(host scheduleHost) error {
	if f.Original != nil && f.Original.Target.Type == "agent" {
		return nil
	}
	if host.Error != "" {
		return errors.New(host.Error)
	}
	for _, p := range host.Providers {
		if p.ID != f.Provider || p.Status != "ready" || p.Enabled != nil && !*p.Enabled {
			continue
		}
		if f.Model == "" {
			return nil
		}
		for _, m := range p.Models {
			if m.ID == f.Model && (m.Selectable == nil || *m.Selectable) {
				return nil
			}
		}
	}
	return errors.New("Choose an available model.")
}
