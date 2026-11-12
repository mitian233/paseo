package workbench

import (
	"github.com/egoist/mygo/ui"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/workbenchstate"
	"strings"
	"testing"
)

func TestHostSettingsUsesSavedIdentityWithoutChangingConnection(t *testing.T) {
	a := &App{URL: "ws://active", connected: true}
	a.desk.prefs.ActiveHost = "active"
	a.desk.prefs.Hosts = []workbenchstate.Host{{ID: "active", Name: "Same name", URL: "ws://active"}, {ID: "other", Name: "Same name", URL: "ws://other"}}
	a.openHostSettings("other")
	if a.desk.hostSettings.host != "other" || a.desk.prefs.ActiveHost != "active" || a.URL != "ws://active" || !a.connected {
		t.Fatal("settings switched the connection")
	}
	a.editSettingsHost()
	if a.desk.editingHost != "other" || a.desk.shell.HostEditor.URL != "ws://other" {
		t.Fatal("edited another host sharing the same label")
	}
	a.openHostSettings("removed")
	if a.desk.hostSettings.err == "" {
		t.Fatal("removed host appeared editable")
	}
}
func TestFooterHelpLinksUseMyGoURLService(t *testing.T) {
	a := &App{}
	tt := ui.NewTester(func(c *ui.Context) { a.footerHelpDialog(c) }, 800, 600)
	for _, item := range a.footerHelpItems() {
		if item.URL == "" {
			continue
		}
		item.Action()
		tt.Frame()
		urls := tt.OpenedURLs()
		if len(urls) == 0 || urls[len(urls)-1] != item.URL {
			t.Fatalf("%s did not open its URL: %v", item.ID, urls)
		}
	}
}
func TestDiagnosticsExcludeSavedCredentialsAndURLSecrets(t *testing.T) {
	a := &App{URL: "ws://user:password@localhost/ws?token=secret", Password: "saved-secret"}
	a.desk.prefs.Hosts = []workbenchstate.Host{{ID: "a", Name: "A", URL: a.URL, HasSecret: true}}
	a.desk.credentials = map[string]credential{"a": {Password: "vault-secret"}}
	text := a.footerDiagnosticText()
	for _, secret := range []string{"password", "token=secret", "saved-secret", "vault-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("diagnostics exposed %s", secret)
		}
	}
	if !strings.Contains(text, "localhost") || !strings.Contains(text, "a") {
		t.Fatal("diagnostics lost host identity")
	}
}
