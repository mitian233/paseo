package workbench

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/timeline"
)

type detailSection struct{ label, text string }

// toolDetailSections presents all protocol fields, including unknown variants,
// as labelled values instead of placing raw JSON in the collapsed stream.
func toolDetailSections(detail, failure json.RawMessage) []detailSection {
	var fields map[string]json.RawMessage
	var sections []detailSection
	if json.Unmarshal(detail, &fields) == nil && fields != nil {
		labels := map[string]string{"command": "Command", "cwd": "Working directory", "output": "Output", "exitCode": "Exit code", "filePath": "File", "content": "Content", "oldString": "Before", "newString": "After", "unifiedDiff": "Diff", "query": "Query", "input": "Input", "result": "Result", "url": "URL", "text": "Text", "log": "Log"}
		order := []string{"command", "cwd", "filePath", "query", "url", "input", "oldString", "newString", "unifiedDiff", "content", "text", "output", "result", "log", "exitCode"}
		seen := make(map[string]bool, len(order)+1)
		seen["type"] = true
		add := func(key string) {
			seen[key] = true
			if value, ok := fields[key]; ok && string(value) != "null" {
				label := labels[key]
				if label == "" {
					label = key
				}
				sections = append(sections, detailSection{label, readableJSON(value)})
			}
		}
		for _, key := range order {
			add(key)
		}
		var rest []string
		for key := range fields {
			if !seen[key] {
				rest = append(rest, key)
			}
		}
		sort.Strings(rest)
		for _, key := range rest {
			add(key)
		}
	} else if len(detail) > 0 && string(detail) != "null" {
		sections = append(sections, detailSection{"Details", readableJSON(detail)})
	}
	if len(failure) > 0 && string(failure) != "null" {
		sections = append(sections, detailSection{"Error", readableJSON(failure)})
	}
	return sections
}

func readableJSON(raw json.RawMessage) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	if text, ok := value.(string); ok {
		return text
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(b)
}

func detailSectionsView(c *ui.Context, sections []detailSection) {
	for _, section := range sections {
		if section.text == "" {
			continue
		}
		ui.Column(c).Gap(3).Children(func() {
			ui.Text(c, section.label).Bold().TextColor(c.Theme().TextMuted)
			ui.Text(c, section.text).Selectable()
		})
	}
}

func composerView(c *ui.Context, draft *string, enabled, running, sending bool, send, stop func()) {
	ui.Column(c).Gap(6).Children(func() {
		ui.TextArea(c, draft).Label("Message agent").Placeholder("Message agent").MinHeight(72).MaxHeight(200).Disabled(!enabled).OnTextKey(func(k ui.TextKey) bool {
			if k.Key != ui.KeyEnter || k.Mods&ui.Shift != 0 {
				return false
			}
			if !enabled || sending {
				return true
			}
			*draft = k.Text()
			if strings.TrimSpace(*draft) != "" {
				send()
			}
			return true
		})
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "Enter to send · Shift+Enter for a new line").TextColor(c.Theme().TextMuted).Grow(1)
			if running {
				if ui.Button(c, "Stop").Disabled(!enabled).Clicked() {
					stop()
				}
			}
			if ui.PrimaryButton(c, "Send").Disabled(!enabled || sending || strings.TrimSpace(*draft) == "").Clicked() {
				send()
			}
		})
	})
}

