package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/desktopui"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/workbenchstate"
)

type footerHelpState struct {
	open                  bool
	kind, pendingURL, err string
}
type footerHelpItem struct {
	ID, Label, URL string
	Action         func()
}
type hostSettingsState struct {
	host, err string
	loading   bool
	epoch     uint64
	info      json.RawMessage
}

// Menu actions queue browser requests until the next UI frame, so every link
// uses MyGo's OpenURL service and receives its real outcome.
func (a *App) footerHelpItems() []footerHelpItem {
	items := []footerHelpItem{
		{ID: "shortcuts", Label: "Keyboard shortcuts"},
		{ID: "changelog", Label: "What's new", URL: "https://paseo.sh/changelog"},
		{ID: "diagnostics", Label: "App diagnostics"},
		{ID: "discord", Label: "Discord", URL: "https://discord.gg/jz8T2uahpH"},
		{ID: "github", Label: "GitHub issue", URL: "https://github.com/getpaseo/paseo/issues/new"},
	}
	for i := range items {
		id, address := items[i].ID, items[i].URL
		items[i].Action = func() {
			if address != "" {
				a.desk.footerHelp.pendingURL = address
				a.desk.footerHelp.err = ""
				return
			}
			a.desk.footerHelp = footerHelpState{open: true, kind: id}
		}
	}
	return items
}
func (a *App) footerHelpDialog(c *ui.Context) {
	s := &a.desk.footerHelp
	if address := s.pendingURL; address != "" {
		s.pendingURL = ""
		c.OpenURLThen(address, func(err error) {
			if err != nil {
				a.desk.footerHelp.open = true
				a.desk.footerHelp.kind = "link-error"
				a.desk.footerHelp.err = err.Error()
			}
		})
	}
	if !s.open {
		return
	}
	ui.Modal(c, &s.open, func() {
		ui.Column(c).Width(620).Padding(20).Gap(12).Children(func() {
			switch s.kind {
			case "shortcuts":
				ui.Text(c, "Keyboard shortcuts").Bold()
				for _, row := range []struct{ key, action string }{{"Enter", "Send message / execute selected command"}, {"Shift+Enter", "Insert a new line in the message"}, {"↑ / ↓", "Select a command center result"}, {"Escape", "Close command center or dismiss a dialog"}, {"Tab / Shift+Tab", "Move focus forward / backward"}} {
					ui.Row(c).Gap(12).Children(func() { ui.Text(c, row.key).Width(160); ui.Text(c, row.action) })
				}
			case "diagnostics":
				ui.Text(c, "App diagnostics").Bold()
				ui.Scroll(c).Height(340).Children(func() { ui.Text(c, a.footerDiagnosticText()) })
				if ui.Button(c, "Copy diagnostics").Clicked() {
					c.WriteClipboard(a.footerDiagnosticText())
				}
			default:
				ui.Text(c, "Could not open link").Bold()
				ui.Text(c, s.err).TextColor(c.Theme().Danger)
			}
			if ui.Button(c, "Close").Clicked() {
				s.open = false
			}
		})
	})
}
func footerVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "development"
}
func safeHostAddress(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "Invalid address"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
func (a *App) footerDiagnosticText() string {
	var text strings.Builder
	fmt.Fprintf(&text, "Paseo native %s\nPlatform: %s/%s\nGo: %s\n", footerVersion(), runtime.GOOS, runtime.GOARCH, runtime.Version())
	for _, h := range a.desk.prefs.Hosts {
		status := "Not connected"
		version := ""
		if h.ID == a.desk.prefs.ActiveHost {
			if a.connected {
				status = "Connected"
			} else if a.connecting {
				status = "Connecting"
			}
			if a.client != nil {
				version = hostDaemonVersion(a.client.ServerInfo())
			}
		}
		fmt.Fprintf(&text, "\n%s [%s]\nConnection: %s\nStatus: %s\n", h.Name, h.ID, safeHostAddress(h.URL), status)
		if version != "" {
			fmt.Fprintf(&text, "Daemon: %s\n", version)
		}
	}
	return text.String()
}
func hostDaemonVersion(info json.RawMessage) string {
	var v struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(info, &v)
	return v.Version
}
func (a *App) savedSettingsHost() (workbenchstate.Host, bool) {
	for _, h := range a.desk.prefs.Hosts {
		if h.ID == a.desk.hostSettings.host {
			return h, true
		}
	}
	return workbenchstate.Host{}, false
}
func (a *App) openHostSettings(id string) {
	epoch := a.desk.hostSettings.epoch + 1
	a.desk.hostSettings = hostSettingsState{host: id, epoch: epoch}
	if _, ok := a.savedSettingsHost(); !ok {
		a.desk.hostSettings.err = "This host is no longer saved."
	}
	if id == a.desk.prefs.ActiveHost && a.client != nil {
		a.desk.hostSettings.info = a.client.ServerInfo()
	}
	a.showDestination("host-settings")
}
func (a *App) editSettingsHost() {
	h, ok := a.savedSettingsHost()
	if !ok {
		a.desk.hostSettings.err = "This host is no longer saved."
		return
	}
	a.desk.editingHost = h.ID
	cr := a.desk.credentials[h.ID]
	if h.HasSecret && cr.Password == "" && cr.LocalCredential == "" {
		data, err := mygo.Secrets.Service("paseo-native").Get(h.ID)
		if err != nil {
			a.desk.hostSettings.err = "Could not read this host's credentials: " + err.Error()
			return
		}
		if err = json.Unmarshal(data, &cr); err != nil {
			a.desk.hostSettings.err = "Could not read this host's credentials: " + err.Error()
			return
		}
	}
	// The existing editor's Save action uses this ID, preserving host identity.
	a.desk.shell.HostEditor = desktopui.HostEditorState{Open: true, Name: h.Name, URL: h.URL, Password: cr.Password, Credential: cr.LocalCredential}
}
func (a *App) refreshHostSettings() {
	s := &a.desk.hostSettings
	if s.loading {
		return
	}
	s.loading = true
	s.err = ""
	s.epoch++
	epoch, host, gen := s.epoch, s.host, a.generation
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		sources, cleanup := a.navigationSources(ctx)
		defer cleanup()
		var info json.RawMessage
		message := "This host is no longer saved."
		for _, source := range sources {
			if source.ID == host {
				if source.Err != nil {
					message = source.Err.Error()
				} else {
					info = source.Info
					message = ""
				}
				break
			}
		}
		a.update(gen, func() {
			s := &a.desk.hostSettings
			if s.host != host || s.epoch != epoch || a.desk.destination != "host-settings" {
				return
			}
			s.loading = false
			s.err = message
			s.info = info
		})
	}()
}
func (a *App) hostSettingsView(c *ui.Context) {
	s := &a.desk.hostSettings
	ui.Column(c).Fill().Padding(24).Gap(12).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "Host settings").Bold().Grow(1)
			if ui.Button(c, "Back").Clicked() {
				s.epoch++
				s.loading = false
				a.showDestination("home")
			}
		})
		h, ok := a.savedSettingsHost()
		if !ok {
			ui.Text(c, "This host is no longer saved.").TextColor(c.Theme().Danger)
			return
		}
		ui.Text(c, h.Name).Bold()
		ui.Text(c, "Host ID: "+h.ID).TextColor(c.Theme().TextMuted)
		ui.Text(c, "Connection: "+safeHostAddress(h.URL))
		status := "Not connected"
		if h.ID == a.desk.prefs.ActiveHost {
			if a.connected {
				status = "Connected"
			} else if a.connecting {
				status = "Connecting…"
			} else {
				status = "Offline"
			}
		}
		ui.Text(c, status)
		if version := hostDaemonVersion(s.info); version != "" {
			ui.Text(c, "Daemon version: "+version)
		}
		if h.HasSecret {
			ui.Text(c, "Credentials are stored in the system credential store.").TextColor(c.Theme().TextMuted)
		}
		if s.err != "" {
			ui.Text(c, s.err).TextColor(c.Theme().Danger)
		}
		if s.loading {
			ui.Text(c, "Reading host details…")
		}
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c, "Edit connection").Disabled(!a.persistAvailable()).Clicked() {
				a.editSettingsHost()
			}
			label := "Connect"
			if h.ID == a.desk.prefs.ActiveHost && a.connected {
				label = "Reconnect"
			}
			if ui.Button(c, label).Disabled(a.connecting).Clicked() {
				if h.ID == a.desk.prefs.ActiveHost {
					a.forceSelectHost(h.ID)
				} else {
					a.selectHost(h.ID)
				}
			}
			if ui.Button(c, "Refresh details").Disabled(s.loading).Clicked() {
				a.refreshHostSettings()
			}
			if ui.Button(c, "Usage").Clicked() {
				a.desk.prefs.UsageHost = h.ID
				a.persistSoon()
				a.openUsage()
			}
		})
	})
}
