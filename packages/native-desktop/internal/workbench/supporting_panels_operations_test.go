package workbench

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSupportingPanelsFileOperations(t *testing.T) {
	for _, tc := range []struct {
		op, path, name, kind, typ string
		req                       map[string]string
	}{
		{"create", "src", "new.go", "file", "fs.entry.create.request", map[string]string{"cwd": "/repo", "parentPath": "src", "name": "new.go", "kind": "file"}},
		{"rename", "src/old.go", "new.go", "", "fs.entry.rename.request", map[string]string{"cwd": "/repo", "path": "src/old.go", "name": "new.go"}},
		{"delete", "src/old.go", "", "", "fs.entry.delete.request", map[string]string{"cwd": "/repo", "path": "src/old.go"}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			calls := directoryCaller(func(_ context.Context, typ string, raw json.RawMessage) (json.RawMessage, error) {
				if typ != tc.typ {
					t.Fatalf("request %s", typ)
				}
				var got map[string]string
				if err := json.Unmarshal(raw, &got); err != nil {
					t.Fatal(err)
				}
				for key, want := range tc.req {
					if got[key] != want {
						t.Fatalf("%s = %q, want %q", key, got[key], want)
					}
				}
				return json.RawMessage(`{"cwd":"/repo","success":true,"renamedPath":"src/new.go","error":null}`), nil
			})
			p := newSupportingPanelsWithCaller(context.Background(), calls, "w", "/repo", nil)
			var oldPath, newPath string
			p.onEntryChanged = func(old, new string) { oldPath, newPath = old, new }
			if err := p.mutateEntry(context.Background(), tc.op, tc.path, tc.name, tc.kind); err != nil {
				t.Fatal(err)
			}
			if tc.op == "rename" && (oldPath != "src/old.go" || newPath != "src/new.go") {
				t.Fatalf("rename notification: %q %q", oldPath, newPath)
			}
			if tc.op == "delete" && (oldPath != "src/old.go" || newPath != "") {
				t.Fatalf("delete notification: %q %q", oldPath, newPath)
			}
		})
	}
}
func TestSupportingPanelsMergeCapabilities(t *testing.T) {
	tests := []struct {
		facts string
		want  int
	}{
		{`{"forge":"gitlab","detailedMergeStatus":"mergeable"}`, 3},
		{`{"forge":"gitlab","detailedMergeStatus":"blocked_status"}`, 0},
		{`{"forge":"gitea","mergeable":true,"hasMerged":false}`, 3},
		{`{"forge":"gitea","mergeable":false,"hasMerged":false}`, 0},
		{`{"forge":"github","mergeStateStatus":"CLEAN","repository":{"squashMergeAllowed":true}}`, 1},
		{`{"forge":"github","mergeStateStatus":"BLOCKED","repository":{"squashMergeAllowed":true}}`, 0},
	}
	for _, tc := range tests {
		var p panelPR
		if err := json.Unmarshal([]byte(`{"forgeSpecific":`+tc.facts+`}`), &p); err != nil {
			t.Fatal(err)
		}
		if got := len(p.mergeMethods()); got != tc.want {
			t.Errorf("%s: got %d methods, want %d", tc.facts, got, tc.want)
		}
	}
}
