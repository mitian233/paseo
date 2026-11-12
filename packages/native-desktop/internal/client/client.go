package client

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var ErrDisconnected = errors.New("daemon disconnected")
var ErrOutcomeUnknown = errors.New("request was sent but its outcome is unknown")
var ErrSubscriptionOverflow = errors.New("subscription updates overflowed; reload authoritative history")

type RPCError struct{ RequestID, RequestType, Code, Message string }

func (e *RPCError) Error() string {
	return fmt.Sprintf("daemon RPC %s: %s (%s)", e.RequestType, e.Message, e.Code)
}

type Target struct{ URL, ClientID, Password, LocalCredential string }
type ConnectionEvent struct {
	State string
	Epoch uint64
	Err   error
}
type Observation struct{ AgentIDs []string }
type result struct {
	payload json.RawMessage
	err     error
}
type pendingCall struct {
	ch       chan result
	sub      *Subscription
	started  bool
	terminal *TerminalStream
	usage    *usageStream
}
type Client struct {
	mu               sync.Mutex
	writer           chan struct{}
	observe          chan struct{}
	conn             *websocket.Conn
	connecting       context.CancelFunc
	pending          map[string]*pendingCall
	subs             map[*Subscription]struct{}
	terminals        map[byte]*TerminalStream
	info             json.RawMessage
	owned, selective bool
	epoch            uint64
	events           chan ConnectionEvent
}
type Subscription struct {
	Updates <-chan json.RawMessage
	updates chan json.RawMessage
	done    chan struct{}
	client  *Client
	agents  map[string]bool
	id      string
	closed  bool
	err     error
}

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
func (c *Client) Events() <-chan ConnectionEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.events == nil {
		c.events = make(chan ConnectionEvent, 16)
	}
	return c.events
}
func (c *Client) eventLocked(state string, err error) {
	if c.events == nil {
		c.events = make(chan ConnectionEvent, 16)
	}
	event := ConnectionEvent{state, c.epoch, err}
	select {
	case c.events <- event:
	default:
		select {
		case <-c.events:
		default:
		}
		c.events <- event
	}
}
func (c *Client) ServerInfo() json.RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append(json.RawMessage(nil), c.info...)
}

