package workbench

import (
	"github.com/egoist/mygo/ui"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/desktopui"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscardConfirmationInvokesGuardedAction(t *testing.T) {
	a := &App{}
	discarded := false
	a.confirm("Discard the unsaved draft?", func() { discarded = true })
	tt := ui.NewTester(func(c *ui.Context) { a.dialogs(c) }, 600, 400)
	if err := tt.Click("Discard changes"); err != nil {
		t.Fatal(err)
	}
	if !discarded {
		t.Fatal("discard confirmation closed without completing requested action")
	}
}

func TestAgentSnapshotAndTurnEventsDriveStopControl(t *testing.T) {
	a := &App{selected: "a"}
	a.applyAgentActivity([]byte(`{"agentId":"a","agent":{"status":"running","capabilities":{"supportsRewindConversation":true}}}`))
	if !a.agentRunning || !a.rewindCapabilities.Conversation {
		t.Fatal("running history did not enable stop or rewind controls")
	}
	a.applyAgentActivity([]byte(`{"agentId":"other","event":{"type":"turn_completed"}}`))
	if !a.agentRunning {
		t.Fatal("another agent stopped the selected conversation")
	}
	a.applyAgentActivity([]byte(`{"agentId":"a","event":{"type":"turn_completed"}}`))
	if a.agentRunning {
		t.Fatal("completed turn left Stop enabled")
	}
	a.applyAgentActivity([]byte(`{"agentId":"a","event":{"type":"turn_started"}}`))
	if !a.agentRunning {
		t.Fatal("new turn did not restore Stop")
	}
}

func TestInvalidSettingsAreNotOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	original := []byte(`{"hosts":`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PASEO_NATIVE_CONFIG", path)
	a := New("")
	if a.desk.notice == "" {
		t.Fatal("invalid settings were not reported")
	}
	a.persistDesktop()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatal("failed load was overwritten by default settings")
	}
}

func TestInvalidSettingsDisableEditsAndExplainRecovery(t *testing.T) {
	a := &App{desk: desktopState{loadFailed: true, notice: "Settings could not be loaded; original file preserved"}}
	if a.persistAvailable() {
		t.Fatal("invalid config allowed settings to appear persistent")
	}
	if got := a.settingsFailureMessage(); got == "" {
		t.Fatal("load failure has no user-visible recovery notice")
	}
}

func TestHostSaveDoesNotMutateInvalidSettings(t *testing.T) {
	a := &App{desk: desktopState{loadFailed: true}}
	a.desk.shell.HostEditor = desktopui.HostEditorState{Open: true}
	a.saveHost("host", "ws://127.0.0.1:7/ws", "", "")
	if len(a.desk.prefs.Hosts) != 0 {
		t.Fatal("host was added to an unwritable in-memory settings state")
	}
	if a.desk.shell.HostEditor.Error == "" {
		t.Fatal("host save failure was not visible")
	}
}

func TestHomeProjectRequestChoosesHostWithoutSavedConnection(t *testing.T) {
	a := &App{}
	tt := ui.NewTester(a.activeContent, 780, 760)
	if err := tt.Click("Add a project"); err != nil {
		t.Fatal(err)
	}
	if !a.desk.shell.HostEditor.Open {
		t.Fatal("home did not open the connection dialog")
	}
}

func TestConnectedHomeOpensProjectCreation(t *testing.T) {
	a := &App{connected: true}
	tt := ui.NewTester(a.activeContent, 780, 760)
	if err := tt.Click("Add a project"); err != nil {
		t.Fatal(err)
	}
	if !a.desk.navigation.open || a.desk.navigation.kind != "add project" {
		t.Fatal("home did not open the project creation workflow")
	}
}

func TestWorkspaceRestoreDoesNotPickUnrememberedWorkspace(t *testing.T) {
	a := &App{workspaces: []Workspace{{ID: "first"}}}
	a.desk.prefs.ActiveWorkspace = make(map[string]string)
	a.restoreWorkspace()
	if a.desk.workspace != "" || len(a.desk.prefs.ActiveWorkspace) != 0 {
		t.Fatalf("unremembered workspace replaced home: workspace=%q remembered=%v", a.desk.workspace, a.desk.prefs.ActiveWorkspace)
	}
}

func TestEnteringWorkspaceRestoresCollapsedNavigation(t *testing.T) {
	a := &App{}
	a.desk.prefs.ActiveWorkspace = make(map[string]string)
	a.desk.shell.NavHidden = true
	a.selectWorkspace("ws")
	if a.desk.shell.NavHidden {
		t.Fatal("workspace left host navigation inaccessible")
	}
}

func TestSidebarProjectRequestChoosesHostWhenDisconnected(t *testing.T) {
	a := &App{}
	var actions desktopui.ShellActions
	a.installNavigationActions(&actions)
	actions.AddProject()
	if !a.desk.shell.HostEditor.Open {
		t.Fatal("sidebar skipped connection prerequisite")
	}
}
