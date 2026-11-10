package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/client"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/timeline"
)

func TestComposerEnterAndShiftEnter(t *testing.T) {
	draft, sent := "", ""
	tt := ui.NewTester(func(c *ui.Context) {
		composerView(c, &draft, true, false, false, func() { sent = draft }, func() {})
	}, 500, 200)
	if err := tt.Click("Message agent"); err != nil {
		t.Fatal(err)
	}
	tt.Type("first")
	tt.Key(ui.Shift, ui.KeyEnter)
	tt.Type("second")
	if draft != "first\nsecond" {
		t.Fatalf("draft: %q", draft)
	}
	tt.Key(0, ui.KeyEnter)
	if sent != "first\nsecond" {
		t.Fatalf("sent: %q", sent)
	}
}

func TestToolDetailSectionsKeepCommandOutputAndError(t *testing.T) {
	sections := toolDetailSections(json.RawMessage(`{"type":"shell","command":"pwd","output":"/repo","exitCode":1}`), json.RawMessage(`"failed"`))
	want := map[string]string{"Command": "pwd", "Output": "/repo", "Exit code": "1", "Error": "failed"}
	for _, section := range sections {
		if expected, ok := want[section.label]; ok && section.text == expected {
			delete(want, section.label)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing readable details: %v; got %+v", want, sections)
	}
}

func TestConversationMessageCopy(t *testing.T) {
	for _, kind := range []string{"user_message", "assistant_message"} {
		t.Run(kind, func(t *testing.T) {
			a := &App{}
			item := timeline.Item{ID: "message", Kind: kind, Text: "**hello**\nworld"}
			tt := ui.NewTester(func(c *ui.Context) { a.conversationItem(c, item) }, 600, 240)
			if err := tt.Click("Copy message"); err != nil {
				t.Fatal(err)
			}
			if got := tt.Clipboard(); got != item.Text {
				t.Fatalf("clipboard = %q", got)
			}
			html, err := tt.ClipboardData().Read(transfer.HTML)
			if err != nil || !strings.Contains(string(html), "<strong>hello</strong>") {
				t.Fatalf("rich clipboard: %q %v", html, err)
			}
		})
	}
}

func TestCompletedTurnDisclosureKeepsFinalReplyAndLiveTurn(t *testing.T) {
	items := []timeline.Item{
		{ID: "prompt", Kind: "user_message", Text: "Question", Timestamp: "2026-02-08T20:10:00Z"},
		{ID: "thought", Kind: "reasoning", Text: "Thinking"},
		{ID: "tool", Kind: "tool_call", Name: "shell"},
		{ID: "reply", Kind: "assistant_message", Text: "Done", Timestamp: "2026-02-08T20:10:03Z", EndTimestamp: "2026-02-08T20:10:03Z"},
		{ID: "live", Kind: "user_message", Text: "Next"},
		{ID: "live-tool", Kind: "tool_call", Name: "read"},
		{ID: "live-reply", Kind: "assistant_message", Text: "In progress"},
	}
	rows := conversationRows(items, nil, true)
	var ids []string
	for _, row := range rows {
		ids = append(ids, row.item.ID)
	}
	if fmt.Sprint(ids) != "[prompt reply live live-tool live-reply]" {
		t.Fatalf("collapsed rows: %v", ids)
	}
	if rows[0].foldedCount != 2 {
		t.Fatalf("missing work summary: %+v", rows[0])
	}
	if !rows[1].turnComplete || rows[1].turnText != "Done" || !strings.HasPrefix(turnTiming(rows[1].startedAt, rows[1].completedAt), "3s") {
		t.Fatalf("turn footer: %+v", rows[1])
	}
	rows = conversationRows(items, map[string]bool{"prompt": true}, true)
	ids = nil
	for _, row := range rows {
		ids = append(ids, row.item.ID)
	}
	if fmt.Sprint(ids) != "[prompt thought tool reply live live-tool live-reply]" {
		t.Fatalf("expanded rows: %v", ids)
	}
}

func TestConversationActionsUseSourceBoundaries(t *testing.T) {
	call := permissionCallFunc(func(_ context.Context, typ string, raw json.RawMessage) (json.RawMessage, error) {
		var p map[string]any
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
		switch typ {
		case "agent.rewind.request":
			if p["agentId"] != "agent" || p["messageId"] != "source-msg" || p["mode"] != "conversation" {
				t.Fatalf("rewind payload: %s", raw)
			}
			return json.RawMessage(`{"agentId":"agent","ok":true,"error":null}`), nil
		case "agent.fork_context.request":
			cursor, _ := p["boundaryCursor"].(map[string]any)
			if p["agentId"] != "agent" || cursor["epoch"] != "epoch" || cursor["seq"] != float64(42) {
				t.Fatalf("fork payload: %s", raw)
			}
			return json.RawMessage(`{"agentId":"agent","attachment":{"type":"text","mimeType":"text/plain","text":"history"},"error":null}`), nil
		default:
			t.Fatalf("unexpected request %s", typ)
			return nil, nil
		}
	})
	item := timeline.Item{ID: "display-row", MessageID: "source-msg", Epoch: "epoch", SeqEnd: 42}
	if err := requestRewind(context.Background(), call, "agent", item.MessageID, "conversation"); err != nil {
		t.Fatal(err)
	}
	attachment, err := requestFork(context.Background(), call, "agent", item)
	if err != nil || string(attachment) != `{"type":"text","mimeType":"text/plain","text":"history"}` {
		t.Fatalf("fork: %s %v", attachment, err)
	}
}

func TestConversationActionsRejectUnrelatedAcknowledgement(t *testing.T) {
	call := permissionCallFunc(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"agentId":"other","ok":true,"attachment":{"type":"text","mimeType":"text/plain","text":"history"}}`), nil
	})
	if err := requestRewind(context.Background(), call, "agent", "message", "conversation"); err == nil {
		t.Fatal("unrelated rewind accepted")
	}
	if _, err := requestFork(context.Background(), call, "agent", timeline.Item{Epoch: "e", SeqEnd: 1}); err == nil {
		t.Fatal("unrelated fork accepted")
	}
}

func TestPermissionsSnapshotAndLiveEvents(t *testing.T) {
	var p permissions
	if err := p.replace("agent", json.RawMessage(`{"agentId":"agent","agent":{"pendingPermissions":[{"id":"p1","name":"Write","kind":"tool"}]}}`)); err != nil {
		t.Fatal(err)
	}
	if len(p.cards) != 1 || p.cards[0].request.ID != "p1" {
		t.Fatalf("snapshot: %#v", p.cards)
	}
	if err := p.apply("agent", json.RawMessage(`{"type":"agent_stream","payload":{"agentId":"agent","event":{"type":"permission_requested","request":{"id":"p2","name":"Ask","kind":"question"}}}}`)); err != nil {
		t.Fatal(err)
	}
	if err := p.apply("agent", json.RawMessage(`{"type":"agent_stream","payload":{"agentId":"agent","event":{"type":"permission_resolved","requestId":"p1"}}}`)); err != nil {
		t.Fatal(err)
	}
	if len(p.cards) != 1 || p.cards[0].request.ID != "p2" {
		t.Fatalf("events: %#v", p.cards)
	}
}

func TestPermissionSubmissionAcknowledgementAndFailure(t *testing.T) {
	var p permissions
	_ = p.replace("agent", json.RawMessage(`{"agentId":"agent","agent":{"pendingPermissions":[{"id":"p1","name":"Write","kind":"tool"}]}}`))
	response := permissionResponse{Behavior: "allow", SelectedActionID: "accept"}
	if !p.begin("p1") || p.begin("p1") {
		t.Fatal("double submit allowed")
	}
	p.finish("p1", errors.New("connection lost"))
	if len(p.cards) != 1 || p.cards[0].busy || p.cards[0].err == "" {
		t.Fatalf("failure lost: %#v", p.cards)
	}
	if !p.begin("p1") {
		t.Fatal("retry blocked")
	}
	p.finish("p1", nil)
	if len(p.cards) != 0 {
		t.Fatal("acknowledged permission still pending")
	}
	_ = response
}

type permissionCallFunc func(context.Context, string, json.RawMessage) (json.RawMessage, error)

func (f permissionCallFunc) Call(ctx context.Context, typ string, payload json.RawMessage) (json.RawMessage, error) {
	return f(ctx, typ, payload)
}

func TestSendPermissionChecksAcknowledgement(t *testing.T) {
	call := permissionCallFunc(func(_ context.Context, typ string, raw json.RawMessage) (json.RawMessage, error) {
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		if typ != "agent_permission_response" || payload["agentId"] != "a" || payload["requestId"] != "p" {
			t.Fatalf("wrong request: %s %s", typ, raw)
		}
		return json.RawMessage(`{"agentId":"other","requestId":"p","resolution":{"behavior":"allow"}}`), nil
	})
	if err := sendPermission(context.Background(), call, "a", "p", permissionResponse{Behavior: "allow"}); err == nil {
		t.Fatal("accepted unrelated acknowledgement")
	}
}

func TestQuestionAnswersRespectChoicesAndOther(t *testing.T) {
	q := permissionQuestion{Header: "Flavor", Options: []permissionOption{{Label: "Tea"}, {Label: "Coffee"}}, MultiSelect: true, AllowOther: true}
	answers, err := questionAnswers([]permissionQuestion{q}, []questionAnswer{{selected: map[int]bool{0: true, 1: true}, other: " juice "}}, false)
	if err != nil || answers["Flavor"] != "Tea, Coffee, juice" {
		t.Fatalf("answers: %#v %v", answers, err)
	}
	if _, err := questionAnswers([]permissionQuestion{{Header: "Required", Options: []permissionOption{{Label: "Yes"}}}}, []questionAnswer{{}}, false); err == nil {
		t.Fatal("accepted unanswered question")
	}
}

func TestPermissionSubmissionWaitsAndCancels(t *testing.T) {
	var p permissions
	_ = p.replace("a", json.RawMessage(`{"agentId":"a","agent":{"pendingPermissions":[{"id":"p","name":"Write","kind":"tool"}]}}`))
	started := make(chan struct{})
	posted := make(chan func(), 1)
	ctx, cancel := context.WithCancel(context.Background())
	call := permissionCallFunc(func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	p.submit(ctx, call, "a", "p", permissionResponse{Behavior: "allow"}, func(f func()) { posted <- f })
	<-started
	if len(p.cards) != 1 || !p.cards[0].busy {
		t.Fatal("request disappeared before acknowledgement")
	}
	cancel()
	(<-posted)()
	if len(p.cards) != 1 || p.cards[0].busy || p.cards[0].err == "" {
		t.Fatalf("cancellation state: %#v", p.cards)
	}
}

func TestQuestionDismissAndInputPreservation(t *testing.T) {
	c := newPermissionCard(permissionRequest{ID: "q", Kind: "question", Input: map[string]any{"questions": []any{map[string]any{"header": "Reply", "question": "What?", "options": []any{}, "allowEmpty": true}}, "extra": "keep"}})
	r, err := c.questionResponse(true)
	if err != nil || r.Behavior != "allow" || r.UpdatedInput["extra"] != "keep" || r.UpdatedInput["answers"].(map[string]string)["Reply"] != "" {
		t.Fatalf("dismiss response: %#v %v", r, err)
	}
}

func TestPermissionUnknownOutcomeBlocksAnotherSubmission(t *testing.T) {
	var p permissions
	_ = p.replace("a", json.RawMessage(`{"agentId":"a","agent":{"pendingPermissions":[{"id":"p","name":"Write","kind":"tool"}]}}`))
	p.begin("p")
	p.finish("p", client.ErrOutcomeUnknown)
	if p.begin("p") || len(p.cards) != 1 || p.cards[0].err == "" {
		t.Fatal("unknown outcome allowed resubmission or disappeared")
	}
}