func (c *Client) Connect(ctx context.Context, target Target) (retErr error) {
	u, err := url.Parse(target.URL)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		return errors.New("target must be a ws or wss URL")
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/ws"
	}
	if target.Password != "" && target.LocalCredential != "" {
		return errors.New("choose one authentication method")
	}
	id := target.ClientID
	if id == "" {
		id, err = randomID()
		if err != nil {
			return err
		}
	}
	c.mu.Lock()
	if c.conn != nil || c.connecting != nil {
		c.mu.Unlock()
		return errors.New("client already connected")
	}
	ctx, cancel := context.WithCancel(ctx)
	c.connecting = cancel
	c.epoch++
	attempt := c.epoch
	c.eventLocked("connecting", nil)
	c.mu.Unlock()
	defer func() {
		cancel()
		c.mu.Lock()
		if c.epoch == attempt && c.connecting != nil {
			c.connecting = nil
			if retErr != nil {
				c.eventLocked("disconnected", retErr)
			}
		}
		c.mu.Unlock()
	}()
	headers := http.Header{}
	if compatibleBearer(target.Password) {
		headers.Set("Authorization", "Bearer "+target.Password)
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), headers)
	if err != nil {
		return err
	}
	hello := map[string]any{"type": "hello", "clientId": id, "clientType": "cli", "protocolVersion": 1, "capabilities": map[string]bool{"hello_rejection": true, "owned_subscriptions": true, "selective_agent_timeline": true, "all_providers": true, "timeline_notifications": true, "terminal_chunked_replay": true}}
	if target.LocalCredential != "" {
		hello["auth"] = map[string]string{"kind": "localCredential", "token": target.LocalCredential}
	} else if target.Password != "" {
		hello["auth"] = map[string]string{"kind": "password", "password": target.Password}
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(deadline)
		_ = conn.SetWriteDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err = conn.WriteJSON(hello); err != nil {
		conn.Close()
		return err
	}
	var info json.RawMessage
	var owned, selective bool
	for {
		_, data, e := conn.ReadMessage()
		if e != nil {
			conn.Close()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return e
		}
		var wire struct {
			Type    string `json:"type"`
			Reason  string `json:"reason"`
			Message struct {
				Type    string          `json:"type"`
				Payload json.RawMessage `json:"payload"`
			} `json:"message"`
		}
		if json.Unmarshal(data, &wire) != nil {
			continue
		}
		if wire.Type == "hello.rejected" {
			conn.Close()
			return fmt.Errorf("hello rejected: %s", wire.Reason)
		}
		if wire.Type == "session" && wire.Message.Type == "status" {
			var status struct {
				Status          string `json:"status"`
				ServerID        string `json:"serverId"`
				ProtocolVersion int    `json:"protocolVersion"`
				Features        struct {
					Owned     bool `json:"ownedSubscriptions"`
					Selective bool `json:"selectiveAgentTimeline"`
				} `json:"features"`
			}
			if json.Unmarshal(wire.Message.Payload, &status) == nil && status.Status == "server_info" && status.ServerID != "" {
				if status.ProtocolVersion != 0 && status.ProtocolVersion != 1 {
					conn.Close()
					return fmt.Errorf("unsupported protocol version %d", status.ProtocolVersion)
				}
				info = wire.Message.Payload
				owned, selective = status.Features.Owned, status.Features.Selective
				break
			}
		}
	}
	if !stop() && ctx.Err() != nil {
		conn.Close()
		return ctx.Err()
	}
	_ = conn.SetReadDeadline(time.Time{})
	_ = conn.SetWriteDeadline(time.Time{})
	c.mu.Lock()
	if ctx.Err() != nil || c.conn != nil {
		c.mu.Unlock()
		conn.Close()
		return ErrDisconnected
	}
	c.conn = conn
	c.connecting = nil
	c.owned, c.selective = owned, selective
	c.info = info
	c.pending = make(map[string]*pendingCall)
	c.subs = make(map[*Subscription]struct{})
	c.eventLocked("online", nil)
	c.mu.Unlock()
	go c.readLoop(conn)
	return nil
}
func compatibleBearer(s string) bool {
	if s == "" {
		return false
	}
	for i := range s {
		b := s[i]
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(b))) {
			return false
		}
	}
	return true
}

// write returns whether writing began, since cancellation after that point has
// an unknown outcome even when the socket reports an error.
func (c *Client) write(ctx context.Context, conn *websocket.Conn, value any, pending *pendingCall) (bool, error) {
	c.mu.Lock()
	if c.writer == nil {
		c.writer = make(chan struct{}, 1)
	}
	writer := c.writer
	c.mu.Unlock()
	select {
	case writer <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	case r := <-pending.ch:
		return false, r.err
	}
	defer func() { <-writer }()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if d, ok := ctx.Deadline(); ok {
		_ = conn.SetWriteDeadline(d)
	} else {
		_ = conn.SetWriteDeadline(time.Time{})
	}
	c.mu.Lock()
	if c.conn != conn {
		c.mu.Unlock()
		return false, ErrDisconnected
	}
	pending.started = true
	c.mu.Unlock()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	err := conn.WriteJSON(value)
	if !stop() && ctx.Err() != nil {
		return true, ctx.Err()
	}
	return true, err
}
func (c *Client) Call(ctx context.Context, requestType string, payload json.RawMessage) (json.RawMessage, error) {
	return c.call(ctx, requestType, payload, nil, nil)
}
func (c *Client) call(ctx context.Context, requestType string, payload json.RawMessage, sub *Subscription, expected *websocket.Conn) (json.RawMessage, error) {
	return c.callTerminal(ctx, requestType, payload, sub, expected, nil)
}
func (c *Client) callTerminal(ctx context.Context, requestType string, payload json.RawMessage, sub *Subscription, expected *websocket.Conn, terminal *TerminalStream) (json.RawMessage, error) {
	return c.callWithUsage(ctx, requestType, payload, sub, expected, terminal, nil)
}

