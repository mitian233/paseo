package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/client"
)

type usageWindow struct {
	ID           string   `json:"id"`
	Label        string   `json:"label"`
	Summary      bool     `json:"summary"`
	UsedPct      *float64 `json:"usedPct"`
	RemainingPct *float64 `json:"remainingPct"`
	ResetsAt     string   `json:"resetsAt"`
	RunsOutAt    string   `json:"runsOutAt"`
	ShortfallPct *float64 `json:"shortfallPct"`
}
type usageBalance struct {
	ID        string   `json:"id"`
	Label     string   `json:"label"`
	Used      *float64 `json:"used"`
	Remaining *float64 `json:"remaining"`
	Limit     *float64 `json:"limit"`
	Unit      string   `json:"unit"`
	ResetsAt  string   `json:"resetsAt"`
}
type usageDetail struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Value string `json:"value"`
}
type usageReport struct {
	Status    string         `json:"status"`
	PlanLabel string         `json:"planLabel"`
	Windows   []usageWindow  `json:"windows"`
	Balances  []usageBalance `json:"balances"`
	Details   []usageDetail  `json:"details"`
	Error     string         `json:"error"`
	Problem   struct {
		Kind        string `json:"kind"`
		Detail      string `json:"detail"`
		ExpiresAt   string `json:"expiresAt"`
		RefreshedBy string `json:"refreshedBy"`
		Status      int    `json:"status"`
	} `json:"problem"`
}
type usageEntry struct {
	ID          string `json:"id"`
	SourceID    string `json:"sourceId"`
	SourceLabel string `json:"sourceLabel"`
	Account     struct {
		Label string `json:"label"`
	} `json:"account"`
	FetchedAt      string      `json:"fetchedAt"`
	SuccessfulAt   string      `json:"successfulAt"`
	Report         usageReport `json:"report"`
	LastSuccessful *struct {
		FetchedAt string      `json:"fetchedAt"`
		Report    usageReport `json:"report"`
	} `json:"lastSuccessful"`
	LoginErrors []struct {
		Harness string      `json:"harness"`
		Report  usageReport `json:"report"`
	} `json:"loginErrors"`
}
type usageState struct {
	open, loading, options, settingSupported, enabled, saving, uncertain, summary bool
	host, err                                                                     string
	epoch, revision                                                               uint64
	reports                                                                       []usageEntry
	cancel                                                                        context.CancelFunc
	summaryAt                                                                     time.Time
}

