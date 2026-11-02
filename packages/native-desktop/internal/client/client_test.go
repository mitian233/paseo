package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func testServer(t *testing.T, serve func(*websocket.Conn)) string {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		serve(conn)
	}))
	t.Cleanup(s.Close)
	return "ws" + strings.TrimPrefix(s.URL, "http") + "/ws"
}
func receive(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	var v map[string]any
	if err := c.ReadJSON(&v); err != nil {
		t.Error(err)
		return nil
	}
	return v
}
func send(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	if err := c.WriteJSON(v); err != nil {
		t.Error(err)
	}
}
func handshake(t *testing.T, c *websocket.Conn) {
	hello := receive(t, c)
	if hello["type"] != "hello" || hello["protocolVersion"] != float64(1) {
		t.Errorf("hello: %v", hello)
	}
	send(t, c, map[string]any{"type": "session", "message": map[string]any{"type": "status", "payload": map[string]any{"status": "server_info", "serverId": "daemon", "features": map[string]any{"ownedSubscriptions": true, "selectiveAgentTimeline": true}}}})
}
func TestCallsCorrelateOutOfOrderAndReportRPCError(t *testing.T) {
	first := make(chan struct{})
	url := testServer(t, func(conn *websocket.Conn) {
		handshake(t, conn)
		a := receive(t, conn)["message"].(map[string]any)
		close(first)
		b := receive(t, conn)["message"].(map[string]any)
		send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "rpc_error", "payload": map[string]any{"requestId": b["requestId"], "requestType": b["type"], "error": "denied", "code": "FORBIDDEN"}}})
		send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "fetch_agents_response", "payload": map[string]any{"requestId": a["requestId"], "agents": []any{}}}})
		<-time.After(200 * time.Millisecond)
	})
	c := new(Client)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Connect(ctx, Target{URL: url, ClientID: "test"}); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	done := make(chan error, 1)
	go func() { _, err := c.Call(ctx, "fetch_agents_request", json.RawMessage(`{}`)); done <- err }()
	<-first
	_, err := c.Call(ctx, "send_agent_message_request", json.RawMessage(`{"agentId":"a","text":"hello"}`))
	var rpc *RPCError
	if !errors.As(err, &rpc) || rpc.Code != "FORBIDDEN" {
		t.Fatalf("rpc error: %v", err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}
func TestDisconnectLeavesSentRequestOutcomeUnknown(t *testing.T) {
	url := testServer(t, func(conn *websocket.Conn) { handshake(t, conn); receive(t, conn) })
	c := new(Client)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Connect(ctx, Target{URL: url, ClientID: "test"}); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err := c.Call(ctx, "send_agent_message_request", json.RawMessage(`{"agentId":"a","text":"hello"}`))
	if !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("expected unknown outcome: %v", err)
	}
}
func TestObserveRoutesAndReleasesTimeline(t *testing.T) {
	released := make(chan string, 1)
	url := testServer(t, func(conn *websocket.Conn) {
		handshake(t, conn)
		m := receive(t, conn)["message"].(map[string]any)
		send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "agent.timeline.set_subscription.response", "payload": map[string]any{"requestId": m["requestId"], "subscriptionId": "sub", "agentIds": []string{"a"}}}})
		send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "agent_stream", "payload": map[string]any{"subscriptionId": "sub", "agentId": "a", "seq": 1}}})
		m = receive(t, conn)["message"].(map[string]any)
		released <- m["type"].(string)
		send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "subscription.release.response", "payload": map[string]any{"requestId": m["requestId"], "subscriptionId": "sub"}}})
	})
	c := new(Client)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Connect(ctx, Target{URL: url, ClientID: "test"}); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sub, err := c.Observe(ctx, Observation{AgentIDs: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case update := <-sub.Updates:
		if !strings.Contains(string(update), `"agent_stream"`) {
			t.Fatal(string(update))
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := sub.Close(); err != nil {
		t.Fatal(err)
	}
	if got := <-released; got != "subscription.release.request" {
		t.Fatal(got)
	}
}

func TestPermissionDecisionKeepsServerIDAndRejectsDuplicateInFlight(t *testing.T) {
	received := make(chan struct{})
	reply := make(chan struct{}, 1)
	defer func() {
		select {
		case reply <- struct{}{}:
		default:
		}
	}()
	url := testServer(t, func(conn *websocket.Conn) {
		handshake(t, conn)
		message := receive(t, conn)["message"].(map[string]any)
		close(received)
		<-reply
		if message["requestId"] != "permission-1" {
			return
		}
		send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "agent_permission_resolved", "payload": map[string]any{"agentId": "a", "requestId": "permission-1"}}})
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c := new(Client)
	if err := c.Connect(ctx, Target{URL: url}); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	payload := json.RawMessage(`{"agentId":"a","requestId":"permission-1","response":{"decision":"allow"}}`)
	first := make(chan error, 1)
	go func() { _, err := c.Call(ctx, "agent_permission_response", payload); first <- err }()
	select {
	case <-received:
	case err := <-first:
		t.Fatalf("permission decision rejected before send: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := c.Call(ctx, "agent_permission_response", payload); err == nil {
		t.Fatal("duplicate permission decision overwrote the original waiter")
	}
	reply <- struct{}{}
	if err := <-first; err != nil {
		t.Fatalf("original permission acknowledgement lost: %v", err)
	}
}
