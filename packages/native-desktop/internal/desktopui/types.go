package desktopui

import "github.com/egoist/mygo/ui"

type Host struct{ ID, Name, Status string }
type Project struct{ ID, Name string }
type Workspace struct{ ID, ProjectID, Project, Name string }
type Agent struct{ ID, WorkspaceID, Title, Status string }
type Tab struct {
	ID, Title, Kind string
	Active, Dirty   bool
}

type HostEditorState struct {
	Name, URL, Password, Credential string
	Open, Busy                      bool
	Error                           string
}

type ShellState struct {
	NavWidth, ExplorerWidth float32
	ExplorerOpen            bool
	NavHidden               bool
	ExpandedProjects        map[string]bool
	DragTab                 string
	HostEditor              HostEditorState
}

type ShellProps struct {
	Theme                          string
	SystemDark                     bool
	Width                          float32
	Hosts                          []Host
	ActiveHost                     string
	Workspaces                     []Workspace
	Projects                       []Project
	Agents                         []Agent
	ActiveWorkspace                string
	Tabs                           []Tab
	HideTabStrip                   bool
	Home                           bool
	PaneID                         string
	WorkspaceTitle, Branch, Status string
}

type ShellActions struct {
	SelectHost             func(string)
	AddHost                func()
	SelectWorkspace        func(string)
	ProjectSettings        func(string)
	OpenProjectFolder      func(string)
	OpenProjectInNewWindow func(string)
	OpenWorkspaceFolder    func(string)
	RemoveProject          func(string)
	RenameProject          func(string)
	AddProject             func()
	CreateWorkspace        func(string)
	RenameWorkspace        func(string)
	CopyWorkspacePath      func(string)
	CopyWorkspaceBranch    func(string)
	MarkWorkspaceRead      func(string)
	MarkWorkspaceUnread    func(string)
	ToggleWorkspacePin     func(string)
	ArchiveWorkspace       func(string)
	OpenAgent              func(string)
	SelectTab              func(string)
	CloseTab               func(string)
	ReorderTab             func(string, string)
	SplitTab               func(id, axis string)
	MoveTabToPane          func(tabID, paneID string)
	NewAgent               func()
	NewWorkspace           func()
	OpenHistory            func()
	OpenSearch             func()
	OpenSchedules          func()
	OpenSettings           func()
	ToggleExplorer         func()
	OpenPanel              func(string)
	ResizeNav              func(float32)
	ResizeExplorer         func(float32)
	SaveHost               func(name, url, password, credential string)
	CancelHost             func()
}

// Content functions build ordinary MyGo UI within the shell's panes.
type Content func(*ui.Context)
