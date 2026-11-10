package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const terminalQueueBytes = 8 << 20
const terminalQueueRecords = 1024

type terminalRecord struct {
	data       []byte
	cols, rows int
}

// TerminalStream is a connection-scoped attachment. Closing it releases the
// observation without terminating the daemon-owned process.
type TerminalStream struct {
	client                 *Client
	conn                   *websocket.Conn
	id, subscriptionID     string
	slot                   byte
	frames                 chan terminalRecord
	done                   chan struct{}
	closed                 bool // guarded by client.mu
	err                    error
	queued                 int
	readMu                 sync.Mutex
	replaySize             func(int, int)
	remaining              []byte
	replayActive           bool
	replayStarted          bool
	replayData             []byte
	replayCols, replayRows int
	replayErr              error
}

func (c *Client) SubscribeTerminal(ctx context.Context, id string) (*TerminalStream, error) {
	if id == "" {
		return nil, errors.New("terminal ID required")
	}
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return nil, ErrDisconnected
	}
	s := &TerminalStream{client: c, id: id, frames: make(chan terminalRecord, terminalQueueRecords), done: make(chan struct{})}
	data, _ := json.Marshal(map[string]any{"terminalId": id, "restore": map[string]any{"mode": "full-snapshot"}})
	_, err := c.callTerminal(ctx, "subscribe_terminal_request", data, nil, conn, s)
	if err != nil {
		// A timed-out subscribe may still allocate a server slot. Disconnect so
		// an eventual response cannot alias a later attachment.
		if errors.Is(err, ErrOutcomeUnknown) {
			c.disconnect(conn, err)
		}
		return nil, err
	}
	return s, nil
}