func (c *Client) callWithUsage(ctx context.Context, requestType string, payload json.RawMessage, sub *Subscription, expected *websocket.Conn, terminal *TerminalStream, usage *usageStream) (json.RawMessage, error) {
	if requestType == "" {
		return nil, errors.New("empty request type")
	}
	fields := map[string]any{}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &fields); err != nil || fields == nil {
			return nil, errors.New("payload must be a JSON object")
		}
	}
	id := ""
	if requestType == "agent_permission_response" {
		// Permission decisions reuse the daemon's request ID; it identifies
		// the pending permission as well as its acknowledgement.
		var ok bool
		id, ok = fields["requestId"].(string)
		if !ok || id == "" {
			return nil, errors.New("permission requestId is required")
		}
	} else if _, ok := fields["requestId"]; ok {
		return nil, errors.New("requestId is managed by client")
	}
	if _, ok := fields["type"]; ok {
		return nil, errors.New("type is managed by client")
	}
	var err error
	if id == "" {
		id, err = randomID()
		if err != nil {
			return nil, err
		}
	}
	fields["type"] = requestType
	fields["requestId"] = id
	ch := make(chan result, 1)
	c.mu.Lock()
	conn := c.conn
	if conn == nil || (expected != nil && conn != expected) {
		c.mu.Unlock()
		return nil, ErrDisconnected
	}
	if sub != nil {
		if _, ok := c.subs[sub]; !ok {
			c.mu.Unlock()
			return nil, ErrDisconnected
		}
	}
	if _, exists := c.pending[id]; exists {
		c.mu.Unlock()
		return nil, errors.New("request already in flight")
	}
	pending := &pendingCall{ch: ch, sub: sub, terminal: terminal, usage: usage}
	c.pending[id] = pending
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.conn == conn {
			delete(c.pending, id)
		}
		c.mu.Unlock()
	}()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	started, writeErr := c.write(ctx, conn, map[string]any{"type": "session", "message": fields}, pending)
	if writeErr != nil {
		if !started {
			return nil, writeErr
		}
		err = writeErr
		c.disconnect(conn, err)
		return nil, fmt.Errorf("%w: %v", ErrOutcomeUnknown, err)
	}
	var updates <-chan struct{}
	if usage != nil {
		updates = usage.notify
	}
	for {
		select {
		case r := <-ch:
			if usage != nil && ctx.Err() == nil {
				c.deliverUsage(usage)
			}
			return r.payload, r.err
		case <-updates:
			if ctx.Err() == nil {
				c.deliverUsage(usage)
			}
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: %v", ErrOutcomeUnknown, ctx.Err())
		}
	}
}
func (c *Client) readLoop(conn *websocket.Conn) {
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			c.disconnect(conn, err)
			return
		}
		if kind == websocket.BinaryMessage {
			c.dispatchTerminal(conn, data)
		} else if kind == websocket.TextMessage {
			c.dispatch(conn, data)
		}
	}
}
func (c *Client) dispatch(conn *websocket.Conn, data []byte) {
	var wire struct {
		Type    string          `json:"type"`
		Message json.RawMessage `json:"message"`
	}
	if json.Unmarshal(data, &wire) != nil || wire.Type != "session" {
		return
	}
	var m struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(wire.Message, &m) != nil {
		return
	}
	var p struct {
		RequestID      string `json:"requestId"`
		RequestType    string `json:"requestType"`
		Error          string `json:"error"`
		Code           string `json:"code"`
		SubscriptionID string `json:"subscriptionId"`
		AgentID        string `json:"agentId"`
		TerminalID     string `json:"terminalId"`
	}
	_ = json.Unmarshal(m.Payload, &p)
	c.mu.Lock()
	if c.conn != conn {
		c.mu.Unlock()
		return
	}
	if m.Type == "usage.list_reports.update" {
		if pending := c.pending[p.RequestID]; pending != nil && pending.usage != nil && !pending.usage.closed {
			var update struct {
				Report json.RawMessage `json:"report"`
			}
			if json.Unmarshal(m.Payload, &update) == nil && len(update.Report) > 0 && string(update.Report) != "null" {
				pending.usage.reports = append(pending.usage.reports, update.Report)
				select {
				case pending.usage.notify <- struct{}{}:
				default:
				}
			}
		}
		c.mu.Unlock()
		return
	}
	if p.RequestID != "" && (m.Type == "rpc_error" || m.Type == "status" || m.Type == "pong" || m.Type == "agent_permission_resolved" || m.Type == "agent_archived" || m.Type == "agent_deleted" || strings.HasSuffix(m.Type, "_response") || strings.HasSuffix(m.Type, ".response") || strings.HasSuffix(m.Type, "/response")) {
		if pending, ok := c.pending[p.RequestID]; ok {
			if pending.usage != nil {
				if m.Type != "usage.list_reports.response" && m.Type != "rpc_error" {
					c.mu.Unlock()
					return
				}
				pending.usage.closed = true
			}
			var e error
			if m.Type == "rpc_error" {
				e = &RPCError{p.RequestID, p.RequestType, p.Code, p.Error}
			}
			if pending.terminal != nil && m.Type == "subscribe_terminal_response" {
				e = c.attachTerminalLocked(conn, pending.terminal, m.Payload)
			}
			if pending.sub != nil && m.Type == "agent.timeline.set_subscription.response" {
				pending.sub.id = p.SubscriptionID
			}
			select {
			case pending.ch <- result{m.Payload, e}:
			default:
			}
		}
	}
	if m.Type == "terminal_stream_exit" {
		for slot, stream := range c.terminals {
			if stream.id == p.TerminalID && (p.SubscriptionID == "" || stream.subscriptionID == p.SubscriptionID) {
				stream.closeLocked(io.EOF)
				delete(c.terminals, slot)
			}
		}
	}
	if m.Type == "agent_stream" || m.Type == "agent.timeline.replacement" {
		for sub := range c.subs {
			if !sub.closed && sub.agents[p.AgentID] && ((!c.owned && (p.SubscriptionID == "" || sub.id == p.SubscriptionID)) || (c.owned && sub.id != "" && sub.id == p.SubscriptionID)) {
				select {
				case sub.updates <- append(json.RawMessage(nil), wire.Message...):
				default:
					sub.closeLocked(ErrSubscriptionOverflow)
					delete(c.subs, sub)
					go c.disconnect(conn, ErrSubscriptionOverflow)
				}
			}
		}
	}
	c.mu.Unlock()
}
func (c *Client) disconnect(conn *websocket.Conn, err error) {
	c.mu.Lock()
	if c.conn != conn {
		c.mu.Unlock()
		return
	}
	c.conn = nil
	for _, pending := range c.pending {
		cause := ErrDisconnected
		if pending.started {
			cause = ErrOutcomeUnknown
		}
		select {
		case pending.ch <- result{err: fmt.Errorf("%w: %v", cause, err)}:
		default:
		}
	}
	for slot, stream := range c.terminals {
		stream.closeLocked(ErrDisconnected)
		delete(c.terminals, slot)
	}
	for s := range c.subs {
		s.closeLocked(fmt.Errorf("%w: %v", ErrDisconnected, err))
		delete(c.subs, s)
	}
	c.eventLocked("disconnected", err)
	c.mu.Unlock()
	_ = conn.Close()
}
func (c *Client) Close() error {
	c.mu.Lock()
	conn := c.conn
	if c.connecting != nil {
		c.connecting()
	}
	c.mu.Unlock()
	if conn != nil {
		c.disconnect(conn, ErrDisconnected)
	}
	return nil
}
func (s *Subscription) closeLocked(err error) {
	if !s.closed {
		s.closed = true
		s.err = err
		close(s.updates)
		close(s.done)
	}
}
func (s *Subscription) Err() error { s.client.mu.Lock(); defer s.client.mu.Unlock(); return s.err }
func (c *Client) legacyAgentsLocked() []string {
	ids := map[string]bool{}
	for s := range c.subs {
		for id := range s.agents {
			ids[id] = true
		}
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	return out
}
func (c *Client) lockObserve(ctx context.Context) (func(), error) {
	c.mu.Lock()
	if c.observe == nil {
		c.observe = make(chan struct{}, 1)
	}
	gate := c.observe
	c.mu.Unlock()
	select {
	case gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-gate
			return nil, err
		}
		return func() { <-gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Client) Observe(ctx context.Context, observation Observation) (*Subscription, error) {
	if len(observation.AgentIDs) == 0 {
		return nil, errors.New("observation requires agent IDs")
	}
	unlock, err := c.lockObserve(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	agents := make(map[string]bool)
	for _, id := range observation.AgentIDs {
		if id == "" {
			return nil, errors.New("empty agent ID")
		}
		agents[id] = true
	}
	ch := make(chan json.RawMessage, 128)
	sub := &Subscription{Updates: ch, updates: ch, done: make(chan struct{}), client: c, agents: agents}
	c.mu.Lock()
	if c.conn == nil {
		c.mu.Unlock()
		return nil, ErrDisconnected
	}
	c.subs[sub] = struct{}{}
	owned, selective := c.owned, c.selective
	conn := c.conn
	ids := observation.AgentIDs
	if !owned {
		ids = c.legacyAgentsLocked()
	}
	c.mu.Unlock()
	if owned || selective {
		data, _ := json.Marshal(map[string]any{"agentIds": ids})
		response, err := c.call(ctx, "agent.timeline.set_subscription.request", data, sub, conn)
		if err != nil {
			c.mu.Lock()
			delete(c.subs, sub)
			sub.closeLocked(err)
			c.mu.Unlock()
			if errors.Is(err, ErrOutcomeUnknown) {
				c.disconnect(conn, err)
			}
			return nil, err
		}
		var p struct {
			SubscriptionID string `json:"subscriptionId"`
		}
		if json.Unmarshal(response, &p) != nil || (owned && p.SubscriptionID == "") {
			c.mu.Lock()
			delete(c.subs, sub)
			sub.closeLocked(errors.New("missing subscription ID"))
			c.mu.Unlock()
			if owned {
				c.disconnect(conn, errors.New("missing subscription ID"))
			}
			return nil, errors.New("missing subscription ID")
		}
		c.mu.Lock()
		closed := sub.closed
		c.mu.Unlock()
		if closed {
			return nil, sub.Err()
		}
	}
	go func() {
		select {
		case <-ctx.Done():
			_ = sub.Close()
		case <-sub.done:
		}
	}()
	return sub, nil
}
func (s *Subscription) Close() error {
	c := s.client
	c.mu.Lock()
	if s.closed {
		c.mu.Unlock()
		return nil
	}
	delete(c.subs, s)
	s.closeLocked(nil)
	owned, selective, id, conn := c.owned, c.selective, s.id, c.conn
	c.mu.Unlock()
	if conn == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	unlock, err := c.lockObserve(ctx)
	if err != nil {
		c.disconnect(conn, err)
		return err
	}
	defer unlock()
	c.mu.Lock()
	if c.conn != conn {
		c.mu.Unlock()
		return ErrDisconnected
	}
	ids := c.legacyAgentsLocked()
	c.mu.Unlock()
	var typ string
	var payload []byte
	if owned && id != "" {
		typ = "subscription.release.request"
		payload, _ = json.Marshal(map[string]any{"subscriptionId": id})
	} else if !owned && selective {
		typ = "agent.timeline.set_subscription.request"
		payload, _ = json.Marshal(map[string]any{"agentIds": ids})
	} else {
		return nil
	}
	_, err = c.call(ctx, typ, payload, nil, conn)
	if err != nil {
		c.disconnect(conn, err)
	}
	return err
}