func (a *App) conversationView(c *ui.Context) {
	ui.Column(c).Grow(1).Gap(8).Padding(12).Children(func() {
		if a.selected == "" {
			ui.Text(c, "Select an agent to view its conversation.").TextColor(c.Theme().TextMuted)
			return
		}
		if a.status != "" {
			ui.Text(c, a.status).TextColor(c.Theme().TextMuted)
		}
		a.list.FollowEnd = true
		rows := conversationRows(a.items, a.turnExpanded, a.agentRunning)
		count := len(rows)
		if len(a.permissions.cards) > 0 {
			count++
		}
		a.list.Key = func(i int) any {
			if i == len(rows) {
				return "pending-permissions"
			}
			return rows[i].item.ID
		}
		ui.List(c, &a.list, count, func(i int) {
			if i == len(rows) {
				ui.Column(c).Gap(8).Disabled(!a.canMutate()).Children(func() {
					a.permissions.view(c, func(id string, response permissionResponse) {
						if !a.canMutate() {
							return
						}
						gen, selection := a.generation, a.selection
						a.permissions.submit(a.selectionCtx, a.client, a.selected, id, response, func(fn func()) { a.selectionUpdate(gen, selection, fn) })
					})
				})
				return
			}
			row := rows[i]
			ui.Column(c).Gap(5).Children(func() {
				a.conversationItem(c, row.item)
				if row.foldedCount > 0 {
					open := a.turnExpanded[row.item.ID]
					marker := "▸ "
					if open {
						marker = "▾ "
					}
					if ui.Button(c, fmt.Sprintf("%s%d steps", marker, row.foldedCount)).Clicked() {
						if a.turnExpanded == nil {
							a.turnExpanded = make(map[string]bool)
						}
						a.turnExpanded[row.item.ID] = !open
					}
				}
				if row.turnComplete {
					ui.Row(c).Gap(8).Children(func() {
						if row.turnText != "" && ui.Button(c, "Copy turn").Clicked() {
							copyMessage(c, row.turnText)
						}
						if a.forkAvailable && a.canMutate() && (row.item.Epoch != "" || row.item.MessageID != "") {
							ui.PopoverMenuButton(c, "Fork", func(m *ui.Menu) {
								if m.Item("Fork to tab").Chosen() {
									a.forkFrom(row.item, "tab")
								}
								if m.Item("Fork to workspace").Chosen() {
									a.forkFrom(row.item, "workspace")
								}
							}).Disabled(a.conversationActionBusy)
						}
						if timing := turnTiming(row.startedAt, row.completedAt); timing != "" {
							ui.Text(c, timing).TextColor(c.Theme().TextMuted)
						}
					})
				}
			})
		}).Grow(1).Gap(12)
		composerView(c, &a.draft, a.canMutate(), a.agentRunning, a.sending, a.send, a.stopTurn)
	})
}

func (a *App) conversationItem(c *ui.Context, item timeline.Item) {
	c = c.Key(item.ID)
	ui.Column(c).Gap(5).Children(func() {
		switch item.Kind {
		case "tool_call", "reasoning":
			label := item.Name
			if item.Kind == "reasoning" {
				label = "Thinking"
			}
			if label == "" {
				label = "Tool"
			}
			if item.Kind == "tool_call" {
				sections := toolDetailSections(item.Detail, nil)
				if len(sections) > 0 {
					summary := strings.SplitN(sections[0].text, "\n", 2)[0]
					if runes := []rune(summary); len(runes) > 100 {
						summary = string(runes[:100]) + "…"
					}
					if summary != "" {
						label += " · " + summary
					}
				}
			}
			if item.Status != "" {
				label += " · " + item.Status
			}
			open := a.toolExpanded[item.ID]
			marker := "▸ "
			if open {
				marker = "▾ "
			}
			if ui.Button(c, marker+label).Clicked() {
				if a.toolExpanded == nil {
					a.toolExpanded = make(map[string]bool)
				}
				a.toolExpanded[item.ID] = !open
				open = !open
			}
			if open {
				if item.Kind == "reasoning" {
					markdown.View(c, item.Text, a.markdownOptions(item.ID))
				} else {
					detailSectionsView(c, toolDetailSections(item.Detail, item.Error))
				}
			}
		case "user_message":
			ui.Row(c).Justify(ui.End).Children(func() {
				ui.Column(c).MaxWidth(680).Padding(10, 14).Radius(12).Background(c.Theme().Surface).Gap(4).Children(func() {
					ui.Text(c, "You").Bold().TextColor(c.Theme().TextMuted)
					markdown.View(c, item.Text, a.markdownOptions(item.ID))
					if stamp := formatMessageTime(item.Timestamp); stamp != "" {
						ui.Text(c, stamp).TextColor(c.Theme().TextMuted)
					}
					if item.MessageID != "" && a.canMutate() && (a.rewindCapabilities.Conversation || a.rewindCapabilities.Files || a.rewindCapabilities.Both) {
						ui.PopoverMenuButton(c, "Rewind", func(m *ui.Menu) {
							m.Item("Rewind may discard later changes").Disabled(true)
							m.Separator()
							if a.rewindCapabilities.Conversation && m.Item("Rewind conversation").Chosen() {
								a.rewindFrom(item, "conversation")
							}
							if a.rewindCapabilities.Files && m.Item("Rewind files").Chosen() {
								a.rewindFrom(item, "files")
							}
							if a.rewindCapabilities.Both && m.Item("Rewind conversation and files").Chosen() {
								a.rewindFrom(item, "both")
							}
						}).Disabled(a.conversationActionBusy)
					}
					if ui.Button(c, "Copy message").Clicked() {
						copyMessage(c, item.Text)
					}
				})
			})
		case "assistant_message":
			markdown.View(c, item.Text, a.markdownOptions(item.ID))
			if ui.Button(c, "Copy message").Clicked() {
				copyMessage(c, item.Text)
			}
		default:
			ui.Text(c, roleLabel(item.Kind)).Bold().TextColor(c.Theme().TextMuted)
			markdown.View(c, item.Text, a.markdownOptions(item.ID))
		}
	})
}

