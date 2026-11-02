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

func TestCloseCancelsPendingHandshake(t *testing.T) {
	ready := make(chan struct{})
	url := testServer(t, func(conn *websocket.Conn) { receive(t, conn); close(ready); _, _, _ = conn.ReadMessage() })
	c := new(Client)
	done := make(chan error, 1)
	go func() { done <- c.Connect(context.Background(), Target{URL: url, ClientID: "test"}) }()
	<-ready
	_ = c.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("handshake succeeded after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel handshake")
	}
}

func TestHelloCapabilitiesAndBearerCompatibility(t *testing.T) {
	for _, password := range []string{"valid-token_1", "中文", "bad\fform", "with space", "a/b"} {
		t.Run(password, func(t *testing.T) {
			headers := make(chan string, 1)
			hellos := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				headers <- r.Header.Get("Authorization")
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				hello := receive(t, conn)
				hellos <- hello
				send(t, conn, map[string]any{"type": "hello.rejected", "reason": "test"})
			}))
			defer server.Close()
			c := new(Client)
			events := c.Events()
			err := c.Connect(context.Background(), Target{URL: "ws" + strings.TrimPrefix(server.URL, "http"), ClientID: "test", Password: password})
			if err == nil {
				t.Fatal("expected rejection")
			}
			header := <-headers
			if password == "valid-token_1" {
				if header != "Bearer "+password {
					t.Fatalf("header %q", header)
				}
			} else if header != "" {
				t.Fatalf("unexpected header %q", header)
			}
			hello := <-hellos
			caps := hello["capabilities"].(map[string]any)
			if caps["all_providers"] != true || caps["timeline_notifications"] != true {
				t.Fatalf("capabilities %v", caps)
			}
			if hello["auth"].(map[string]any)["password"] != password {
				t.Fatal("hello password changed")
			}
			if ev := <-events; ev.State != "connecting" {
				t.Fatal(ev)
			}
			if ev := <-events; ev.State != "disconnected" || ev.Err == nil {
				t.Fatal(ev)
			}
		})
	}
}

func TestObserveUnknownOutcomeDisconnectsOwner(t *testing.T) {
	received := make(chan struct{})
	closed := make(chan struct{})
	url := testServer(t, func(conn *websocket.Conn) {
		handshake(t, conn)
		receive(t, conn)
		close(received)
		_, _, _ = conn.ReadMessage()
		close(closed)
	})
	c := new(Client)
	if err := c.Connect(context.Background(), Target{URL: url, ClientID: "test"}); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.Observe(ctx, Observation{AgentIDs: []string{"a"}}); done <- err }()
	<-received
	cancel()
	if err := <-done; !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("remote owner remained connected")
	}
}

func TestOwnedBootstrapFiltersOtherOwners(t *testing.T) {
	ready := make(chan struct{})
	release := make(chan struct{})
	url := testServer(t, func(conn *websocket.Conn) {
		handshake(t, conn)
		m := receive(t, conn)["message"].(map[string]any)
		send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "agent.timeline.set_subscription.response", "payload": map[string]any{"requestId": m["requestId"], "subscriptionId": "one"}}})
		m = receive(t, conn)["message"].(map[string]any)
		send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "agent_stream", "payload": map[string]any{"subscriptionId": "one", "agentId": "a", "seq": 1}}})
		send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "agent.timeline.set_subscription.response", "payload": map[string]any{"requestId": m["requestId"], "subscriptionId": "two"}}})
		send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "agent_stream", "payload": map[string]any{"subscriptionId": "two", "agentId": "a", "seq": 2}}})
		close(ready)
		<-release
	})
	c := new(Client)
	if err := c.Connect(context.Background(), Target{URL: url, ClientID: "test"}); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	defer close(release)
	_, err := c.Observe(context.Background(), Observation{AgentIDs: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := c.Observe(context.Background(), Observation{AgentIDs: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	<-ready
	select {
	case raw := <-sub.Updates:
		var msg struct {
			Payload struct {
				Seq int `json:"seq"`
			} `json:"payload"`
		}
		_ = json.Unmarshal(raw, &msg)
		if msg.Payload.Seq != 2 {
			t.Fatalf("received other owner's update: %s", raw)
		}
	case <-time.After(time.Second):
		t.Fatal("missing update")
	}
}

func TestCallCancelsWhileWriterBusy(t *testing.T) {
	hold := make(chan struct{})
	url := testServer(t, func(conn *websocket.Conn) { handshake(t, conn); <-hold })
	c := new(Client)
	if err := c.Connect(context.Background(), Target{URL: url, ClientID: "test"}); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	defer close(hold)
	c.mu.Lock()
	c.writer = make(chan struct{}, 1)
	c.writer <- struct{}{}
	c.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.Call(ctx, "fetch_agents_request", nil); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiting writer ignored cancellation")
	}
	<-c.writer
}

func TestActiveWriteCancellationClosesStalledSocket(t *testing.T) {
	hold := make(chan struct{})
	url := testServer(t, func(conn *websocket.Conn) { handshake(t, conn); <-hold })
	c := new(Client)
	if err := c.Connect(context.Background(), Target{URL: url, ClientID: "test"}); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	defer close(hold)
	c.mu.Lock()
	c.writer = make(chan struct{}, 1)
	writer := c.writer
	c.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	payload, _ := json.Marshal(map[string]string{"data": strings.Repeat("x", 32<<20)})
	done := make(chan error, 1)
	go func() { _, err := c.Call(ctx, "send_agent_message_request", payload); done <- err }()
	deadline := time.After(5 * time.Second)
	for len(writer) == 0 {
		select {
		case err := <-done:
			t.Fatalf("write ended before cancellation: %v", err)
		case <-deadline:
			t.Fatal("write never started")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, ErrOutcomeUnknown) && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("active write ignored cancellation")
	}
}

func TestOldConnectionFrameCannotReachNewSubscription(t *testing.T) {
	oldConn, newConn := new(websocket.Conn), new(websocket.Conn)
	updates := make(chan json.RawMessage, 1)
	c := &Client{conn: newConn, owned: true}
	sub := &Subscription{client: c, updates: updates, Updates: updates, done: make(chan struct{}), agents: map[string]bool{"a": true}, id: "same"}
	c.subs = map[*Subscription]struct{}{sub: {}}
	frame := []byte(`{"type":"session","message":{"type":"agent_stream","payload":{"agentId":"a","subscriptionId":"same","seq":1}}}`)
	c.dispatch(oldConn, frame)
	select {
	case update := <-updates:
		t.Fatalf("old connection delivered %s", update)
	default:
	}
	c.dispatch(newConn, frame)
	select {
	case <-updates:
	default:
		t.Fatal("current connection did not deliver frame")
	}
}

func TestFailedHandshakeEmitsDisconnected(t *testing.T) {
	for _, scenario := range []string{"read failure", "unsupported version"} {
		t.Run(scenario, func(t *testing.T) {
			url := testServer(t, func(conn *websocket.Conn) {
				receive(t, conn)
				if scenario == "unsupported version" {
					send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "status", "payload": map[string]any{"status": "server_info", "serverId": "daemon", "protocolVersion": 99}}})
				}
			})
			c := new(Client)
			events := c.Events()
			if err := c.Connect(context.Background(), Target{URL: url, ClientID: "test"}); err == nil {
				t.Fatal("expected handshake failure")
			}
			first, second := <-events, <-events
			if first.State != "connecting" || second.State != "disconnected" || second.Epoch != first.Epoch || second.Err == nil {
				t.Fatalf("events: %+v %+v", first, second)
			}
		})
	}
}