func (s *usageState) accepts(epoch, revision uint64, host string) bool {
	return (s.open || s.summary) && s.epoch == epoch && s.revision == revision && s.host == host
}
func (e usageEntry) content() (usageReport, string, bool) {
	if e.Report.Status == "available" {
		at := e.SuccessfulAt
		if at == "" {
			at = e.FetchedAt
		}
		return e.Report, at, false
	}
	if e.LastSuccessful != nil {
		return e.LastSuccessful.Report, e.LastSuccessful.FetchedAt, true
	}
	return usageReport{}, "", false
}
func usageProblem(r usageReport) string {
	if r.Status == "error" {
		return r.Error
	}
	switch r.Problem.Kind {
	case "expired":
		return "Login expired at " + r.Problem.ExpiresAt + ". Sign in again" + usageRefresher(r.Problem.RefreshedBy) + "."
	case "rejected":
		return fmt.Sprintf("Usage request rejected (%d). Sign in again%s.", r.Problem.Status, usageRefresher(r.Problem.RefreshedBy))
	case "no_quota":
		if r.Problem.Detail != "" {
			return r.Problem.Detail
		}
		return "No usage quota is available for this account."
	}
	return "Usage is unavailable."
}
func usageRefresher(s string) string {
	if s != "" {
		return " with " + s
	}
	return ""
}
func (e usageEntry) messages() []string {
	if e.Report.Status == "available" {
		return nil
	}
	var result []string
	for _, login := range e.LoginErrors {
		result = append(result, login.Harness+": "+usageProblem(login.Report))
	}
	if len(result) == 0 {
		result = append(result, usageProblem(e.Report))
	}
	return result
}
func usagePercent(w usageWindow, remaining bool) string {
	var used float64
	if w.UsedPct != nil {
		used = *w.UsedPct
	} else if w.RemainingPct != nil {
		used = 100 - *w.RemainingPct
	} else {
		return "No usage value"
	}
	if math.IsNaN(used) || math.IsInf(used, 0) {
		return "No usage value"
	}
	used = math.Max(0, math.Min(100, used))
	if remaining {
		return fmt.Sprintf("%.0f%% remaining", 100-used)
	}
	return fmt.Sprintf("%.0f%% used", used)
}
func usagePinKey(report, window string) string { return report + "\x00" + window }
func usageSummary(entries []usageEntry, pins map[string]bool, remaining bool) string {
	var rows []string
	for _, e := range entries {
		if e.Report.Status != "available" {
			continue
		}
		for _, w := range e.Report.Windows {
			if !usageWindowPinned(entries, pins, e, w) || usagePercent(w, remaining) == "No usage value" {
				continue
			}
			label := e.SourceLabel
			if e.Account.Label != "" {
				label += " · " + e.Account.Label
			}
			rows = append(rows, label+" "+w.Label+" "+usagePercent(w, remaining))
		}
	}
	return strings.Join(rows, " · ")
}
func usageSource(e usageEntry) string {
	if e.SourceID != "" {
		return e.SourceID
	}
	return e.ID
}
func usageWindowPinned(entries []usageEntry, pins map[string]bool, entry usageEntry, w usageWindow) bool {
	if pins != nil {
		return pins[usagePinKey(usageSource(entry), w.ID)]
	}
	for _, e := range entries {
		if usageSource(e) != usageSource(entry) || e.Report.Status != "available" {
			continue
		}
		marked := false
		first := ""
		for _, candidate := range e.Report.Windows {
			if usagePercent(candidate, false) == "No usage value" {
				continue
			}
			if first == "" {
				first = candidate.ID
			}
			if candidate.Summary {
				marked = true
				if candidate.ID == w.ID {
					return true
				}
			}
		}
		if !marked && first == w.ID {
			return true
		}
	}
	return false
}
func customUsagePins(entries []usageEntry) map[string]bool {
	// The sentinel preserves an explicitly empty selection through omitempty.
	pins := map[string]bool{"": false}
	for _, e := range entries {
		for _, w := range e.Report.Windows {
			if usageWindowPinned(entries, nil, e, w) {
				pins[usagePinKey(usageSource(e), w.ID)] = true
			}
		}
	}
	return pins
}

type usageReportCaller interface {
	ListUsageReports(context.Context, bool, []string, func(json.RawMessage)) ([]json.RawMessage, error)
}

