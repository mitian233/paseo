package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/client"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/timeline"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type directoryCaller func(context.Context, string, json.RawMessage) (json.RawMessage, error)

func (f directoryCaller) Call(c context.Context, typ string, p json.RawMessage) (json.RawMessage, error) {
	return f(c, typ, p)
}

func TestTerminalRestoreCapabilityBlocksOlderDaemonAttach(t *testing.T) {
	if !terminalRestoreAvailable(json.RawMessage(`{"features":{"terminalRestoreModes":true}}`)) {
		t.Fatal("restore-capable host rejected")
	}
	if terminalRestoreAvailable(json.RawMessage(`{"features":{"terminalRestoreModes":false}}`)) {
		t.Fatal("older host accepted terminal restore")
	}
}

func TestDirectoryLoadsAllPages(t *testing.T) {
	call := directoryCaller(func(_ context.Context, typ string, p json.RawMessage) (json.RawMessage, error) {
		var req struct {
			Page struct {
				Cursor string `json:"cursor"`
			} `json:"page"`
		}
		if err := json.Unmarshal(p, &req); err != nil {
			t.Fatal(err)
		}
		if typ == "fetch_workspaces_request" {
			return json.RawMessage(`{"entries":[{"id":"w","name":"repo","projectDisplayName":"P"}],"pageInfo":{"hasMore":false,"nextCursor":null}}`), nil
		}
		if typ != "fetch_agents_request" {
			t.Fatalf("unexpected request %s", typ)
		}
		if req.Page.Cursor == "" {
			return json.RawMessage(`{"entries":[{"agent":{"id":"a","title":"One","workspaceId":"w"}}],"pageInfo":{"hasMore":true,"nextCursor":"next"}}`), nil
		}
		if req.Page.Cursor != "next" {
			t.Fatalf("bad cursor %q", req.Page.Cursor)
		}
		return json.RawMessage(`{"entries":[{"agent":{"id":"b","title":"Two","workspaceId":"w"}}],"pageInfo":{"hasMore":false,"nextCursor":null}}`), nil
	})
	w, a, err := loadDirectory(context.Background(), call)
	if err != nil || len(w) != 1 || len(a) != 2 || a[1].ID != "b" || w[0].Name != "repo" {
		t.Fatalf("directory: %#v %#v %v", w, a, err)
	}
}

func TestDirectoryRetainsEntriesWhenActivityChangesBetweenPages(t *testing.T) {
	for _, typ := range []string{"fetch_workspaces_request", "fetch_agents_request"} {
		t.Run(typ, func(t *testing.T) {
			call := directoryCaller(func(_ context.Context, _ string, raw json.RawMessage) (json.RawMessage, error) {
				var req struct {
					Sort []struct {
						Key       string `json:"key"`
						Direction string `json:"direction"`
					} `json:"sort"`
					Page struct {
						Cursor string `json:"cursor"`
					} `json:"page"`
				}
				if err := json.Unmarshal(raw, &req); err != nil {
					t.Fatal(err)
				}
				stableKey := "created_at"
				if typ == "fetch_workspaces_request" {
					stableKey = "project_id"
				}
				stable := len(req.Sort) == 1 && req.Sort[0].Key == stableKey && req.Sort[0].Direction == "asc"
				if req.Page.Cursor == "" {
					return json.RawMessage(`{"entries":[{"id":"first"}],"pageInfo":{"hasMore":true,"nextCursor":"next"}}`), nil
				}
				// An activity update moves the second entry ahead of an activity cursor,
				// but leaves immutable creation/project ordering unchanged.
				if !stable {
					return json.RawMessage(`{"entries":[],"pageInfo":{"hasMore":false}}`), nil
				}
				return json.RawMessage(`{"entries":[{"id":"second"}],"pageInfo":{"hasMore":false}}`), nil
			})
			entries, err := loadPages(context.Background(), call, typ)
			if err != nil || len(entries) != 2 {
				t.Fatalf("activity update lost an entry: %s %v", entries, err)
			}
		})
	}
}

func TestPromptUnknownRetryKeepsMessageID(t *testing.T) {
	var sender promptSender
	var ids []string
	call := directoryCaller(func(_ context.Context, _ string, raw json.RawMessage) (json.RawMessage, error) {
		var req struct {
			MessageID string `json:"messageId"`
		}
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, req.MessageID)
		if len(ids) == 1 {
			return nil, client.ErrOutcomeUnknown
		}
		return json.RawMessage(`{"accepted":true}`), nil
	})
	if err := sender.sendPrompt(context.Background(), call, "a", "hello"); !errors.Is(err, client.ErrOutcomeUnknown) {
		t.Fatal(err)
	}
	if err := sender.sendPrompt(context.Background(), call, "a", "hello"); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] == "" || ids[0] != ids[1] {
		t.Fatalf("retry changed logical message ID: %v", ids)
	}
}