type conversationRow struct {
	item         timeline.Item
	foldedCount  int
	turnComplete bool
	turnText     string
	startedAt    string
	completedAt  string
}

func turnTrigger(item timeline.Item) bool {
	if item.Kind == "user_message" {
		return true
	}
	if item.Kind != "tool_call" {
		return false
	}
	if item.Name == "heartbeat" {
		return true
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(item.Raw, &raw) != nil {
		return false
	}
	_, ok := raw["agentMessage"]
	return ok
}

func conversationRows(items []timeline.Item, expanded map[string]bool, running bool) []conversationRow {
	rows := make([]conversationRow, 0, len(items))
	for start := 0; start < len(items); {
		end := start + 1
		for end < len(items) {
			previous, next := items[end-1], items[end]
			if previous.TurnID != "" && next.TurnID != "" {
				if previous.TurnID != next.TurnID {
					break
				}
			} else if turnTrigger(next) {
				break
			}
			end++
		}
		lastReply := -1
		for i := end - 1; i >= start; i-- {
			if items[i].Kind == "assistant_message" {
				lastReply = i
				break
			}
		}
		closed := end < len(items) || !running
		fold := closed && lastReply > start+1 && (items[start].Kind == "user_message" || items[start].Kind == "tool_call")
		work := 0
		if fold {
			for i := start + 1; i < lastReply; i++ {
				if items[i].Kind == "tool_call" || items[i].Kind == "reasoning" || items[i].Kind == "assistant_message" {
					work++
				}
			}
			fold = work > 0
		}
		var replyParts []string
		for i := start; i < end; i++ {
			if items[i].Kind == "assistant_message" && items[i].Text != "" {
				replyParts = append(replyParts, items[i].Text)
			}
		}
		for i := start; i < end; i++ {
			if fold && !expanded[items[start].ID] && i > start && i < lastReply && !turnTrigger(items[i]) && items[i].Kind != "compaction" {
				continue
			}
			r := conversationRow{item: items[i]}
			if i == start && fold {
				r.foldedCount = work
			}
			if closed && i == lastReply {
				r.turnComplete = true
				r.turnText = strings.Join(replyParts, "\n\n")
				if turnTrigger(items[start]) {
					r.startedAt = items[start].Timestamp
				}
				r.completedAt = items[end-1].EndTimestamp
				if r.completedAt == "" {
					r.completedAt = items[end-1].Timestamp
				}
			}
			rows = append(rows, r)
		}
		start = end
	}
	return rows
}

func formatMessageTime(value string) string {
	stamp, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return ""
	}
	return stamp.Local().Format("Jan 2, 15:04")
}

func turnTiming(start, end string) string {
	stamp := formatMessageTime(end)
	a, errA := time.Parse(time.RFC3339Nano, start)
	b, errB := time.Parse(time.RFC3339Nano, end)
	if errA == nil && errB == nil && !b.Before(a) && b.Sub(a) >= time.Second {
		elapsed := b.Sub(a).Round(time.Second).String()
		if stamp != "" {
			return elapsed + " · " + stamp
		}
		return elapsed
	}
	return stamp
}

func copyMessage(c *ui.Context, text string) {
	doc := parser.Parse(text)
	c.Services().WriteClipboardData(transfer.New(transfer.NewItem(
		transfer.Bytes(transfer.Text, []byte(text)),
		transfer.Bytes(transfer.Markdown, []byte(text)),
		transfer.Bytes(transfer.HTML, []byte(`<meta charset="utf-8">`+doc.HTML())),
	)))
}
