package workbench

import "encoding/json"

// applyAgentActivity accepts a selected agent snapshot or its owned timeline event.
func (a *App) applyAgentActivity(raw []byte) {
	var p struct {
		AgentID string `json:"agentId"`
		Agent   *struct {
			ID           string `json:"id"`
			Status       string `json:"status"`
			Capabilities struct {
				Conversation bool `json:"supportsRewindConversation"`
				Files        bool `json:"supportsRewindFiles"`
				Both         bool `json:"supportsRewindBoth"`
			} `json:"capabilities"`
		} `json:"agent"`
		Event struct {
			Type string `json:"type"`
		} `json:"event"`
	}
	var envelope struct {
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(raw, &envelope) == nil && len(envelope.Payload) > 0 {
		raw = envelope.Payload
	}
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	if p.AgentID != "" && p.AgentID != a.selected {
		return
	}
	if p.Agent != nil {
		if p.Agent.ID != "" && p.Agent.ID != a.selected {
			return
		}
		a.agentRunning = p.Agent.Status == "running"
		a.rewindCapabilities.Conversation = p.Agent.Capabilities.Conversation
		a.rewindCapabilities.Files = p.Agent.Capabilities.Files
		a.rewindCapabilities.Both = p.Agent.Capabilities.Both
	}
	switch p.Event.Type {
	case "turn_started":
		a.agentRunning = true
	case "turn_completed", "turn_failed", "turn_canceled", "turn_cancelled":
		a.agentRunning = false
	}
}
