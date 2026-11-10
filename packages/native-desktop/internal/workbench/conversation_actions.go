package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/getpaseo/paseo/packages/native-desktop/internal/client"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/timeline"
)

func requestRewind(ctx context.Context, c permissionCaller, agent, message, mode string) error {
	if agent == "" || message == "" || (mode != "conversation" && mode != "files" && mode != "both") {
		return errors.New("invalid rewind request")
	}
	raw, _ := json.Marshal(map[string]string{"agentId": agent, "messageId": message, "mode": mode})
	response, err := c.Call(ctx, "agent.rewind.request", raw)
	if err != nil {
		return err
	}
	var ack struct {
		AgentID string  `json:"agentId"`
		OK      bool    `json:"ok"`
		Error   *string `json:"error"`
	}
	if err := json.Unmarshal(response, &ack); err != nil || ack.AgentID != agent {
		return errors.New("invalid rewind acknowledgement")
	}
	if !ack.OK {
		if ack.Error != nil && *ack.Error != "" {
			return errors.New(*ack.Error)
		}
		return errors.New("rewind was rejected")
	}
	return nil
}

func requestFork(ctx context.Context, c permissionCaller, agent string, item timeline.Item) (json.RawMessage, error) {
	if agent == "" {
		return nil, errors.New("missing agent")
	}
	payload := map[string]any{"agentId": agent}
	if item.Epoch != "" && item.SeqEnd >= 0 {
		payload["boundaryCursor"] = map[string]any{"epoch": item.Epoch, "seq": item.SeqEnd}
	} else if item.MessageID != "" {
		payload["boundaryMessageId"] = item.MessageID
	} else {
		return nil, errors.New("missing fork boundary")
	}
	raw, _ := json.Marshal(payload)
	response, err := c.Call(ctx, "agent.fork_context.request", raw)
	if err != nil {
		return nil, err
	}
	var ack struct {
		AgentID    string          `json:"agentId"`
		Attachment json.RawMessage `json:"attachment"`
		Error      *string         `json:"error"`
	}
	if err := json.Unmarshal(response, &ack); err != nil || ack.AgentID != agent {
		return nil, errors.New("invalid fork acknowledgement")
	}
	if ack.Error != nil && *ack.Error != "" {
		return nil, errors.New(*ack.Error)
	}
	var attachment struct {
		Type     string `json:"type"`
		MimeType string `json:"mimeType"`
		Text     string `json:"text"`
	}
	if len(ack.Attachment) == 0 || string(ack.Attachment) == "null" || json.Unmarshal(ack.Attachment, &attachment) != nil || attachment.Type != "text" || attachment.MimeType != "text/plain" || strings.TrimSpace(attachment.Text) == "" {
		return nil, errors.New("fork context did not include a history attachment")
	}
	return append(json.RawMessage(nil), ack.Attachment...), nil
}

func (a *App) rewindFrom(item timeline.Item, mode string) {
	if !a.canMutate() || a.conversationActionBusy || item.MessageID == "" {
		return
	}
	gen, selection, agent, c, ctx := a.generation, a.selection, a.selected, a.client, a.selectionCtx
	a.conversationActionBusy = true
	go func() {
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		err := requestRewind(callCtx, c, agent, item.MessageID, mode)
		a.selectionUpdate(gen, selection, func() {
			a.conversationActionBusy = false
			if err != nil {
				if errors.Is(err, client.ErrOutcomeUnknown) {
					a.status = "Rewind outcome unknown; refresh the conversation before trying again."
				} else {
					a.status = fmt.Sprintf("Rewind failed: %v", err)
				}
				return
			}
			a.rewindCompleted(item, mode)
		})
	}()
}

func (a *App) forkFrom(item timeline.Item, target string) {
	if !a.canMutate() || a.conversationActionBusy || !a.forkAvailable || (target != "tab" && target != "workspace") {
		return
	}
	gen, selection, agent, c, ctx := a.generation, a.selection, a.selected, a.client, a.selectionCtx
	a.conversationActionBusy = true
	go func() {
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		attachment, err := requestFork(callCtx, c, agent, item)
		a.selectionUpdate(gen, selection, func() {
			a.conversationActionBusy = false
			if err != nil {
				a.status = fmt.Sprintf("Fork failed: %v", err)
				return
			}
			a.openForkDraft(attachment, target)
		})
	}()
}
