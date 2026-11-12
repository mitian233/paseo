package workbench

func workspaceSidebarStatus(workspace string, agents []Agent) (string, bool) {
	priority := 6
	status := ""
	unread := false
	for _, ag := range agents {
		if ag.WorkspaceID != workspace {
			continue
		}
		unread = unread || ag.RequiresAttention
		rank, label := 5, ""
		switch {
		case ag.PendingPermissionCount > 0 || ag.AttentionReason == "permission":
			rank, label = 0, "Needs input"
		case ag.Status == "error" || ag.AttentionReason == "error":
			rank, label = 1, "Failed"
		case ag.Status == "running":
			rank, label = 2, "Running"
		case ag.RequiresAttention:
			rank, label = 3, "Unread"
		}
		if rank < priority {
			priority, status = rank, label
		}
	}
	return status, unread
}
