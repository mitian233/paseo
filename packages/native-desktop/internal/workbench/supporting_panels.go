package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/client"
)

type panelCaller interface {
	Call(context.Context, string, json.RawMessage) (json.RawMessage, error)
}
type panelEntry struct{ Name, Path, Kind string }
type panelFile struct{ Path, Kind, Encoding, Content, ModifiedAt, Revision string }
type panelDocument struct {
	state                       editor.State
	saved, modifiedAt, revision string
}
type panelDiffLine struct{ Type, Content string }
type panelHunk struct {
	OldStart, OldCount, NewStart, NewCount int
	Lines                                  []panelDiffLine
}
type panelDiff struct {
	Path, OldPath, Status string
	IsNew, IsDeleted      bool
	Additions, Deletions  int
	Hunks                 []panelHunk
}
type panelPR struct {
	Number                                                                      int
	Title, URL, State, BaseRefName, HeadRefName, RepoOwner, RepoName, Mergeable string
	IsMerged, IsDraft                                                           bool
	Checks                                                                      []struct{ Name, Status, Duration string }
	Github                                                                      *panelForgeFacts
	ForgeSpecific                                                               *panelForgeFacts
}
type panelForgeFacts struct {
	Forge, MergeStateStatus, DetailedMergeStatus, MergeStatus               string
	Mergeable, HasMerged, HasConflicts, IsMergeQueueEnabled, IsInMergeQueue bool
	Repository                                                              struct{ MergeCommitAllowed, SquashMergeAllowed, RebaseMergeAllowed bool }
}

func (pr *panelPR) mergeMethods() []string {
	facts := pr.ForgeSpecific
	if facts == nil && pr.Github != nil {
		copy := *pr.Github
		copy.Forge = "github"
		facts = &copy
	}
	if facts == nil {
		return nil
	}
	switch facts.Forge {
	case "github":
		if (facts.MergeStateStatus != "CLEAN" && facts.MergeStateStatus != "HAS_HOOKS") || facts.IsMergeQueueEnabled || facts.IsInMergeQueue {
			return nil
		}
		var methods []string
		if facts.Repository.MergeCommitAllowed {
			methods = append(methods, "merge")
		}
		if facts.Repository.SquashMergeAllowed {
			methods = append(methods, "squash")
		}
		if facts.Repository.RebaseMergeAllowed {
			methods = append(methods, "rebase")
		}
		return methods
	case "gitlab":
		if facts.DetailedMergeStatus != "" {
			if facts.DetailedMergeStatus != "mergeable" {
				return nil
			}
		} else if facts.MergeStatus != "can_be_merged" || facts.HasConflicts {
			return nil
		}
	case "gitea":
		if !facts.Mergeable || facts.HasMerged {
			return nil
		}
	default:
		return nil
	}
	return []string{"merge", "squash", "rebase"}
}

type panelTimelineItem struct{ Author, Body, Kind, ReviewState string }
type supportingPanels struct {
	mu                                                    sync.Mutex
	ctx                                                   context.Context
	cancel                                                context.CancelFunc
	caller                                                panelCaller
	cwd, workspaceID                                      string
	notify                                                func()
	onOpenFile, onOpenDiff                                func(string)
	onEntryChanged                                        func(string, string)
	dirs                                                  map[string][]panelEntry
	expanded                                              map[string]*bool
	documents                                             map[string]*panelDocument
	loading                                               map[string]bool
	errs                                                  map[string]string
	diffs                                                 []panelDiff
	diffStates                                            map[string]*editor.DiffState
	pr                                                    *panelPR
	prEnabled                                             bool
	timeline                                              []panelTimelineItem
	currentFile                                           string
	entryDialogState, deleteDialog, mergeDialog           bool
	entryOp, entryPath, entryName, entryKind, mergeMethod string
	entryErr                                              string
	entryDialogOpen                                       bool
	entryBusy                                             bool
	confirmDelete                                         string
}

