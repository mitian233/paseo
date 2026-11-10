package workbench

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// A save must use the revision that was read and preserve edits on conflict.
func TestSupportingPanelsSaveConflictPreservesDraft(t *testing.T) {
	calls := directoryCaller(func(_ context.Context, typ string, raw json.RawMessage) (json.RawMessage, error) {
		switch typ {
		case "file_explorer_request":
			return json.RawMessage(`{"cwd":"/repo","path":"a.txt","mode":"file","directory":null,"file":{"path":"a.txt","kind":"text","encoding":"utf-8","content":"old","size":3,"modifiedAt":"stamp","revision":"rev1"},"error":null}`), nil
		case "fs.file.write.request":
			var req struct {
				Content            string `json:"content"`
				ExpectedRevision   string `json:"expectedRevision"`
				ExpectedModifiedAt string `json:"expectedModifiedAt"`
			}
			if err := json.Unmarshal(raw, &req); err != nil {
				t.Fatal(err)
			}
			if req.Content != "edited" || req.ExpectedRevision != "rev1" || req.ExpectedModifiedAt != "stamp" {
				t.Fatalf("incorrect write: %+v", req)
			}
			return json.RawMessage(`{"result":{"status":"conflict","version":{"status":"missing","cwd":"/repo","path":"a.txt"}}}`), nil
		default:
			t.Fatalf("unexpected request %s", typ)
			return nil, nil
		}
	})
	p := newSupportingPanelsWithCaller(context.Background(), calls, "w", "/repo", nil)
	if err := p.loadFile(context.Background(), "a.txt"); err != nil {
		t.Fatal(err)
	}
	p.documents["a.txt"].state.Buffer.Set("edited")
	if p.CanClose("a.txt") {
		t.Fatal("dirty file can close")
	}
	if err := p.saveFile(context.Background(), "a.txt"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "conflict") {
		t.Fatalf("expected conflict, got %v", err)
	}
	if p.CanClose("a.txt") || !p.HasUnsavedWork() {
		t.Fatal("conflict discarded draft")
	}
}

// Selecting a changed file must show that file's actual hunks, not another file's.
func TestSupportingPanelsSelectsChangedFile(t *testing.T) {
	calls := directoryCaller(func(_ context.Context, typ string, raw json.RawMessage) (json.RawMessage, error) {
		if typ != "checkout.diff.get.request" {
			t.Fatalf("unexpected request %s", typ)
		}
		return json.RawMessage(`{"cwd":"/repo","files":[{"path":"one","isNew":false,"isDeleted":false,"additions":1,"deletions":0,"hunks":[{"oldStart":1,"oldCount":0,"newStart":1,"newCount":1,"lines":[{"type":"add","content":"first"}]}]},{"path":"two","isNew":false,"isDeleted":false,"additions":1,"deletions":0,"hunks":[{"oldStart":1,"oldCount":0,"newStart":1,"newCount":1,"lines":[{"type":"add","content":"second"}]}]}],"error":null}`), nil
	})
	p := newSupportingPanelsWithCaller(context.Background(), calls, "w", "/repo", nil)
	if err := p.loadChanges(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := p.diffState("two")
	if state == nil {
		t.Fatal("selected diff missing")
	}
	got := state.New.String()
	if !strings.Contains(got, "second") || strings.Contains(got, "first") {
		t.Fatalf("wrong diff: %q", got)
	}
}
