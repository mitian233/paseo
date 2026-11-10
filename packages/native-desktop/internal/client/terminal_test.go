package client

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestTerminalReceivesImmediateBinaryOutputAndReleases(t *testing.T) {
	released := make(chan string, 1)
	url := testServer(t, func(conn *websocket.Conn) {
		handshake(t, conn)
		m := receive(t, conn)["message"].(map[string]any)
		send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "subscribe_terminal_response", "payload": map[string]any{"requestId": m["requestId"], "terminalId": "term", "subscriptionId": "sub", "slot": 3, "error": nil}}})
		_ = conn.WriteMessage(websocket.BinaryMessage, []byte{1, 3, 'o', 'k'})
		m = receive(t, conn)["message"].(map[string]any)
		released <- m["type"].(string)
		send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "subscription.release.response", "payload": map[string]any{"requestId": m["requestId"]}}})
	})
	c := new(Client)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Connect(ctx, Target{URL: url, ClientID: "test"}); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	stream, err := c.SubscribeTerminal(ctx, "term")
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8)
	n, err := stream.Read(buf)
	if err != nil || string(buf[:n]) != "ok" {
		t.Fatalf("output %q: %v", buf[:n], err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if got := <-released; got != "subscription.release.request" {
		t.Fatal(got)
	}
}

func TestTerminalInputAndResizeUseBinaryFrames(t *testing.T) {
	frames := make(chan []byte, 2)
	url := testServer(t, func(conn *websocket.Conn) {
		handshake(t, conn)
		m := receive(t, conn)["message"].(map[string]any)
		send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "subscribe_terminal_response", "payload": map[string]any{"requestId": m["requestId"], "terminalId": "term", "subscriptionId": "sub", "slot": 7, "error": nil}}})
		for range 2 {
			kind, b, err := conn.ReadMessage()
			if err != nil || kind != websocket.BinaryMessage {
				return
			}
			frames <- b
		}
		m = receive(t, conn)["message"].(map[string]any)
		send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "subscription.release.response", "payload": map[string]any{"requestId": m["requestId"]}}})
	})
	c := new(Client)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Connect(ctx, Target{URL: url, ClientID: "test"}); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	stream, err := c.SubscribeTerminal(ctx, "term")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if err := stream.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	a, b := <-frames, <-frames
	if string(a) != "\x02\x07abc" {
		t.Fatalf("input %q", a)
	}
	if len(b) < 2 || b[0] != 3 || b[1] != 7 {
		t.Fatalf("resize %q", b)
	}
	var size struct {
		Cols   int    `json:"cols"`
		Rows   int    `json:"rows"`
		Intent string `json:"intent"`
	}
	if json.Unmarshal(b[2:], &size) != nil || size.Cols != 100 || size.Rows != 30 || size.Intent != "claim" {
		t.Fatalf("resize %s", b[2:])
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalReplayPrecedesLiveOutput(t *testing.T) {
	url := testServer(t, func(conn *websocket.Conn) {
		handshake(t, conn)
		m := receive(t, conn)["message"].(map[string]any)
		send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "subscribe_terminal_response", "payload": map[string]any{"requestId": m["requestId"], "terminalId": "term", "subscriptionId": "sub", "slot": 4, "error": nil}}})
		for _, frame := range [][]byte{append([]byte{6, 4}, []byte(`{"rows":25,"cols":90,"retainedHistoryLines":0}`)...), append([]byte{7, 4}, []byte("history")...), {8, 4}, append([]byte{1, 4}, []byte("live")...)} {
			_ = conn.WriteMessage(websocket.BinaryMessage, frame)
		}
		m = receive(t, conn)["message"].(map[string]any)
		send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "subscription.release.response", "payload": map[string]any{"requestId": m["requestId"]}}})
	})
	c := new(Client)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Connect(ctx, Target{URL: url, ClientID: "test"}); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, err := c.SubscribeTerminal(ctx, "term")
	if err != nil {
		t.Fatal(err)
	}
	cols, rows := 0, 0
	s.OnReplaySize(func(c, r int) { cols, rows = c, r })
	b := make([]byte, len("\x1bchistorylive"))
	if _, err = io.ReadFull(s, b); err != nil {
		t.Fatal(err)
	}
	if string(b) != "\x1bchistorylive" || cols != 90 || rows != 25 {
		t.Fatalf("replay %q size %dx%d", b, cols, rows)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalDoesNotPublishReplayUntilEnd(t *testing.T) {
	url := testServer(t, func(conn *websocket.Conn) {
		handshake(t, conn)
		m := receive(t, conn)["message"].(map[string]any)
		send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "subscribe_terminal_response", "payload": map[string]any{"requestId": m["requestId"], "terminalId": "term", "subscriptionId": "sub", "slot": 9, "error": nil}}})
		_ = conn.WriteMessage(websocket.BinaryMessage, append([]byte{6, 9}, []byte(`{"rows":25,"cols":90}`)...))
		_ = conn.WriteMessage(websocket.BinaryMessage, append([]byte{7, 9}, []byte("partial")...))
		_ = conn.WriteMessage(websocket.BinaryMessage, append([]byte{1, 9}, []byte("live-before-end")...))
		time.Sleep(30 * time.Millisecond)
	})
	c := new(Client)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Connect(ctx, Target{URL: url, ClientID: "test"}); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, err := c.SubscribeTerminal(ctx, "term")
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 64)
	_, err = s.Read(b)
	if err == nil {
		t.Fatalf("partial replay escaped before end: %q", b)
	}
}

func TestTerminalReplayPublishesAtomicallyAtEnd(t *testing.T) {
	url := testServer(t, func(conn *websocket.Conn) {
		handshake(t, conn)
		m := receive(t, conn)["message"].(map[string]any)
		send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "subscribe_terminal_response", "payload": map[string]any{"requestId": m["requestId"], "terminalId": "term", "subscriptionId": "sub", "slot": 10, "error": nil}}})
		for _, frame := range [][]byte{append([]byte{6, 10}, []byte(`{"rows":25,"cols":90}`)...), append([]byte{7, 10}, []byte("history")...), {8, 10}, append([]byte{1, 10}, []byte("live")...)} {
			_ = conn.WriteMessage(websocket.BinaryMessage, frame)
		}
		time.Sleep(30 * time.Millisecond)
	})
	c := new(Client)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Connect(ctx, Target{URL: url, ClientID: "test"}); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, err := c.SubscribeTerminal(ctx, "term")
	if err != nil {
		t.Fatal(err)
	}
	cols, rows := 0, 0
	s.OnReplaySize(func(c, r int) { cols, rows = c, r })
	b := make([]byte, len("\x1bchistorylive"))
	if _, err = io.ReadFull(s, b); err != nil {
		t.Fatal(err)
	}
	if string(b) != "\x1bchistorylive" || cols != 90 || rows != 25 {
		t.Fatalf("stream=%q size=%dx%d", b, cols, rows)
	}
}