func TestObserveWaiterCancellationAndLocalClose(t *testing.T) {
	blocked := make(chan struct{})
	release := make(chan struct{})
	url := testServer(t, func(conn *websocket.Conn) {
		handshake(t, conn)
		m := receive(t, conn)["message"].(map[string]any)
		send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "agent.timeline.set_subscription.response", "payload": map[string]any{"requestId": m["requestId"], "subscriptionId": "first"}}})
		receive(t, conn)
		close(blocked)
		<-release
	})
	c := new(Client)
	if err := c.Connect(context.Background(), Target{URL: url}); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	defer close(release)
	sub, err := c.Observe(context.Background(), Observation{AgentIDs: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Observe(ctx, Observation{AgentIDs: []string{"b"}})
	<-blocked
	waitCtx, waitCancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.Observe(waitCtx, Observation{AgentIDs: []string{"c"}}); done <- err }()
	waitCancel()
	go sub.Close()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiter: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("observe waiter ignored cancellation")
	}
	select {
	case <-sub.done:
	case <-time.After(time.Second):
		t.Error("local subscription remained open")
	}
}

func TestReleaseErrorDisconnectsOwner(t *testing.T) {
	closed := make(chan struct{})
	url := testServer(t, func(conn *websocket.Conn) {
		handshake(t, conn)
		m := receive(t, conn)["message"].(map[string]any)
		send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "agent.timeline.set_subscription.response", "payload": map[string]any{"requestId": m["requestId"], "subscriptionId": "first"}}})
		m = receive(t, conn)["message"].(map[string]any)
		send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "rpc_error", "payload": map[string]any{"requestId": m["requestId"], "requestType": m["type"], "code": "FAILED", "error": "release failed"}}})
		_, _, _ = conn.ReadMessage()
		close(closed)
	})
	c := new(Client)
	if err := c.Connect(context.Background(), Target{URL: url}); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sub, err := c.Observe(context.Background(), Observation{AgentIDs: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := sub.Close(); err == nil {
		t.Fatal("expected release error")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("failed release left owner connected")
	}
}

func TestConnectionEventsRetainLatestTransition(t *testing.T) {
	c := new(Client)
	events := c.Events()
	c.mu.Lock()
	for range 20 {
		c.epoch++
		c.eventLocked("online", nil)
		c.eventLocked("disconnected", nil)
	}
	c.mu.Unlock()
	var last ConnectionEvent
	for len(events) > 0 {
		last = <-events
	}
	if last.State != "disconnected" || last.Epoch != 20 {
		t.Fatalf("latest event: %+v", last)
	}
}

func TestQueuedCallDisconnectIsUnsent(t *testing.T) {
	hold := make(chan struct{})
	url := testServer(t, func(conn *websocket.Conn) { handshake(t, conn); <-hold })
	c := new(Client)
	if err := c.Connect(context.Background(), Target{URL: url}); err != nil {
		t.Fatal(err)
	}
	defer close(hold)
	c.mu.Lock()
	c.writer = make(chan struct{}, 1)
	c.writer <- struct{}{}
	writer := c.writer
	c.mu.Unlock()
	done := make(chan error, 1)
	go func() { _, err := c.Call(context.Background(), "fetch_agents_request", nil); done <- err }()
	deadline := time.After(time.Second)
	for {
		c.mu.Lock()
		n := len(c.pending)
		c.mu.Unlock()
		if n == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("call did not queue")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	_ = c.Close()
	<-writer
	select {
	case err := <-done:
		if !errors.Is(err, ErrDisconnected) || errors.Is(err, ErrOutcomeUnknown) {
			t.Fatalf("unsent call: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued call did not return")
	}
}
