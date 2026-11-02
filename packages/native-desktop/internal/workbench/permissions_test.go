package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/getpaseo/paseo/packages/native-desktop/internal/client"
)

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
