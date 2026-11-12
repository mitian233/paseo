package workbench

import "testing"

func TestWorkspaceSidebarAttentionPrecedesRunningAndIgnoresOtherWorkspace(t *testing.T) {
	agents := []Agent{{WorkspaceID: "other", AttentionReason: "error"}, {WorkspaceID: "w", Status: "running"}, {WorkspaceID: "w", RequiresAttention: true, PendingPermissionCount: 1}}
	status, unread := workspaceSidebarStatus("w", agents)
	if status != "Needs input" || !unread {
		t.Fatalf("got %q %v", status, unread)
	}
	agents = agents[:2]
	status, unread = workspaceSidebarStatus("w", agents)
	if status != "Running" || unread {
		t.Fatalf("got %q %v", status, unread)
	}
}
