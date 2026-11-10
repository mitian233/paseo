package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/getpaseo/paseo/packages/native-desktop/internal/client"
)

type navigationTestCaller struct {
	response string
	err      error
	calls    int
	kind     string
	payload  json.RawMessage
}

func (f *navigationTestCaller) Call(_ context.Context, kind string, payload json.RawMessage) (json.RawMessage, error) {
	f.calls++
	f.kind = kind
	f.payload = payload
	return json.RawMessage(f.response), f.err
}

func TestNavigationRequestRejectsDeniedAndUnknownResults(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		err            error
		want           string
	}{
		{"denied", `{"requestId":"r","workspaceId":"w","accepted":false,"title":null,"error":"denied"}`, nil, "denied"},
		{"missing acceptance", `{"requestId":"r","workspaceId":"w","error":null}`, nil, "acknowledgement"},
		{"unknown", ``, client.ErrOutcomeUnknown, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &navigationTestCaller{response: tc.response, err: tc.err}
			err := navigationRequest(context.Background(), f, "workspace.title.set.request", map[string]any{"workspaceId": "w", "title": "new"})
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Fatalf("error = %v", err)
			}
			if f.calls != 1 {
				t.Fatalf("calls = %d", f.calls)
			}
			if tc.err != nil && !errors.Is(err, client.ErrOutcomeUnknown) {
				t.Fatalf("lost unknown outcome: %v", err)
			}
		})
	}
}

func TestNavigationRequestAcceptsMatchingAcknowledgement(t *testing.T) {
	f := &navigationTestCaller{response: `{"requestId":"r","workspaceId":"w","accepted":true,"title":"new","error":null}`}
	if err := navigationRequest(context.Background(), f, "workspace.title.set.request", map[string]any{"workspaceId": "w", "title": "new"}); err != nil {
		t.Fatal(err)
	}
	if f.kind != "workspace.title.set.request" || string(f.payload) != `{"title":"new","workspaceId":"w"}` {
		t.Fatalf("request %s %s", f.kind, f.payload)
	}
}

func TestNavigationRequestDetectsPendingCreation(t *testing.T) {
	f := &navigationTestCaller{response: `{"requestId":"r","workspace":null,"setupTerminalId":null,"error":null,"creation":{"phase":"accepted","error":null}}`}
	err := navigationRequest(context.Background(), f, "workspace.create.request", map[string]any{"source": map[string]any{"kind": "directory", "path": "/tmp/repo"}})
	if !errors.Is(err, errNavigationPending) {
		t.Fatalf("expected pending, got %v", err)
	}
	if f.calls != 1 {
		t.Fatalf("calls = %d", f.calls)
	}
}

func TestNavigationRequestRejectsWrongWorkspace(t *testing.T) {
	f := &navigationTestCaller{response: `{"requestId":"r","workspaceId":"other","accepted":true,"title":"new","error":null}`}
	if err := navigationRequest(context.Background(), f, "workspace.title.set.request", map[string]any{"workspaceId": "w", "title": "new"}); err == nil {
		t.Fatal("accepted acknowledgement for another workspace")
	}
}
