package timeline

import (
	"encoding/json"
	"testing"
)

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func TestHistoryAndIncrementalStream(t *testing.T) {
	var m Model
	err := m.Replace(raw(`{"requestId":"r","agentId":"a","agent":null,"direction":"tail","projection":"projected","epoch":"e","reset":false,"staleCursor":false,"gap":false,"window":{"minSeq":1,"maxSeq":2,"nextSeq":3},"startCursor":{"epoch":"e","seq":1},"endCursor":{"epoch":"e","seq":2},"hasOlder":false,"hasNewer":false,"entries":[{"provider":"codex","item":{"type":"assistant_message","text":"Hello","messageId":"msg"},"timestamp":"2026-02-08T20:10:00Z","seqStart":1,"seqEnd":2,"sourceSeqRanges":[{"startSeq":1,"endSeq":2}],"collapsed":["assistant_merge"]}],"error":null}`))
	if err != nil {
		t.Fatal(err)
	}
	update := raw(`{"type":"agent_stream","payload":{"agentId":"a","timestamp":"2026-02-08T20:10:01Z","seq":3,"epoch":"e","event":{"type":"timeline","provider":"codex","item":{"type":"assistant_message","text":" world","messageId":"msg"}}}}`)
	if gap, err := m.Apply(update); err != nil || gap {
		t.Fatalf("apply: gap=%v err=%v", gap, err)
	}
	if gap, err := m.Apply(update); err != nil || gap {
		t.Fatalf("duplicate: gap=%v err=%v", gap, err)
	}
	items := m.Items()
	if len(items) != 1 || items[0].Text != "Hello world" || items[0].SeqStart != 1 || items[0].SeqEnd != 3 || items[0].ID == "" {
		t.Fatalf("items: %+v", items)
	}
	if items[0].MessageID != "msg" || items[0].Epoch != "e" || items[0].Timestamp != "2026-02-08T20:10:00Z" || items[0].EndTimestamp != "2026-02-08T20:10:01Z" {
		t.Fatalf("lost source identity or timestamps: %+v", items[0])
	}
	items[0].Text = "changed"
	if m.Items()[0].Text != "Hello world" {
		t.Fatal("Items exposes model state")
	}
}

func TestToolLifecycleGapReplacementAndReset(t *testing.T) {
	var m Model
	history := raw(`{"requestId":"r","agentId":"a","agent":null,"direction":"tail","projection":"projected","epoch":"e","reset":false,"staleCursor":false,"gap":false,"window":{"minSeq":1,"maxSeq":1,"nextSeq":2},"startCursor":{"epoch":"e","seq":1},"endCursor":{"epoch":"e","seq":1},"hasOlder":false,"hasNewer":false,"entries":[{"provider":"claude","item":{"type":"tool_call","callId":"c","name":"shell","status":"running","detail":{"type":"shell","command":"ls"},"error":null},"timestamp":"2026-02-08T20:10:00Z","seqStart":1,"seqEnd":1,"sourceSeqRanges":[{"startSeq":1,"endSeq":1}],"collapsed":[]}],"error":null}`)
	if err := m.Replace(history); err != nil {
		t.Fatal(err)
	}
	update := raw(`{"type":"agent_stream","payload":{"agentId":"a","timestamp":"2026-02-08T20:10:01Z","seq":2,"epoch":"e","event":{"type":"timeline","provider":"claude","item":{"type":"tool_call","callId":"c","name":"shell","status":"completed","detail":{"type":"shell","output":"ok"},"error":null}}}}`)
	if gap, err := m.Apply(update); gap || err != nil {
		t.Fatalf("apply: %v %v", gap, err)
	}
	items := m.Items()
	if len(items) != 1 || items[0].ID == "" || items[0].SeqStart != 1 || items[0].SeqEnd != 2 || items[0].Text == "" {
		t.Fatalf("tool: %+v", items)
	}
	gapMessage := raw(`{"type":"agent_stream","payload":{"agentId":"a","timestamp":"2026-02-08T20:10:02Z","seq":4,"epoch":"e","event":{"type":"timeline","provider":"claude","item":{"type":"assistant_message","text":"late"}}}}`)
	if gap, err := m.Apply(gapMessage); !gap || err != nil {
		t.Fatalf("gap: %v %v", gap, err)
	}
	if len(m.Items()) != 1 {
		t.Fatal("gap event applied")
	}
	replacement := raw(`{"type":"agent.timeline.replacement","payload":{"agentId":"a","epoch":"new"}}`)
	if gap, err := m.Apply(replacement); !gap || err != nil {
		t.Fatalf("replacement: %v %v", gap, err)
	}
	m.Reset()
	if len(m.Items()) != 0 {
		t.Fatal("reset retained items")
	}
	if gap, err := m.Apply(update); !gap || err != nil {
		t.Fatalf("stream without baseline: %v %v", gap, err)
	}
}