// A disconnected observation must prevent a new send before any RPC starts.
func TestSendBlockedWhenSelectionObservationEnded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := &App{connected: true, observerReady: true, selected: "a", draft: "hello", selectionCtx: ctx, client: new(client.Client)}
	a.send()
	if a.sending {
		t.Fatal("send started after the observation ended")
	}
}

func TestHistoryFailureEndsSelectionObservation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var envelope map[string]any
		if conn.ReadJSON(&envelope) != nil {
			return
		}
		_ = conn.WriteJSON(map[string]any{"type": "session", "message": map[string]any{"type": "status", "payload": map[string]any{"status": "server_info", "serverId": "daemon", "features": map[string]any{"ownedSubscriptions": true, "selectiveAgentTimeline": true}}}})
		if conn.ReadJSON(&envelope) != nil {
			return
		}
		request := envelope["message"].(map[string]any)
		_ = conn.WriteJSON(map[string]any{"type": "session", "message": map[string]any{"type": "agent.timeline.set_subscription.response", "payload": map[string]any{"requestId": request["requestId"], "subscriptionId": "sub", "agentIds": []string{"a"}}}})
		if conn.ReadJSON(&envelope) != nil {
			return
		}
		request = envelope["message"].(map[string]any)
		_ = conn.WriteJSON(map[string]any{"type": "session", "message": map[string]any{"type": "rpc_error", "payload": map[string]any{"requestId": request["requestId"], "requestType": request["type"], "error": "history unavailable", "code": "FAILED"}}})
		if conn.ReadJSON(&envelope) != nil {
			return
		}
		request = envelope["message"].(map[string]any)
		_ = conn.WriteJSON(map[string]any{"type": "session", "message": map[string]any{"type": "subscription.release.response", "payload": map[string]any{"requestId": request["requestId"], "subscriptionId": "sub"}}})
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := new(client.Client)
	if err := c.Connect(ctx, client.Target{URL: "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"}); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	selectionCtx, cancelSelection := context.WithCancel(ctx)
	defer cancelSelection()
	a := &App{connected: true, selected: "a", selectionCtx: selectionCtx, cancelSelection: cancelSelection, client: c}
	a.observe(selectionCtx, cancelSelection, c, 0, 0, "a")
	if selectionCtx.Err() == nil {
		t.Fatal("history failure left the selection active after its observer exited")
	}
}
func TestSendRejectsUnacceptedResponse(t *testing.T) {
	call := directoryCaller(func(_ context.Context, typ string, p json.RawMessage) (json.RawMessage, error) {
		var req struct {
			AgentID string `json:"agentId"`
			Text    string `json:"text"`
		}
		_ = json.Unmarshal(p, &req)
		if typ != "send_agent_message_request" || req.AgentID != "a" || req.Text != "hello" {
			return nil, errors.New("wrong request")
		}
		return json.RawMessage(`{"agentId":"a","accepted":false,"error":"denied"}`), nil
	})
	if err := sendPrompt(context.Background(), call, "a", "hello", "message-1"); err == nil || err.Error() != "denied" {
		t.Fatalf("rejection: %v", err)
	}
}
func TestDirectoryRejectsBrokenPagination(t *testing.T) {
	call := directoryCaller(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"entries":[],"pageInfo":{"hasMore":true,"nextCursor":null}}`), nil
	})
	if _, _, err := loadDirectory(context.Background(), call); err == nil {
		t.Fatal("missing cursor accepted")
	}
}

// This opt-in smoke sends PASEO_SMOKE_PROMPT to PASEO_SMOKE_AGENT.
func TestExistingDaemonPromptPersistence(t *testing.T) {
	url, agent, text := os.Getenv("PASEO_SMOKE_URL"), os.Getenv("PASEO_SMOKE_AGENT"), os.Getenv("PASEO_SMOKE_PROMPT")
	if url == "" || agent == "" || text == "" {
		t.Skip("explicit daemon, agent and prompt are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := new(client.Client)
	if err := c.Connect(ctx, client.Target{URL: url}); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sub, err := c.Observe(ctx, client.Observation{AgentIDs: []string{agent}})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	if err := (&promptSender{}).sendPrompt(ctx, c, agent, text); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case update, ok := <-sub.Updates:
			if !ok {
				t.Fatal("observation closed before turn completed")
			}
			var event struct {
				Payload struct {
					Event struct {
						Type string `json:"type"`
					} `json:"event"`
				} `json:"payload"`
			}
			if err := json.Unmarshal(update, &event); err != nil {
				t.Fatal(err)
			}
			if event.Payload.Event.Type != "turn_completed" {
				continue
			}
			request, _ := json.Marshal(map[string]any{"agentId": agent, "direction": "tail", "limit": 0, "projection": "projected"})
			history, err := c.Call(ctx, "fetch_agent_timeline_request", request)
			if err != nil {
				t.Fatal(err)
			}
			var model timeline.Model
			if err := model.Replace(history); err != nil {
				t.Fatal(err)
			}
			for _, item := range model.Items() {
				if item.Kind == "user_message" && item.Text == text {
					return
				}
			}
			t.Fatalf("accepted prompt absent from daemon history: %+v", model.Items())
		}
	}
}
