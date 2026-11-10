package timeline

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Item is a display-independent snapshot of a timeline row.
type Item struct {
	ID           string
	Kind         string
	Text         string
	MessageID    string
	Epoch        string
	Timestamp    string
	EndTimestamp string
	TurnID       string
	Name         string
	Status       string
	Detail       json.RawMessage
	Error        json.RawMessage
	Raw          json.RawMessage
	SeqStart     int64
	SeqEnd       int64
}

type seqRange struct {
	Start int64 `json:"startSeq"`
	End   int64 `json:"endSeq"`
}
type cursor struct {
	Epoch string `json:"epoch"`
	Seq   int64  `json:"seq"`
}
type wireItem struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Message   string          `json:"message"`
	MessageID string          `json:"messageId"`
	CallID    string          `json:"callId"`
	PluginID  string          `json:"pluginId"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Status    string          `json:"status"`
	Raw       json.RawMessage `json:"-"`
	Detail    json.RawMessage `json:"detail"`
	Error     json.RawMessage `json:"error"`
}

func (w *wireItem) UnmarshalJSON(data []byte) error {
	type plain wireItem
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*w = wireItem(decoded)
	w.Raw = append(json.RawMessage(nil), data...)
	return nil
}

type entry struct {
	Item      wireItem   `json:"item"`
	TurnID    string     `json:"turnId"`
	Timestamp string     `json:"timestamp"`
	SeqStart  int64      `json:"seqStart"`
	SeqEnd    int64      `json:"seqEnd"`
	Ranges    []seqRange `json:"sourceSeqRanges"`
}
type row struct {
	Item
	messageID, turnID, identity string
	ranges                      []seqRange
	tool                        wireItem
}

// Model contains one agent's authoritative history and ordered stream updates.
// It performs no network operations; a true Apply result asks the caller to
// fetch and replace the authoritative timeline before applying further updates.
type Model struct {
	mu               sync.Mutex
	agent, epoch     string
	start, end       *cursor
	covered          int64
	ready, reconcile bool
	rows             []row
}

func (m *Model) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agent = ""
	m.epoch = ""
	m.start = nil
	m.end = nil
	m.covered = 0
	m.ready = false
	m.reconcile = false
	m.rows = nil
}

func (m *Model) Items() []Item {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make([]Item, len(m.rows))
	for i := range m.rows {
		items[i] = m.rows[i].Item
		items[i].Detail = append(json.RawMessage(nil), items[i].Detail...)
		items[i].Error = append(json.RawMessage(nil), items[i].Error...)
		items[i].Raw = append(json.RawMessage(nil), items[i].Raw...)
	}
	return items
}

// Replace accepts the payload of fetch_agent_timeline_response.
func (m *Model) Replace(payload json.RawMessage) error {
	var p struct {
		Agent     string  `json:"agentId"`
		Epoch     string  `json:"epoch"`
		Direction string  `json:"direction"`
		Error     *string `json:"error"`
		Start     *cursor `json:"startCursor"`
		End       *cursor `json:"endCursor"`
		Window    struct {
			Next int64 `json:"nextSeq"`
		} `json:"window"`
		Entries []entry `json:"entries"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("timeline history: %w", err)
	}
	if p.Error != nil {
		return fmt.Errorf("timeline history: %s", *p.Error)
	}
	if p.Agent == "" || p.Epoch == "" || p.Direction != "tail" || p.Entries == nil {
		return fmt.Errorf("invalid timeline history payload")
	}
	if (p.Start != nil && p.Start.Epoch != p.Epoch) || (p.End != nil && p.End.Epoch != p.Epoch) {
		return fmt.Errorf("timeline cursor epoch mismatch")
	}
	rows := make([]row, 0, len(p.Entries))
	for _, e := range p.Entries {
		if e.Item.Type == "" || e.SeqStart < 0 || e.SeqEnd < e.SeqStart {
			return fmt.Errorf("invalid timeline entry")
		}
		r := newRow(e.Item, e.TurnID, p.Epoch, e.SeqStart, e.SeqEnd)
		r.Timestamp, r.EndTimestamp = e.Timestamp, e.Timestamp
		if len(e.Ranges) > 0 {
			r.ranges = append([]seqRange(nil), e.Ranges...)
		}
		rows = append(rows, r)
	}
	covered := int64(0)
	if p.End != nil {
		covered = p.End.Seq
	} else if p.Window.Next > 0 {
		covered = p.Window.Next - 1
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agent = p.Agent
	m.epoch = p.Epoch
	m.start = p.Start
	m.end = p.End
	m.covered = covered
	m.rows = rows
	m.ready = true
	m.reconcile = false
	return nil
}