func TestToolUpdatePreservesStructuredDetails(t *testing.T) {
	var m Model
	if err := m.Replace(raw(`{"agentId":"a","direction":"tail","epoch":"e","window":{"minSeq":1,"maxSeq":1,"nextSeq":2},"endCursor":{"epoch":"e","seq":1},"entries":[{"turnId":"turn","item":{"type":"tool_call","callId":"call","name":"shell","status":"running","metadata":{"origin":"provider"},"detail":{"type":"shell","command":"pwd"}},"seqStart":1,"seqEnd":1}]}`)); err != nil {
		t.Fatal(err)
	}
	if gap, err := m.Apply(raw(`{"type":"agent_stream","payload":{"agentId":"a","seq":2,"epoch":"e","event":{"type":"timeline","turnId":"turn","item":{"type":"tool_call","callId":"call","status":"failed","detail":{"type":"shell","output":"denied"},"error":"failed"}}}}`)); gap || err != nil {
		t.Fatalf("apply: gap=%v err=%v", gap, err)
	}
	items := m.Items()
	if len(items) != 1 || items[0].Name != "shell" || items[0].Status != "failed" || string(items[0].Detail) != `{"command":"pwd","output":"denied","type":"shell"}` || string(items[0].Error) != `"failed"` {
		t.Fatalf("structured tool details lost: %+v", items)
	}
	var preserved struct {
		Metadata map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal(items[0].Raw, &preserved); err != nil || preserved.Metadata["origin"] != "provider" {
		t.Fatalf("provider metadata lost: %s", items[0].Raw)
	}
	items[0].Detail[0] = 'x'
	if string(m.Items()[0].Detail) != `{"command":"pwd","output":"denied","type":"shell"}` {
		t.Fatal("snapshot exposes mutable detail")
	}
}

func TestToolUpdateWithNullDetailPreservesStructuredDetails(t *testing.T) {
	var m Model
	if err := m.Replace(raw(`{"agentId":"a","direction":"tail","epoch":"e","window":{"minSeq":1,"maxSeq":1,"nextSeq":2},"endCursor":{"epoch":"e","seq":1},"entries":[{"turnId":"turn","item":{"type":"tool_call","callId":"call","name":"shell","status":"running","detail":{"type":"shell","command":"pwd"}},"seqStart":1,"seqEnd":1}]}`)); err != nil {
		t.Fatal(err)
	}
	if gap, err := m.Apply(raw(`{"type":"agent_stream","payload":{"agentId":"a","seq":2,"epoch":"e","event":{"type":"timeline","turnId":"turn","item":{"type":"tool_call","callId":"call","status":"completed","detail":null}}}}`)); gap || err != nil {
		t.Fatalf("apply: %v %v", gap, err)
	}
	items := m.Items()
	var detail map[string]json.RawMessage
	if len(items) != 1 || json.Unmarshal(items[0].Detail, &detail) != nil || string(detail["command"]) != `"pwd"` || string(detail["type"]) != `"shell"` {
		t.Fatalf("tool details lost on null lifecycle update: %+v", items)
	}
}

func TestProjectedCoverageAndAgentSwitch(t *testing.T) {
	var m Model
	history := raw(`{"requestId":"r","agentId":"a","agent":null,"direction":"tail","projection":"projected","epoch":"e","reset":false,"staleCursor":false,"gap":false,"window":{"minSeq":1,"maxSeq":3,"nextSeq":4},"startCursor":{"epoch":"e","seq":1},"endCursor":{"epoch":"e","seq":1},"hasOlder":false,"hasNewer":true,"entries":[{"provider":"codex","item":{"type":"assistant_message","text":"ABC","messageId":"m"},"timestamp":"2026-02-08T20:10:00Z","seqStart":1,"seqEnd":3,"sourceSeqRanges":[{"startSeq":1,"endSeq":1},{"startSeq":3,"endSeq":3}],"collapsed":["assistant_merge"]}],"error":null}`)
	if err := m.Replace(history); err != nil {
		t.Fatal(err)
	}
	stream := raw(`{"type":"agent_stream","payload":{"agentId":"a","timestamp":"2026-02-08T20:10:00Z","seq":2,"epoch":"e","event":{"type":"timeline","provider":"codex","item":{"type":"user_message","text":"middle"}}}}`)
	if gap, err := m.Apply(stream); gap || err != nil {
		t.Fatalf("middle: %v %v", gap, err)
	}
	stream = raw(`{"type":"agent_stream","payload":{"agentId":"a","timestamp":"2026-02-08T20:10:00Z","seq":3,"epoch":"e","event":{"type":"timeline","provider":"codex","item":{"type":"assistant_message","text":"C","messageId":"m"}}}}`)
	if gap, err := m.Apply(stream); gap || err != nil {
		t.Fatalf("covered: %v %v", gap, err)
	}
	if got := m.Items(); len(got) != 2 || got[0].Text != "ABC" || got[1].Text != "middle" {
		t.Fatalf("covered rows: %+v", got)
	}
	m.Reset()
	other := raw(`{"requestId":"r2","agentId":"b","agent":null,"direction":"tail","projection":"projected","epoch":"f","reset":false,"staleCursor":false,"gap":false,"window":{"minSeq":1,"maxSeq":1,"nextSeq":2},"startCursor":{"epoch":"f","seq":1},"endCursor":{"epoch":"f","seq":1},"hasOlder":false,"hasNewer":false,"entries":[{"provider":"codex","item":{"type":"user_message","text":"new agent"},"timestamp":"2026-02-08T20:10:00Z","seqStart":1,"seqEnd":1,"sourceSeqRanges":[{"startSeq":1,"endSeq":1}],"collapsed":[]}],"error":null}`)
	if err := m.Replace(other); err != nil {
		t.Fatal(err)
	}
	if gap, err := m.Apply(stream); gap || err != nil {
		t.Fatalf("old agent: %v %v", gap, err)
	}
	if got := m.Items(); len(got) != 1 || got[0].Text != "new agent" {
		t.Fatalf("switched: %+v", got)
	}
}
func TestAssistantStreamDoesNotMergeTurns(t *testing.T) {
	var m Model
	if err := m.Replace(raw(`{"agentId":"a","direction":"tail","epoch":"e","endCursor":{"epoch":"e","seq":2},"entries":[{"turnId":"first","seqStart":1,"seqEnd":2,"item":{"type":"assistant_message","text":"First answer"}}]}`)); err != nil {
		t.Fatal(err)
	}
	for _, update := range []string{
		`{"type":"agent_stream","payload":{"agentId":"a","epoch":"e","seq":3,"event":{"type":"timeline","turnId":"second","item":{"type":"assistant_message","text":"Second"}}}}`,
		`{"type":"agent_stream","payload":{"agentId":"a","epoch":"e","seq":4,"event":{"type":"timeline","turnId":"second","item":{"type":"assistant_message","text":" answer"}}}}`,
	} {
		if reconcile, err := m.Apply(raw(update)); reconcile || err != nil {
			t.Fatalf("apply: %v %v", reconcile, err)
		}
	}
	items := m.Items()
	if len(items) != 2 || items[0].Text != "First answer" || items[1].Text != "Second answer" {
		t.Fatalf("cross-turn content: %+v", items)
	}
}