func usageFeatures(info json.RawMessage) (modern, legacy, setting bool) {
	var v struct {
		Features struct {
			UsageSources      bool `json:"usageSources"`
			ProviderUsageList bool `json:"providerUsageList"`
			UsageSetting      bool `json:"usageSetting"`
		} `json:"features"`
	}
	if json.Unmarshal(info, &v) != nil {
		return
	}
	return v.Features.UsageSources, v.Features.ProviderUsageList, v.Features.UsageSetting
}
func decodeUsageEntry(raw json.RawMessage) (usageEntry, error) {
	var e usageEntry
	if err := json.Unmarshal(raw, &e); err != nil {
		return e, err
	}
	if e.ID == "" || e.SourceLabel == "" || (e.Report.Status != "available" && e.Report.Status != "error" && e.Report.Status != "unavailable") {
		return e, errors.New("Host sent an incomplete usage report")
	}
	return e, nil
}
func loadUsageEntries(ctx context.Context, c caller, info json.RawMessage, force bool, ids []string, onReport func(usageEntry)) ([]usageEntry, error) {
	modern, legacy, _ := usageFeatures(info)
	if modern {
		stream, ok := c.(usageReportCaller)
		if !ok {
			return nil, errors.New("This connection cannot read usage reports")
		}
		var decodeErr error
		raws, err := stream.ListUsageReports(ctx, force, ids, func(raw json.RawMessage) {
			e, eerr := decodeUsageEntry(raw)
			if eerr != nil {
				decodeErr = eerr
				return
			}
			if onReport != nil {
				onReport(e)
			}
		})
		entries := make([]usageEntry, 0, len(raws))
		for _, raw := range raws {
			e, eerr := decodeUsageEntry(raw)
			if eerr != nil {
				decodeErr = eerr
				continue
			}
			entries = append(entries, e)
		}
		if err != nil {
			return entries, err
		}
		return entries, decodeErr
	}
	if !legacy {
		return nil, errors.New("Update the host to see usage.")
	}
	raw, err := c.Call(ctx, "provider.usage.list.request", json.RawMessage(`{}`))
	if err != nil {
		return nil, err
	}
	var response struct {
		FetchedAt string `json:"fetchedAt"`
		Providers *[]struct {
			ProviderID  string `json:"providerId"`
			DisplayName string `json:"displayName"`
			FetchedAt   string `json:"fetchedAt"`
			usageReport
		} `json:"providers"`
	}
	if err = json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	if response.Providers == nil {
		return nil, errors.New("Host usage response is missing providers")
	}
	entries := make([]usageEntry, 0, len(*response.Providers))
	for _, p := range *response.Providers {
		if p.ProviderID == "" || p.DisplayName == "" || (p.Status != "available" && p.Status != "error" && p.Status != "unavailable") {
			return nil, errors.New("Host sent an incomplete usage account")
		}
		if len(ids) > 0 {
			found := false
			for _, id := range ids {
				if id == p.ProviderID {
					found = true
				}
			}
			if !found {
				continue
			}
		}
		e := usageEntry{ID: p.ProviderID, SourceID: p.ProviderID, SourceLabel: p.DisplayName, FetchedAt: p.FetchedAt, Report: p.usageReport}
		if e.FetchedAt == "" {
			e.FetchedAt = response.FetchedAt
		}
		if e.Report.Status == "unavailable" {
			e.Report.Problem.Kind = "no_quota"
			e.Report.Problem.Detail = e.Report.Error
		}
		entries = append(entries, e)
		if onReport != nil {
			onReport(e)
		}
	}
	return entries, nil
}
func parseUsageEnabled(raw json.RawMessage) (bool, error) {
	var p struct {
		Config *struct {
			Usage struct {
				Enabled *bool `json:"enabled"`
			} `json:"usage"`
		} `json:"config"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return false, err
	}
	if p.Config == nil {
		return false, errors.New("Host response is missing usage configuration")
	}
	return p.Config.Usage.Enabled == nil || *p.Config.Usage.Enabled, nil
}
func (a *App) openUsage() {
	old := a.desk.usage
	if old.cancel != nil {
		old.cancel()
	}
	host := a.desk.prefs.UsageHost
	if host == "" {
		host = a.desk.prefs.ActiveHost
	}
	if host == "" && len(a.desk.prefs.Hosts) > 0 {
		host = a.desk.prefs.Hosts[0].ID
	}
	a.desk.usage = usageState{open: true, host: host, epoch: old.epoch + 1, enabled: true}
	a.loadUsage(false, nil)
}
func (a *App) closeUsage() {
	s := &a.desk.usage
	if s.cancel != nil {
		s.cancel()
	}
	s.open = false
	s.summary = false
	s.summaryAt = time.Time{}
	s.epoch++
	s.loading = false
}

// ensureUsageSummary is called by the shell while the optional summary is shown.
func (a *App) ensureUsageSummary() {
	s := &a.desk.usage
	if s.open {
		return
	}
	if !a.desk.prefs.UsageInSidebar {
		if s.summary {
			if s.cancel != nil {
				s.cancel()
			}
			s.summary = false
			s.revision++
			s.loading = false
		}
		return
	}
	host := a.desk.prefs.UsageHost
	if host == "" {
		host = a.desk.prefs.ActiveHost
	}
	if host == "" && len(a.desk.prefs.Hosts) > 0 {
		host = a.desk.prefs.Hosts[0].ID
	}
	if host == "" {
		return
	}
	if s.host == host && (s.loading || time.Since(s.summaryAt) < time.Minute) {
		return
	}
	if s.host != host {
		s.reports = nil
	}
	s.host = host
	s.summary = true
	s.epoch++
	a.loadUsage(false, nil)
}
func (a *App) loadUsage(force bool, ids []string) {
	s := &a.desk.usage
	if s.host == "" {
		s.err = "No hosts are available. Add a host to see usage."
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	s.cancel = cancel
	s.revision++
	epoch, revision, host, gen := s.epoch, s.revision, s.host, a.generation
	s.loading = true
	s.err = ""
	s.summaryAt = time.Now()
	go func() {
		defer cancel()
		sources, cleanup := a.navigationSources(ctx)
		defer cleanup()
		var source *navigationSource
		for i := range sources {
			if sources[i].ID == host {
				source = &sources[i]
				break
			}
		}
		var entries []usageEntry
		var err error
		enabled, setting := true, false
		if source == nil {
			err = errors.New("This host is no longer saved.")
		} else if source.Err != nil {
			err = source.Err
		} else {
			_, _, setting = usageFeatures(source.Info)
			if setting {
				var raw json.RawMessage
				raw, err = source.Client.Call(ctx, "get_daemon_config_request", json.RawMessage(`{}`))
				if err == nil {
					enabled, err = parseUsageEnabled(raw)
				}
			}
			if err == nil && enabled {
				entries, err = loadUsageEntries(ctx, source.Client, source.Info, force, ids, func(entry usageEntry) {
					a.update(gen, func() {
						s := &a.desk.usage
						if s.accepts(epoch, revision, host) {
							s.reports = upsertUsage(s.reports, entry)
						}
					})
				})
			}
		}
		a.update(gen, func() {
			s := &a.desk.usage
			if !s.accepts(epoch, revision, host) {
				return
			}
			s.loading = false
			s.settingSupported = setting
			s.enabled = enabled
			if err != nil {
				s.err = err.Error()
				return
			}
			s.uncertain = false
			if len(ids) == 0 {
				s.reports = settleUsage(s.reports, entries)
			} else {
				for _, id := range ids {
					found := false
					for _, e := range entries {
						if e.ID == id {
							found = true
							break
						}
					}
					if !found {
						s.reports = removeUsage(s.reports, id)
					}
				}
				for _, e := range entries {
					s.reports = upsertUsage(s.reports, e)
				}
			}
		})
	}()
}
func usageFetchedAfter(a, b string) bool {
	first, e1 := time.Parse(time.RFC3339Nano, a)
	second, e2 := time.Parse(time.RFC3339Nano, b)
	return e1 == nil && e2 == nil && first.After(second)
}
func upsertUsage(shown []usageEntry, e usageEntry) []usageEntry {
	for i, old := range shown {
		if old.ID == e.ID {
			if !usageFetchedAfter(old.FetchedAt, e.FetchedAt) {
				shown[i] = e
			}
			return shown
		}
	}
	return append(shown, e)
}
func removeUsage(entries []usageEntry, id string) []usageEntry {
	for i, e := range entries {
		if e.ID == id {
			return append(entries[:i], entries[i+1:]...)
		}
	}
	return entries
}
func settleUsage(shown, finished []usageEntry) []usageEntry {
	result := make([]usageEntry, 0, len(finished))
	for _, e := range shown {
		for _, f := range finished {
			if e.ID == f.ID {
				if usageFetchedAfter(e.FetchedAt, f.FetchedAt) {
					result = append(result, e)
				} else {
					result = append(result, f)
				}
				break
			}
		}
	}
	for _, e := range finished {
		found := false
		for _, r := range result {
			if r.ID == e.ID {
				found = true
				break
			}
		}
		if !found {
			result = append(result, e)
		}
	}
	return result
}
func (a *App) setUsageEnabled(enabled bool) {
	s := &a.desk.usage
	if s.saving || s.loading || s.uncertain {
		return
	}
	s.saving = true
	s.err = ""
	epoch, revision, host, gen := s.epoch, s.revision, s.host, a.generation
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		sources, cleanup := a.navigationSources(ctx)
		defer cleanup()
		err := errors.New("Host is unavailable")
		confirmed := false
		for _, source := range sources {
			if source.ID != host {
				continue
			}
			if source.Err != nil {
				err = source.Err
				break
			}
			payload, _ := json.Marshal(map[string]any{"config": map[string]any{"usage": map[string]bool{"enabled": enabled}}})
			var raw json.RawMessage
			raw, err = source.Client.Call(ctx, "set_daemon_config_request", payload)
			if err == nil {
				confirmed, err = parseUsageEnabled(raw)
				if err != nil {
					err = fmt.Errorf("%w: %v", client.ErrOutcomeUnknown, err)
				} else if confirmed != enabled {
					err = fmt.Errorf("%w: host did not confirm usage setting", client.ErrOutcomeUnknown)
				}
			}
			break
		}
		a.update(gen, func() {
			s := &a.desk.usage
			if !s.accepts(epoch, revision, host) {
				return
			}
			s.saving = false
			if err != nil {
				s.err = err.Error()
				s.uncertain = errors.Is(err, client.ErrOutcomeUnknown)
				return
			}
			s.enabled = confirmed
			a.loadUsage(false, nil)
		})
	}()
}
func (a *App) usageDialog(c *ui.Context) {
	s := &a.desk.usage
	if !s.open {
		return
	}
	closeRequested := false
	modal := ui.Modal(c, &s.open, func() {
		ui.Column(c).Width(640).Padding(20).Gap(10).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, "Usage").Bold().Grow(1)
				if ui.Button(c, "Refresh all").Disabled(s.loading || s.saving).Clicked() {
					a.loadUsage(true, nil)
				}
				if ui.Button(c, "Options").Clicked() {
					s.options = !s.options
				}
				if ui.Button(c, "Close").Clicked() {
					closeRequested = true
				}
			})
			if closeRequested {
				return
			}
			ui.Row(c).Gap(6).Children(func() {
				for _, h := range a.desk.prefs.Hosts {
					if ui.Button(c, h.Name).Disabled(h.ID == s.host || s.saving).Clicked() {
						s.host = h.ID
						s.reports = nil
						a.desk.prefs.UsageHost = h.ID
						a.persistSoon()
						a.loadUsage(false, nil)
					}
				}
			})
			if s.options {
				ui.Column(c).Gap(6).Children(func() {
					ui.Row(c).Gap(8).Children(func() {
						ui.Switch(c, &a.desk.prefs.UsageRemaining).Label("Display remaining usage").OnChange(func() { a.persistSoon() })
						ui.Text(c, "Display remaining usage")
					})
					ui.Row(c).Gap(8).Children(func() {
						ui.Switch(c, &a.desk.prefs.UsageInSidebar).Label("Show usage in sidebar").OnChange(func() { a.persistSoon() })
						ui.Text(c, "Show usage in sidebar")
					})
					if s.settingSupported {
						value := s.enabled
						ui.Row(c).Gap(8).Children(func() {
							ui.Switch(c, &value).Label("Check usage on this host").Disabled(s.loading || s.saving || s.uncertain).OnChange(func() { a.setUsageEnabled(value) })
							ui.Text(c, "Check usage on this host")
						})
					}
				})
			}
			if s.loading {
				ui.Text(c, "Loading usage…").TextColor(c.Theme().TextMuted)
			}
			if s.err != "" {
				ui.Text(c, s.err).TextColor(c.Theme().Danger)
				if s.uncertain {
					ui.Text(c, "The save result is unknown. Refresh before trying another setting.")
				}
				if ui.Button(c, "Reload").Disabled(s.loading || s.saving).Clicked() {
					a.loadUsage(false, nil)
				}
			}
			if !s.enabled {
				ui.Text(c, "Usage checks are turned off on this host.")
				return
			}
			if !s.loading && s.err == "" && len(s.reports) == 0 {
				ui.Text(c, "No usage reports are available for this host.").TextColor(c.Theme().TextMuted)
			}
			ui.Scroll(c).Height(380).Children(func() {
				ui.Column(c).Gap(12).Children(func() {
					for _, entry := range s.reports {
						entry := entry
						ui.Column(c).Gap(4).Children(func() {
							label := entry.SourceLabel
							if entry.Account.Label != "" {
								label += " · " + entry.Account.Label
							}
							ui.Row(c).Gap(8).Children(func() {
								ui.Text(c, label).Bold().Grow(1)
								if ui.Button(c, "Refresh").Disabled(s.loading || s.saving).Clicked() {
									a.loadUsage(true, []string{entry.ID})
								}
							})
							report, at, retained := entry.content()
							if report.PlanLabel != "" {
								ui.Text(c, report.PlanLabel)
							}
							for _, message := range entry.messages() {
								ui.Text(c, message).TextColor(c.Theme().Danger)
							}
							if retained {
								ui.Text(c, "Showing the last successful usage report.").TextColor(c.Theme().Warning)
							}
							for _, w := range report.Windows {
								w := w
								key := usagePinKey(usageSource(entry), w.ID)
								pin := usageWindowPinned(s.reports, a.desk.prefs.UsagePins, entry, w)
								ui.Row(c).Gap(8).Children(func() {
									ui.Switch(c, &pin).Label("Pin " + w.Label).Disabled(!a.desk.prefs.UsageInSidebar).OnChange(func() {
										if a.desk.prefs.UsagePins == nil {
											a.desk.prefs.UsagePins = customUsagePins(s.reports)
										}
										a.desk.prefs.UsagePins[key] = pin
										a.persistSoon()
									})
									ui.Text(c, w.Label).Grow(1)
									ui.Text(c, usagePercent(w, a.desk.prefs.UsageRemaining))
								})
								if w.ResetsAt != "" {
									ui.Text(c, "Resets "+usageDate(w.ResetsAt)).TextColor(c.Theme().TextMuted)
								}
								if w.RunsOutAt != "" {
									ui.Text(c, "Runs out "+usageDate(w.RunsOutAt)).TextColor(c.Theme().Warning)
								}
								if w.ShortfallPct != nil {
									ui.Text(c, fmt.Sprintf("Projected shortfall %.0f%%", *w.ShortfallPct)).TextColor(c.Theme().Warning)
								}
							}
							for _, b := range report.Balances {
								ui.Text(c, usageBalanceText(b))
								if b.ResetsAt != "" {
									ui.Text(c, "Resets "+usageDate(b.ResetsAt))
								}
							}
							for _, d := range report.Details {
								ui.Text(c, d.Label+": "+d.Value)
							}
							if at != "" {
								ui.Text(c, "Updated "+usageDate(at)).TextColor(c.Theme().TextMuted)
							}
						})
					}
				})
			})
		})
	})
	if closeRequested || modal.Dismissed() || !s.open {
		a.closeUsage()
	}
}
func usageDate(raw string) string {
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.Local().Format("Jan 2, 15:04 MST")
	}
	return raw
}
func usageBalanceText(b usageBalance) string {
	var values []string
	if b.Used != nil {
		values = append(values, fmt.Sprintf("%g used", *b.Used))
	}
	if b.Remaining != nil {
		values = append(values, fmt.Sprintf("%g remaining", *b.Remaining))
	}
	if b.Limit != nil {
		values = append(values, fmt.Sprintf("%g limit", *b.Limit))
	}
	return b.Label + ": " + strings.Join(values, " · ") + " " + b.Unit
}