func newSupportingPanels(ctx context.Context, c *client.Client, workspaceID, cwd string, notify func()) *supportingPanels {
	return newSupportingPanelsWithCaller(ctx, c, workspaceID, cwd, notify)
}
func newSupportingPanelsWithCaller(ctx context.Context, c panelCaller, workspaceID, cwd string, notify func()) *supportingPanels {
	ctx, cancel := context.WithCancel(ctx)
	return &supportingPanels{ctx: ctx, cancel: cancel, caller: c, cwd: cwd, workspaceID: workspaceID, notify: notify, dirs: map[string][]panelEntry{}, expanded: map[string]*bool{}, documents: map[string]*panelDocument{}, loading: map[string]bool{}, errs: map[string]string{}, diffStates: map[string]*editor.DiffState{}}
}
func (p *supportingPanels) changed() {
	if p.notify != nil {
		p.notify()
	}
}
func (p *supportingPanels) call(ctx context.Context, typ string, req any, result any) error {
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}
	raw, err = p.caller.Call(ctx, typ, raw)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, result); err != nil {
		return fmt.Errorf("%s: %w", typ, err)
	}
	return nil
}
func (p *supportingPanels) mutateEntry(ctx context.Context, operation, target, name, kind string) error {
	if operation != "create" {
		p.mu.Lock()
		for file, doc := range p.documents {
			if (file == target || strings.HasPrefix(file, target+"/")) && doc.state.Buffer.String() != doc.saved {
				p.mu.Unlock()
				return errors.New("Save unsaved files before modifying this entry")
			}
		}
		p.mu.Unlock()
	}
	req := map[string]any{"cwd": p.cwd}
	var typ string
	switch operation {
	case "create":
		typ = "fs.entry.create.request"
		if target == "" {
			target = "."
		}
		req["parentPath"], req["name"], req["kind"] = target, name, kind
	case "rename":
		typ = "fs.entry.rename.request"
		req["path"], req["name"] = target, name
	case "delete":
		typ = "fs.entry.delete.request"
		req["path"] = target
	default:
		return errors.New("unknown file operation")
	}
	var result struct {
		Success     bool
		Error       *string
		RenamedPath *string
	}
	if err := p.call(ctx, typ, req, &result); err != nil {
		return err
	}
	if result.Error != nil {
		return errors.New(*result.Error)
	}
	if !result.Success {
		return errors.New("file operation failed")
	}
	if operation != "create" {
		newPath := ""
		if operation == "rename" {
			if result.RenamedPath == nil {
				return errors.New("rename response has no destination")
			}
			newPath = *result.RenamedPath
		}
		p.mu.Lock()
		for file, doc := range p.documents {
			if (file == target || strings.HasPrefix(file, target+"/")) && doc.state.Buffer.String() == doc.saved {
				delete(p.documents, file)
			}
		}
		for dir := range p.dirs {
			if dir == target || strings.HasPrefix(dir, target+"/") {
				delete(p.dirs, dir)
			}
		}
		callback := p.onEntryChanged
		p.mu.Unlock()
		if callback != nil {
			callback(target, newPath)
		}
	}
	return nil
}
func (p *supportingPanels) entryDialog(c *ui.Context) {
	if !p.entryDialogOpen {
		return
	}
	canceled := false
	modal := ui.Modal(c, &p.entryDialogOpen, func() {
		ui.Column(c).Width(400).Padding(20).Gap(10).Background(c.Theme().Background).Children(func() {
			ui.Text(c, strings.Title(p.entryOp)).Bold()
			if p.entryOp == "create" {
				ui.TextInput(c, &p.entryName).Label("Name")
				ui.Row(c).Gap(6).Children(func() {
					for _, kind := range []string{"file", "directory"} {
						if ui.Button(c, kind).Clicked() {
							p.entryKind = kind
						}
					}
				})
			} else {
				ui.TextInput(c, &p.entryName).Label("New name")
			}
			if p.entryErr != "" {
				ui.Text(c, p.entryErr).TextColor(c.Theme().Danger)
			}
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if ui.Button(c, "Cancel").Clicked() {
					p.entryDialogOpen = false
					canceled = true
				}
				if ui.PrimaryButton(c, "Save").Disabled(p.entryBusy).Clicked() {
					p.entryBusy = true
					op, target, name, kind := p.entryOp, p.entryPath, p.entryName, p.entryKind
					go func() {
						ctx, cancel := context.WithTimeout(p.ctx, 30*time.Second)
						defer cancel()
						err := p.mutateEntry(ctx, op, target, name, kind)
						p.mu.Lock()
						p.entryBusy = false
						if err != nil {
							p.entryErr = err.Error()
						} else {
							p.entryDialogOpen = false
						}
						p.mu.Unlock()
						p.changed()
					}()
				}
			})
		})
	})
	if canceled || modal.Dismissed() {
		p.entryDialogOpen = false
	}
}

