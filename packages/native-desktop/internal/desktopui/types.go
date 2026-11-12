package desktopui

import "github.com/egoist/mygo/ui"

type Host struct{ ID, Name, Status string }
type Project struct {
	ID, Name string
	Group    bool
}
type Workspace struct {
	ID, ProjectID, Project, Name, Branch, Status string
	Pinned, Unread                               bool
}
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
	NavWidth, ExplorerWidth  float32
	ExplorerOpen             bool
	NavHidden                bool
	ExpandedProjects         map[string]bool
	NavOverlay               bool
	DragTab                  string
	HostEditor               HostEditorState
	GroupMode, ProjectFilter string
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
	ActiveDestination              string
	ChromeHeight                   float32
	NavItems                       []NavPreference
	HelpItems                      []MenuAction
	SidebarHeader                  func(*ui.Context)
	UsageSummary                   string
	SidebarLoading                 bool
	SidebarEmpty                   string
	SidebarStatus                  func(*ui.Context)
	PreserveWorkspaceOrder         bool
}

type ShellActions struct {
	SelectHost              func(string)
	AddHost                 func()
	SelectWorkspace         func(string)
	ProjectSettings         func(string)
	OpenProjectFolder       func(string)
	OpenProjectInNewWindow  func(string)
	OpenWorkspaceFolder     func(string)
	RemoveProject           func(string)
	RenameProject           func(string)
	AddProject              func()
	CreateWorkspace         func(string)
	RenameWorkspace         func(string)
	CopyWorkspacePath       func(string)
	CopyWorkspaceBranch     func(string)
	MarkWorkspaceRead       func(string)
	MarkWorkspaceUnread     func(string)
	ToggleWorkspacePin      func(string)
	ArchiveWorkspace        func(string)
	OpenAgent               func(string)
	SelectTab               func(string)
	CloseTab                func(string)
	ReorderTab              func(string, string)
	SplitTab                func(id, axis string)
	MoveTabToPane           func(tabID, paneID string)
	NewAgent                func()
	NewWorkspace            func()
	OpenHistory             func()
	OpenSearch              func()
	OpenSchedules           func()
	ImportSession           func()
	OpenUsage               func()
	OpenHostSettings        func(string)
	ToggleNavigation        func()
	OpenSettings            func()
	ToggleExplorer          func()
	OpenPanel               func(string)
	ResizeNav               func(float32)
	ResizeExplorer          func(float32)
	SaveHost                func(name, url, password, credential string)
	CancelHost              func()
	ReorderSidebarProject   func(string, string)
	ReorderSidebarWorkspace func(string, string)
}

// Content functions build ordinary MyGo UI within the shell's panes.
type Content func(*ui.Context)

type NavPreference struct {
	ID     string
	Hidden bool
}
type MenuAction struct {
	Label string
	Run   func()
}
