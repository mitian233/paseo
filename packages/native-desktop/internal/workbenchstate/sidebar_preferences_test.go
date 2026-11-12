package workbenchstate

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSidebarDirectoryPreferencesRoundTrip(t *testing.T) {
	want := json.RawMessage(`{"groupMode":"status","hostFilters":["remote"],"workspaceOrder":{"project":["workspace-b","workspace-a"]}}`)
	s := State{SidebarDirectory: want}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var a, b map[string]any
	if err = json.Unmarshal(want, &a); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(got.SidebarDirectory, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("sidebar preferences = %#v, want %#v", b, a)
	}
}
