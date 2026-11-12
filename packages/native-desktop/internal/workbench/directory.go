package workbench

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/getpaseo/paseo/packages/native-desktop/internal/client"
)

type caller interface {
	Call(context.Context, string, json.RawMessage) (json.RawMessage, error)
}
type Project struct {
	ID   string `json:"projectId"`
	Name string `json:"projectDisplayName"`
	Root string `json:"projectRootPath"`
	Kind string `json:"projectKind"`
}
type Workspace struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Project   string `json:"projectDisplayName"`
	ProjectID string `json:"projectId"`
	Root      string `json:"projectRootPath"`
	Directory string `json:"workspaceDirectory"`
	Git       struct {
		CurrentBranch string `json:"currentBranch"`
	} `json:"gitRuntime"`
	Kind     string  `json:"projectKind"`
	PinnedAt *string `json:"pinnedAt"`
}
type Agent struct {
	ID                     string `json:"id"`
	Title                  string `json:"title"`
	WorkspaceID            string `json:"workspaceId"`
	CWD                    string `json:"cwd"`
	Provider               string `json:"provider"`
	Status                 string `json:"status"`
	RequiresAttention      bool   `json:"requiresAttention"`
	AttentionReason        string `json:"attentionReason"`
	PendingPermissionCount int    `json:"pendingPermissionCount"`
}
type directoryPage struct {
	Entries  []json.RawMessage `json:"entries"`
	PageInfo struct {
		HasMore    bool    `json:"hasMore"`
		NextCursor *string `json:"nextCursor"`
	} `json:"pageInfo"`
}

func loadPages(ctx context.Context, c caller, typ string) ([]json.RawMessage, error) {
	var entries []json.RawMessage
	cursor := ""
	seen := map[string]bool{}
	for {
		page := map[string]any{"limit": 200}
		if cursor != "" {
			page["cursor"] = cursor
		}
		key := "created_at"
		if typ == "fetch_workspaces_request" {
			key = "project_id"
		}
		req, _ := json.Marshal(map[string]any{"page": page, "sort": []map[string]string{{"key": key, "direction": "asc"}}})
		raw, err := c.Call(ctx, typ, req)
		if err != nil {
			return nil, err
		}
		var p directoryPage
		if err = json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("%s: %w", typ, err)
		}
		entries = append(entries, p.Entries...)
		if !p.PageInfo.HasMore {
			return entries, nil
		}
		if p.PageInfo.NextCursor == nil || *p.PageInfo.NextCursor == "" || seen[*p.PageInfo.NextCursor] {
			return nil, errors.New("directory pagination returned an invalid cursor")
		}
		cursor = *p.PageInfo.NextCursor
		seen[cursor] = true
	}
}
func loadDirectory(ctx context.Context, c caller) ([]Workspace, []Agent, error) {
	ws, err := loadPages(ctx, c, "fetch_workspaces_request")
	if err != nil {
		return nil, nil, fmt.Errorf("fetch workspaces: %w", err)
	}
	as, err := loadPages(ctx, c, "fetch_agents_request")
	if err != nil {
		return nil, nil, fmt.Errorf("fetch agents: %w", err)
	}
	workspaces := make([]Workspace, 0, len(ws))
	agents := make([]Agent, 0, len(as))
	for _, v := range ws {
		var w Workspace
		if err := json.Unmarshal(v, &w); err != nil {
			return nil, nil, err
		}
		workspaces = append(workspaces, w)
	}
	for _, v := range as {
		var a struct {
			Agent Agent `json:"agent"`
		}
		if err := json.Unmarshal(v, &a); err != nil {
			return nil, nil, err
		}
		if a.Agent.ID != "" {
			agents = append(agents, a.Agent)
		}
	}
	return workspaces, agents, nil
}

type promptKey struct{ agent, text string }
type promptAttempt struct {
	id      string
	unknown bool
}
type promptSender struct {
	mu       sync.Mutex
	attempts map[promptKey]promptAttempt
}

func (s *promptSender) sendPrompt(ctx context.Context, c caller, agentID, text string) error {
	key := promptKey{agentID, text}
	s.mu.Lock()
	if s.attempts == nil {
		s.attempts = make(map[promptKey]promptAttempt)
	}
	attempt := s.attempts[key]
	if attempt.id == "" {
		attempt.id = rand.Text()
		s.attempts[key] = attempt
	}
	s.mu.Unlock()
	err := sendPrompt(ctx, c, agentID, text, attempt.id)
	s.mu.Lock()
	current := s.attempts[key]
	if current.id == attempt.id {
		if errors.Is(err, client.ErrOutcomeUnknown) {
			current.unknown = true
			s.attempts[key] = current
		} else if err == nil || !current.unknown {
			delete(s.attempts, key)
		}
	}
	s.mu.Unlock()
	return err
}

func sendPrompt(ctx context.Context, c caller, agentID, text, messageID string) error {
	req, _ := json.Marshal(map[string]string{"agentId": agentID, "text": text, "messageId": messageID})
	raw, err := c.Call(ctx, "send_agent_message_request", req)
	if err != nil {
		return err
	}
	var result struct {
		Accepted bool    `json:"accepted"`
		Error    *string `json:"error"`
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if !result.Accepted {
		if result.Error != nil && *result.Error != "" {
			return errors.New(*result.Error)
		}
		return errors.New("daemon did not accept the message")
	}
	return nil
}