func (p *supportingPanels) openEntryDialog(operation, target, kind string) {
	p.entryOp = operation
	p.entryPath = target
	p.entryName = ""
	p.entryKind = kind
	p.entryErr = ""
	p.entryBusy = false
	p.entryDialogOpen = true
}
func (p *supportingPanels) loadDir(ctx context.Context, path string) error {
	var response struct {
		Directory *struct{ Entries []panelEntry }
		Error     *string
	}
	requestPath := path
	if requestPath == "" {
		requestPath = "."
	}
	err := p.call(ctx, "file_explorer_request", map[string]any{"cwd": p.cwd, "path": requestPath, "mode": "list"}, &response)
	if err == nil && response.Error != nil {
		err = errors.New(*response.Error)
	}
	if err == nil && response.Directory == nil {
		err = errors.New("directory missing from response")
	}
	if err == nil {
		p.mu.Lock()
		p.dirs[path] = response.Directory.Entries
		p.mu.Unlock()
	}
	return err
}
func (p *supportingPanels) loadFile(ctx context.Context, path string) error {
	p.mu.Lock()
	existing := p.documents[path]
	p.mu.Unlock()
	if existing != nil {
		return nil
	}
	var response struct {
		File  *panelFile
		Error *string
	}
	err := p.call(ctx, "file_explorer_request", map[string]any{"cwd": p.cwd, "path": path, "mode": "file"}, &response)
	if err != nil {
		return err
	}
	if response.Error != nil {
		return errors.New(*response.Error)
	}
	if response.File == nil {
		return errors.New("file missing from response")
	}
	f := response.File
	if f.Kind != "text" || f.Encoding != "utf-8" {
		return fmt.Errorf("%s file cannot be edited", f.Kind)
	}
	buffer := editor.NewBuffer(f.Content)
	doc := &panelDocument{state: editor.State{Buffer: buffer}, saved: buffer.String(), modifiedAt: f.ModifiedAt, revision: f.Revision}
	p.mu.Lock()
	if p.documents[path] == nil {
		p.documents[path] = doc
	}
	p.mu.Unlock()
	return nil
}
func (p *supportingPanels) CanClose(path string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.documents[path]
	return d == nil || d.state.Buffer.String() == d.saved
}
func (p *supportingPanels) HasUnsavedWork() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, d := range p.documents {
		if d.state.Buffer.String() != d.saved {
			return true
		}
	}
	return false
}
func (p *supportingPanels) saveFile(ctx context.Context, path string) error {
	p.mu.Lock()
	d := p.documents[path]
	if d == nil {
		p.mu.Unlock()
		return errors.New("file is not open")
	}
	text, stamp, revision, ending := d.state.Buffer.String(), d.modifiedAt, d.revision, d.state.Buffer.LineEnding()
	p.mu.Unlock()
	content := text
	if ending == "\r\n" {
		content = strings.ReplaceAll(text, "\n", "\r\n")
	}
	req := map[string]any{"cwd": p.cwd, "path": path, "content": content, "expectedModifiedAt": stamp}
	if revision != "" {
		req["expectedRevision"] = revision
	}
	var response struct {
		Result struct{ Status, ModifiedAt, Revision, Error string }
	}
	if err := p.call(ctx, "fs.file.write.request", req, &response); err != nil {
		return err
	}
	switch response.Result.Status {
	case "conflict":
		return errors.New("file conflict: the file changed on disk; draft preserved")
	case "error":
		return errors.New(response.Result.Error)
	case "written":
		p.mu.Lock()
		d.saved, d.modifiedAt, d.revision = text, response.Result.ModifiedAt, response.Result.Revision
		p.mu.Unlock()
		return nil
	default:
		return errors.New("unexpected file write response")
	}
}
func (p *supportingPanels) Save(ctx context.Context) error {
	p.mu.Lock()
	path := p.currentFile
	p.mu.Unlock()
	if path == "" {
		return errors.New("no file selected")
	}
	return p.saveFile(ctx, path)
}
func (p *supportingPanels) loadChanges(ctx context.Context) error {
	var response struct {
		Files        []panelDiff
		Error        *struct{ Message string }
		DiffTooLarge bool
	}
	err := p.call(ctx, "checkout.diff.get.request", map[string]any{"cwd": p.cwd, "compare": map[string]string{"mode": "uncommitted"}}, &response)
	if err != nil {
		return err
	}
	if response.Error != nil {
		return errors.New(response.Error.Message)
	}
	if response.DiffTooLarge {
		return errors.New("diff is too large to display")
	}
	p.mu.Lock()
	p.diffs = response.Files
	p.diffStates = map[string]*editor.DiffState{}
	p.mu.Unlock()
	return nil
}
func (p *supportingPanels) loadPR(ctx context.Context) error {
	var response struct {
		Status                *panelPR
		GithubFeaturesEnabled bool
		Error                 *struct{ Message string }
	}
	if err := p.call(ctx, "checkout_pr_status_request", map[string]any{"cwd": p.cwd}, &response); err != nil {
		return err
	}
	if response.Error != nil {
		return errors.New(response.Error.Message)
	}
	p.mu.Lock()
	p.pr = response.Status
	p.prEnabled = response.GithubFeaturesEnabled
	p.timeline = nil
	p.mu.Unlock()
	if response.Status == nil || response.Status.Number == 0 || response.Status.RepoOwner == "" || response.Status.RepoName == "" {
		return nil
	}
	var timeline struct {
		Items []panelTimelineItem
		Error *struct{ Message, Kind string }
	}
	err := p.call(ctx, "pull_request_timeline_request", map[string]any{"cwd": p.cwd, "prNumber": response.Status.Number, "repoOwner": response.Status.RepoOwner, "repoName": response.Status.RepoName}, &timeline)
	if err != nil {
		return err
	}
	if timeline.Error != nil {
		return fmt.Errorf("timeline: %s", timeline.Error.Message)
	}
	p.mu.Lock()
	p.timeline = timeline.Items
	p.mu.Unlock()
	return nil
}
func (p *supportingPanels) mergePR(ctx context.Context, method string) error {
	var response struct {
		Success bool
		Error   *struct{ Message string }
	}
	if err := p.call(ctx, "checkout_pr_merge_request", map[string]any{"cwd": p.cwd, "mergeMethod": method}, &response); err != nil {
		return err
	}
	if response.Error != nil {
		return errors.New(response.Error.Message)
	}
	if !response.Success {
		return errors.New("pull request merge failed")
	}
	return p.loadPR(ctx)
}
func (p *supportingPanels) start(key string, fn func(context.Context) error) {
	p.mu.Lock()
	if p.loading[key] {
		p.mu.Unlock()
		return
	}
	p.loading[key] = true
	delete(p.errs, key)
	p.mu.Unlock()
	p.changed()
	go func() {
		err := fn(p.ctx)
		p.mu.Lock()
		p.loading[key] = false
		if err != nil && !errors.Is(err, context.Canceled) {
			p.errs[key] = err.Error()
		}
		p.mu.Unlock()
		p.changed()
	}()
}
func (p *supportingPanels) Open(kind, target string) {
	switch kind {
	case "files":
		p.start("dir:", func(ctx context.Context) error { return p.loadDir(ctx, "") })
	case "changes":
		p.start("changes", p.loadChanges)
	case "diff":
		p.start("changes", p.loadChanges)
	case "file":
		p.mu.Lock()
		p.currentFile = target
		p.mu.Unlock()
		p.start("file:"+target, func(ctx context.Context) error { return p.loadFile(ctx, target) })
	case "pull_request", "pr":
		p.start("pr", p.loadPR)
	}
}
func (p *supportingPanels) Close() { p.cancel() }
func (p *supportingPanels) renderTree(c *ui.Context, path string) {
	for _, entry := range p.dirs[path] {
		entry := entry
		if entry.Kind == "directory" {
			opened := p.expanded[entry.Path]
			if opened == nil {
				opened = new(bool)
				p.expanded[entry.Path] = opened
			}
			before := *opened
			ui.TreeItem(c, entry.Name, opened, func() { p.renderTree(c, entry.Path) })
			if !before && *opened {
				go p.start("dir:"+entry.Path, func(ctx context.Context) error { return p.loadDir(ctx, entry.Path) })
			}
			if msg := p.errs["dir:"+entry.Path]; msg != "" && *opened {
				ui.Text(c, msg)
			}
		} else if ui.TreeItem(c, entry.Name, nil, nil).Clicked() && p.onOpenFile != nil {
			go p.onOpenFile(entry.Path)
		}
	}
}
func (p *supportingPanels) diffState(path string) *editor.DiffState {
	if d := p.diffStates[path]; d != nil {
		return d
	}
	for _, f := range p.diffs {
		if f.Path != path {
			continue
		}
		patch := editor.FilePatch{OldName: f.Path, NewName: f.Path, Binary: f.Status == "binary"}
		if f.OldPath != "" {
			patch.OldName = f.OldPath
		}
		if f.IsNew {
			patch.OldName = ""
		}
		if f.IsDeleted {
			patch.NewName = ""
		}
		for _, h := range f.Hunks {
			ph := editor.PatchHunk{OldStart: h.OldStart, OldLines: h.OldCount, NewStart: h.NewStart, NewLines: h.NewCount}
			for _, l := range h.Lines {
				prefix := " "
				switch l.Type {
				case "add":
					prefix = "+"
				case "remove":
					prefix = "-"
				case "header":
					continue
				}
				ph.Lines = append(ph.Lines, prefix+l.Content)
			}
			patch.Hunks = append(patch.Hunks, ph)
		}
		d := &editor.DiffState{}
		d.SetPatch(patch)
		p.diffStates[path] = d
		return d
	}
	return nil
}
func (p *supportingPanels) View(c *ui.Context, kind, target string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ui.Column(c).Fill().Gap(6).Children(func() {
		key := kind
		if kind == "files" {
			key = "dir:"
		}
		if kind == "file" {
			key = "file:" + target
		}
		if kind == "diff" {
			key = "changes"
		}
		if kind == "pull_request" {
			key = "pr"
		}
		if p.loading[key] {
			ui.Text(c, "Loading…")
		}
		if msg := p.errs[key]; msg != "" {
			ui.Text(c, msg)
			if ui.Button(c, "Retry").Clicked() {
				go p.Open(kind, target)
			}
		}
		switch kind {
		case "files":
			ui.Row(c).Gap(6).Children(func() {
				ui.Text(c, "Files").Bold()
				ui.Spacer(c)
				if ui.Button(c, "New file").Clicked() {
					p.openEntryDialog("create", ".", "file")
				}
				if ui.Button(c, "New folder").Clicked() {
					p.openEntryDialog("create", ".", "directory")
				}
				if ui.Button(c, "Refresh").Clicked() {
					go p.Open("files", "")
				}
			})
			ui.Scroll(c).Grow(1).Children(func() { ui.Tree(c, func() { p.renderTree(c, "") }) })
			p.entryDialog(c)
			for _, entry := range p.dirs[""] {
				entry := entry
				if entry.Name == "" {
					continue
				}
				ui.Row(c.Key("entry:" + entry.Path)).Height(28).Gap(4).Children(func() {
					ui.Spacer(c)
					ui.Button(c, "Actions for "+entry.Name).PopoverMenuButton(func(m *ui.Menu) {
						if m.Item("Rename").Chosen() {
							p.openEntryDialog("rename", entry.Path, "")
						}
						if m.Item("Delete").Chosen() {
							p.confirmDelete = entry.Path
							p.deleteDialog = true
						}
					})
				})
			}
			if p.confirmDelete != "" {
				target := p.confirmDelete
				choice := ui.AlertDialog(c, &p.deleteDialog, "Delete entry", "Delete "+target+"?", "Cancel", "Delete")
				if choice == 1 {
					p.confirmDelete = ""
					go func() {
						ctx, cancel := context.WithTimeout(p.ctx, 30*time.Second)
						defer cancel()
						err := p.mutateEntry(ctx, "delete", target, "", "")
						p.mu.Lock()
						if err != nil {
							p.errs["dir:"] = err.Error()
						}
						p.mu.Unlock()
						p.changed()
					}()
				}
				if choice == 0 {
					p.confirmDelete = ""
				}
			}
		case "changes":
			ui.Row(c).Gap(6).Children(func() {
				ui.Text(c, "Changes").Bold()
				if ui.Button(c, "Refresh").Clicked() {
					go p.Open("changes", "")
				}
			})
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Tree(c, func() {
					for _, f := range p.diffs {
						f := f
						if ui.TreeItem(c, fmt.Sprintf("%s  +%d −%d", f.Path, f.Additions, f.Deletions), nil, nil).Clicked() && p.onOpenDiff != nil {
							go p.onOpenDiff(f.Path)
						}
					}
				})
			})
		case "file":
			if d := p.documents[target]; d != nil {
				ui.Row(c).Gap(6).Children(func() {
					ui.Text(c, target).Bold()
					if d.state.Buffer.String() != d.saved {
						ui.Text(c, "●")
					}
					if ui.Button(c, "Save").Clicked() {
						go p.start("file:"+target, func(ctx context.Context) error { return p.saveFile(ctx, target) })
					}
				})
				editor.View(c, &d.state).Grow(1)
			}
		case "diff":
			if d := p.diffState(target); d != nil {
				ui.Text(c, target).Bold()
				for _, f := range p.diffs {
					if f.Path == target && f.Status == "too_large" {
						ui.Text(c, "This file's diff is too large to display.")
					}
					if f.Path == target && f.Status == "binary" {
						ui.Text(c, "Binary file changed.")
					}
				}
				editor.DiffView(c, d).Grow(1)
			} else if !p.loading[key] && p.errs[key] == "" {
				ui.Text(c, "No changes for this file.")
			}
		case "pr", "pull_request":
			if p.pr == nil {
				if !p.loading[key] && p.errs[key] == "" {
					ui.Text(c, "No pull request for this workspace.")
				}
				return
			}
			pr := p.pr
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Column(c).Gap(6).Children(func() {
					ui.Text(c, fmt.Sprintf("#%d %s", pr.Number, pr.Title)).Bold()
					ui.Text(c, pr.State+"  "+pr.HeadRefName+" → "+pr.BaseRefName)
					ui.Text(c, pr.URL)
					if ui.Button(c, "Refresh").Clicked() {
						go p.Open("pr", "")
					}
					if p.prEnabled && strings.EqualFold(pr.State, "open") && !pr.IsDraft && !pr.IsMerged && pr.Mergeable == "MERGEABLE" && pr.Github != nil {
						for _, action := range []struct {
							label, method string
							allowed       bool
						}{{"Merge", "merge", pr.Github.Repository.MergeCommitAllowed}, {"Squash and merge", "squash", pr.Github.Repository.SquashMergeAllowed}, {"Rebase and merge", "rebase", pr.Github.Repository.RebaseMergeAllowed}} {
							action := action
							if action.allowed && ui.Button(c, action.label).Disabled(p.loading["pr"]).Clicked() {
								go p.start("pr", func(ctx context.Context) error { return p.mergePR(ctx, action.method) })
							}
						}
					}
					for _, check := range pr.Checks {
						ui.Text(c, check.Name+"  "+check.Status+"  "+check.Duration)
					}
					for _, item := range p.timeline {
						ui.Text(c, item.Author+"  "+item.Kind+"  "+item.ReviewState).Bold()
						ui.Text(c, item.Body)
					}
				})
			})
		}
	})
}