func (c *Client) attachTerminalLocked(conn *websocket.Conn, s *TerminalStream, raw json.RawMessage) error {
	var p struct {
		TerminalID     string  `json:"terminalId"`
		SubscriptionID string  `json:"subscriptionId"`
		Slot           *int    `json:"slot"`
		Error          *string `json:"error"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	if p.Error != nil {
		return errors.New(*p.Error)
	}
	if p.TerminalID != s.id || p.Slot == nil || *p.Slot < 0 || *p.Slot > 255 {
		return errors.New("invalid terminal attachment")
	}
	slot := byte(*p.Slot)
	if c.terminals == nil {
		c.terminals = make(map[byte]*TerminalStream)
	}
	if c.terminals[slot] != nil {
		return errors.New("terminal slot already attached")
	}
	s.conn, s.slot, s.subscriptionID = conn, slot, p.SubscriptionID
	c.terminals[slot] = s
	return nil
}

func (c *Client) dispatchTerminal(conn *websocket.Conn, data []byte) {
	if len(data) < 2 {
		return
	}
	op, slot := data[0], data[1]
	if op != 1 && op != 5 && op != 6 && op != 7 && op != 8 {
		return
	}
	c.mu.Lock()
	s := c.terminals[slot]
	if c.conn != conn || s == nil || s.closed {
		c.mu.Unlock()
		return
	}
	fail := func(err error) { c.mu.Unlock(); c.disconnect(conn, err) }
	switch op {
	case 6:
		if s.replayActive {
			fail(errors.New("nested terminal replay"))
			return
		}
		var meta struct {
			Cols int `json:"cols"`
			Rows int `json:"rows"`
		}
		if json.Unmarshal(data[2:], &meta) != nil || meta.Cols <= 0 || meta.Rows <= 0 {
			fail(errors.New("invalid terminal replay dimensions"))
			return
		}
		s.replayActive = true
		s.replayStarted = true
		s.replayData = nil
		s.replayCols = meta.Cols
		s.replayRows = meta.Rows
	case 7:
		if !s.replayActive || !s.replayStarted {
			fail(errors.New("terminal replay data outside replay"))
			return
		}
		payload := data[2:]
		if len(payload) > terminalQueueBytes-len(s.replayData) || len(s.replayData)+len(payload) > terminalQueueBytes {
			fail(ErrSubscriptionOverflow)
			return
		}
		s.replayData = append(s.replayData, payload...)
	case 8:
		if !s.replayActive || !s.replayStarted {
			fail(errors.New("terminal replay end without begin"))
			return
		}
		payload := append([]byte("\x1bc"), s.replayData...)
		if len(payload) > terminalQueueBytes-s.queued || len(s.frames) == cap(s.frames) {
			fail(ErrSubscriptionOverflow)
			return
		}
		s.queued += len(payload)
		s.frames <- terminalRecord{data: payload, cols: s.replayCols, rows: s.replayRows}
		s.replayActive = false
		s.replayStarted = false
		s.replayData = nil
		s.replayCols = 0
		s.replayRows = 0
	case 5, 1:
		if s.replayActive {
			fail(errors.New("live terminal output before replay end"))
			return
		}
		payload := data[2:]
		if op == 5 {
			payload = append([]byte("\x1bc"), payload...)
		}
		if len(payload) == 0 {
			c.mu.Unlock()
			return
		}
		if len(payload) > terminalQueueBytes-s.queued || len(s.frames) == cap(s.frames) {
			fail(ErrSubscriptionOverflow)
			return
		}
		s.queued += len(payload)
		s.frames <- terminalRecord{data: append([]byte(nil), payload...)}
	}
	c.mu.Unlock()
}

func (s *TerminalStream) closeLocked(err error) {
	if !s.closed {
		s.closed = true
		s.err = err
		close(s.done)
	}
}

// OnReplaySize installs the handler called in stream order before replay bytes.
// Set it before starting the reader.
func (s *TerminalStream) OnReplaySize(fn func(cols, rows int)) { s.replaySize = fn }
func (s *TerminalStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.readMu.Lock()
	defer s.readMu.Unlock()
	if len(s.remaining) == 0 {
		select {
		case record := <-s.frames:
			if record.cols > 0 && s.replaySize != nil {
				s.replaySize(record.cols, record.rows)
			}
			s.remaining = record.data
		case <-s.done:
			s.client.mu.Lock()
			err := s.err
			s.client.mu.Unlock()
			if err == nil {
				err = io.EOF
			}
			return 0, err
		}
	}
	n := copy(p, s.remaining)
	s.remaining = s.remaining[n:]
	s.client.mu.Lock()
	s.queued -= n
	s.client.mu.Unlock()
	return n, nil
}
func (s *TerminalStream) send(op byte, payload []byte) error {
	c := s.client
	c.mu.Lock()
	conn := s.conn
	valid := !s.closed && c.conn == conn && c.terminals[s.slot] == s
	writer := c.writer
	c.mu.Unlock()
	if !valid {
		return ErrDisconnected
	}
	writer <- struct{}{}
	defer func() { <-writer }()
	c.mu.Lock()
	valid = !s.closed && c.conn == conn && c.terminals[s.slot] == s
	c.mu.Unlock()
	if !valid {
		return ErrDisconnected
	}
	frame := make([]byte, 2+len(payload))
	frame[0] = op
	frame[1] = s.slot
	copy(frame[2:], payload)
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		c.disconnect(conn, err)
		return err
	}
	return nil
}
func (s *TerminalStream) Write(p []byte) (int, error) {
	if err := s.send(2, p); err != nil {
		return 0, err
	}
	return len(p), nil
}
func (s *TerminalStream) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return errors.New("invalid terminal size")
	}
	b, _ := json.Marshal(map[string]any{"cols": cols, "rows": rows, "intent": "claim"})
	return s.send(3, b)
}
func (s *TerminalStream) Close() error {
	c := s.client
	c.mu.Lock()
	if s.closed {
		c.mu.Unlock()
		return nil
	}
	s.closeLocked(nil)
	conn, owned, id := s.conn, c.owned, s.subscriptionID
	c.mu.Unlock()
	if conn == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if owned && id != "" {
		b, _ := json.Marshal(map[string]any{"subscriptionId": id})
		_, err := c.call(ctx, "subscription.release.request", b, nil, conn)
		if err != nil {
			c.disconnect(conn, err)
			return err
		}
	} else {
		// Legacy unsubscribe is fire-and-forget.
		c.mu.Lock()
		writer := c.writer
		c.mu.Unlock()
		select {
		case writer <- struct{}{}:
		case <-ctx.Done():
			c.disconnect(conn, ctx.Err())
			return ctx.Err()
		}
		err := conn.WriteJSON(map[string]any{"type": "session", "message": map[string]any{"type": "unsubscribe_terminal_request", "terminalId": s.id}})
		<-writer
		if err != nil {
			c.disconnect(conn, err)
			return err
		}
	}
	c.mu.Lock()
	if c.conn == conn && c.terminals[s.slot] == s {
		delete(c.terminals, s.slot)
	}
	c.mu.Unlock()
	return nil
}

func (c *Client) CreateTerminal(ctx context.Context, workspaceID, cwd string) (string, error) {
	b, _ := json.Marshal(map[string]any{"workspaceId": workspaceID, "cwd": cwd})
	raw, err := c.Call(ctx, "create_terminal_request", b)
	if err != nil {
		return "", err
	}
	var p struct {
		Terminal *struct {
			ID string `json:"id"`
		} `json:"terminal"`
		Error *string `json:"error"`
	}
	if err = json.Unmarshal(raw, &p); err != nil {
		return "", err
	}
	if p.Error != nil {
		return "", errors.New(*p.Error)
	}
	if p.Terminal == nil || p.Terminal.ID == "" {
		return "", fmt.Errorf("daemon returned no terminal")
	}
	return p.Terminal.ID, nil
}
