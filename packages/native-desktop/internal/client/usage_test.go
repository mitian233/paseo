package client

import (
	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	"strings"
	"testing"
	"time"
)

func TestUsageStreamCorrelatesUpdatesAndPreservesPartialErrors(t *testing.T) {
	done := make(chan struct{})
	defer close(done)
	url := testServer(t, func(conn *websocket.Conn) {
		handshake(t, conn)
		m := receive(t, conn)["message"].(map[string]any)
		if m["type"] != "usage.list_reports.request" || m["forceRefresh"] != true {
			t.Errorf("request = %v", m)
		}
		for _, p := range []map[string]any{
			{"requestId": "other", "report": map[string]any{"id": "wrong"}},
			{"requestId": m["requestId"], "report": map[string]any{"id": "claude:work"}},
		} {
			send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "usage.list_reports.update", "payload": p}})
		}
		send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "usage.list_reports.response", "payload": map[string]any{"requestId": m["requestId"], "error": "one source failed"}}})
		<-done
	})
	c := new(Client)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Connect(ctx, Target{URL: url, ClientID: "usage-test"}); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var seen []json.RawMessage
	reports, err := c.ListUsageReports(ctx, true, []string{"claude:work"}, func(r json.RawMessage) { seen = append(seen, r) })
	if err == nil || !strings.Contains(err.Error(), "one source failed") {
		t.Fatalf("error = %v", err)
	}
	if len(reports) != 1 || len(seen) != 1 || !strings.Contains(string(reports[0]), "claude:work") {
		t.Fatalf("reports = %s, streamed = %s", reports, seen)
	}
}

func TestUsageCancellationDropsLateUpdates(t *testing.T) {
	c := &Client{pending: map[string]*pendingCall{}}
	stream := newUsageStream(nil)
	c.pending["active"] = &pendingCall{ch: make(chan result, 1), usage: stream}
	frame := func(kind, id string) {
		c.dispatch(nil, []byte(`{"type":"session","message":{"type":"`+kind+`","payload":{"requestId":"`+id+`","report":{"id":"account"},"error":null}}}`))
	}
	frame("usage.list_reports.update", "active")
	frame("usage.list_reports.response", "active")
	frame("usage.list_reports.update", "active")
	if len(stream.reports) != 1 {
		t.Fatalf("completion retained late update: %s", stream.reports)
	}
	canceled := newUsageStream(nil)
	c.pending["canceled"] = &pendingCall{ch: make(chan result, 1), usage: canceled}
	delete(c.pending, "canceled")
	frame("usage.list_reports.update", "canceled")
	if len(canceled.reports) != 0 {
		t.Fatal("released request retained a late update")
	}
}

func TestUsageCanceledRequestReleasesItsOwner(t *testing.T) {
	requested := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	url := testServer(t, func(conn *websocket.Conn) { handshake(t, conn); receive(t, conn); close(requested); <-release })
	c := new(Client)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Connect(ctx, Target{URL: url, ClientID: "cancel-usage"}); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	requestCtx, cancelRequest := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() { _, err := c.ListUsageReports(requestCtx, false, nil, nil); result <- err }()
	select {
	case <-requested:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancelRequest()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("canceled request succeeded")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	c.mu.Lock()
	remaining := len(c.pending)
	c.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("canceled request still owns %d pending calls", remaining)
	}
}