// Apply accepts one session message from Subscription.Updates.
func (m *Model) Apply(message json.RawMessage) (bool, error) {
	var msg struct {
		Type    string `json:"type"`
		Payload struct {
			Agent     string `json:"agentId"`
			Epoch     string `json:"epoch"`
			Timestamp string `json:"timestamp"`
			Seq       *int64 `json:"seq"`
			Event     struct {
				Type   string   `json:"type"`
				TurnID string   `json:"turnId"`
				Item   wireItem `json:"item"`
			} `json:"event"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(message, &msg); err != nil {
		return false, fmt.Errorf("timeline update: %w", err)
	}
	if msg.Type != "agent_stream" && msg.Type != "agent.timeline.replacement" {
		return false, nil
	}
	p := msg.Payload
	if p.Agent == "" {
		return false, fmt.Errorf("timeline update missing agentId")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ready {
		return true, nil
	}
	if p.Agent != m.agent {
		return false, nil
	}
	if msg.Type == "agent.timeline.replacement" {
		m.reconcile = true
		return true, nil
	}
	if p.Event.Type != "timeline" {
		return m.reconcile, nil
	}
	if p.Event.Item.Type == "" {
		return false, fmt.Errorf("timeline update missing item")
	}
	if m.reconcile {
		return true, nil
	}
	if p.Seq == nil || p.Epoch == "" || *p.Seq < 0 || p.Epoch != m.epoch {
		m.reconcile = true
		return true, nil
	}
	seq := *p.Seq
	if seq <= m.covered {
		return false, nil
	}
	if seq != m.covered+1 {
		m.reconcile = true
		return true, nil
	}
	coveredByRow := false
	for _, r := range m.rows {
		for _, span := range r.ranges {
			if span.Start <= seq && seq <= span.End {
				coveredByRow = true
				break
			}
		}
		if coveredByRow {
			break
		}
	}
	if !coveredByRow {
		m.applyItem(p.Event.Item, p.Event.TurnID, p.Timestamp, seq)
	}
	m.covered = seq
	m.end = &cursor{Epoch: m.epoch, Seq: seq}
	if m.start == nil {
		m.start = &cursor{Epoch: m.epoch, Seq: seq}
	}
	return false, nil
}

func newRow(item wireItem, turn, epoch string, start, end int64) row {
	identity := ""
	switch item.Type {
	case "tool_call":
		if item.CallID != "" {
			identity = "tool:" + turn + ":" + item.CallID
		}
	case "plugin":
		if item.ID != "" {
			identity = "plugin:" + item.PluginID + ":" + item.ID
		}
	}
	id := epoch + ":" + strconv.FormatInt(start, 10)
	if identity != "" {
		id = identity
	}
	return row{Item: Item{ID: id, Kind: item.Type, Text: itemText(item), MessageID: item.MessageID, Epoch: epoch, TurnID: turn, Name: item.Name, Status: item.Status, Detail: item.Detail, Error: item.Error, Raw: item.Raw, SeqStart: start, SeqEnd: end}, messageID: item.MessageID, turnID: turn, identity: identity, tool: item, ranges: []seqRange{{Start: start, End: end}}}
}

func itemText(item wireItem) string {
	switch item.Type {
	case "tool_call":
		text := item.Name
		if item.Status != "" {
			if text != "" {
				text += " — "
			}
			text += item.Status
		}
		if len(item.Detail) > 0 && string(item.Detail) != "null" {
			text += "\n" + string(item.Detail)
		}
		if len(item.Error) > 0 && string(item.Error) != "null" {
			text += "\n" + string(item.Error)
		}
		return text
	default:
		if item.Text != "" {
			return item.Text
		}
		if item.Message != "" {
			return item.Message
		}
		if item.Name != "" {
			return item.Name
		}
		if len(item.Raw) > 0 {
			return string(item.Raw)
		}
		return item.Status
	}
}

func (m *Model) applyItem(item wireItem, turn, timestamp string, seq int64) {
	incoming := newRow(item, turn, m.epoch, seq, seq)
	incoming.Timestamp, incoming.EndTimestamp = timestamp, timestamp
	if incoming.identity != "" {
		for i := range m.rows {
			r := &m.rows[i]
			if r.identity == incoming.identity {
				if item.Type == "tool_call" {
					item = mergeTool(r.tool, item)
					r.tool = item
					r.Text = itemText(item)
					r.Name, r.Status, r.Detail, r.Error, r.Raw = item.Name, item.Status, item.Detail, item.Error, item.Raw
				} else {
					r.Text = incoming.Text
				}
				if seq > r.SeqEnd {
					r.SeqEnd = seq
				}
				if timestamp != "" {
					r.EndTimestamp = timestamp
				}
				r.ranges = append(r.ranges, seqRange{seq, seq})
				return
			}
		}
	}
	if n := len(m.rows); n > 0 {
		r := &m.rows[n-1]
		if (item.Type == "assistant_message" && r.Kind == item.Type && r.turnID == turn && (item.MessageID == "" || item.MessageID == r.messageID)) || (item.Type == "reasoning" && r.Kind == item.Type && r.turnID == turn) {
			r.Text += item.Text
			if seq > r.SeqEnd {
				r.SeqEnd = seq
			}
			if timestamp != "" {
				r.EndTimestamp = timestamp
			}
			r.ranges = append(r.ranges, seqRange{seq, seq})
			return
		}
	}
	m.rows = append(m.rows, incoming)
}

func mergeTool(old, next wireItem) wireItem {
	if next.Name == "" {
		next.Name = old.Name
	}
	if next.Status == "" || (old.Status == "failed") || (old.Status == "completed" && next.Status != "failed") || (old.Status == "canceled" && next.Status == "running") {
		next.Status = old.Status
	}
	var a, b map[string]json.RawMessage
	if json.Unmarshal(old.Detail, &a) == nil && json.Unmarshal(next.Detail, &b) == nil && a != nil && b != nil {
		var oldType, newType string
		_ = json.Unmarshal(a["type"], &oldType)
		_ = json.Unmarshal(b["type"], &newType)
		if oldType == newType {
			for k, v := range b {
				a[k] = v
			}
			next.Detail, _ = json.Marshal(a)
		} else if newType == "unknown" && oldType != "unknown" {
			next.Detail = old.Detail
		}
	} else if len(next.Detail) == 0 || strings.TrimSpace(string(next.Detail)) == "null" || b == nil {
		next.Detail = old.Detail
	}
	if len(next.Error) == 0 || string(next.Error) == "null" {
		if next.Status == "failed" {
			next.Error = old.Error
		}
	}
	var previous, current map[string]json.RawMessage
	if json.Unmarshal(old.Raw, &previous) == nil && previous != nil && json.Unmarshal(next.Raw, &current) == nil && current != nil {
		for key, value := range current {
			previous[key] = value
		}
		previous["name"], _ = json.Marshal(next.Name)
		previous["status"], _ = json.Marshal(next.Status)
		if len(next.Detail) > 0 {
			previous["detail"] = next.Detail
		}
		if len(next.Error) > 0 {
			previous["error"] = next.Error
		}
		next.Raw, _ = json.Marshal(previous)
	}
	return next
}
