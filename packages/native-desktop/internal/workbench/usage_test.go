package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestUsageRetainsSuccessfulValuesButShowsCurrentFailure(t *testing.T) {
	var e usageEntry
	err := json.Unmarshal([]byte(`{"id":"claude:work","sourceLabel":"Claude","account":{"label":"Work"},"fetchedAt":"2026-11-12T10:00:00Z","report":{"status":"error","error":"Login expired"},"lastSuccessful":{"fetchedAt":"2026-11-12T09:00:00Z","report":{"status":"available","windows":[{"id":"session","label":"Session","remainingPct":68}]}}}`), &e)
	if err != nil {
		t.Fatal(err)
	}
	report, at, retained := e.content()
	if !retained || at != "2026-11-12T09:00:00Z" || len(report.Windows) != 1 {
		t.Fatalf("retained = %+v %s %v", report, at, retained)
	}
	if got := usagePercent(report.Windows[0], false); got != "32% used" {
		t.Fatal(got)
	}
	if !strings.Contains(strings.Join(e.messages(), " "), "Login expired") {
		t.Fatal("current error disappeared")
	}
}
func TestUsageSummaryDoesNotAddUnrelatedAccountPercentages(t *testing.T) {
	entries := []usageEntry{{ID: "a", SourceLabel: "Claude", Report: usageReport{Status: "available", Windows: []usageWindow{{ID: "session", Label: "Session", Summary: true, UsedPct: new(float64(30))}}}}, {ID: "b", SourceLabel: "Codex", Report: usageReport{Status: "available", Windows: []usageWindow{{ID: "week", Label: "Weekly", Summary: true, UsedPct: new(float64(80))}}}}}
	got := usageSummary(entries, nil, false)
	if got != "Claude Session 30% used · Codex Weekly 80% used" {
		t.Fatal(got)
	}
	got = usageSummary(entries, map[string]bool{usagePinKey("b", "week"): true}, true)
	if got != "Codex Weekly 20% remaining" {
		t.Fatal(got)
	}
}
func TestUsageCompletionCannotCrossHostOrClosedDialog(t *testing.T) {
	s := usageState{open: true, host: "b", epoch: 4, revision: 2}
	if s.accepts(4, 2, "a") || s.accepts(3, 2, "b") || s.accepts(4, 1, "b") {
		t.Fatal("old request accepted")
	}
	if !s.accepts(4, 2, "b") {
		t.Fatal("current request rejected")
	}
	s.open = false
	if s.accepts(4, 2, "b") {
		t.Fatal("closed dialog accepted update")
	}
}
func TestUsageLegacyMalformedAndUnsupportedResponses(t *testing.T) {
	for _, raw := range []string{`{}`, `{"providers":null}`, `{"providers":[{"providerId":"","status":"available"}]}`} {
		c := &homeProviderTestCaller{responses: map[string]json.RawMessage{"provider.usage.list.request": json.RawMessage(raw)}}
		if _, err := loadUsageEntries(context.Background(), c, json.RawMessage(`{"features":{"providerUsageList":true}}`), false, nil, nil); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	c := &homeProviderTestCaller{}
	if _, err := loadUsageEntries(context.Background(), c, json.RawMessage(`{"features":{}}`), false, nil, nil); err == nil || len(c.calls) != 0 {
		t.Fatalf("unsupported host queried: %v", err)
	}
	c = &homeProviderTestCaller{errs: map[string]error{"provider.usage.list.request": errors.New("offline")}}
	if _, err := loadUsageEntries(context.Background(), c, json.RawMessage(`{"features":{"providerUsageList":true}}`), false, nil, nil); err == nil || err.Error() != "offline" {
		t.Fatal(err)
	}
}

func TestUsageSettlingKeepsAccountOrderAndNewerRefresh(t *testing.T) {
	shown := []usageEntry{{ID: "a", FetchedAt: "2026-11-12T11:00:00Z", SourceLabel: "new"}, {ID: "b", FetchedAt: "2026-11-12T10:00:00Z"}, {ID: "gone"}}
	finished := []usageEntry{{ID: "b", FetchedAt: "2026-11-12T10:00:00Z"}, {ID: "a", FetchedAt: "2026-11-12T09:00:00Z", SourceLabel: "old"}, {ID: "c"}}
	got := settleUsage(shown, finished)
	if len(got) != 3 || got[0].ID != "a" || got[0].SourceLabel != "new" || got[1].ID != "b" || got[2].ID != "c" {
		t.Fatalf("settled reports = %+v", got)
	}
}

type usageStreamTestCaller struct{ legacyCalls int }

func (c *usageStreamTestCaller) Call(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	c.legacyCalls++
	return nil, errors.New("unexpected legacy call")
}
func (c *usageStreamTestCaller) ListUsageReports(_ context.Context, force bool, ids []string, onReport func(json.RawMessage)) ([]json.RawMessage, error) {
	if !force || len(ids) != 1 || ids[0] != "source:work" {
		return nil, errors.New("wrong report selection")
	}
	raw := json.RawMessage(`{"id":"source:work","sourceId":"source","sourceLabel":"Provider","account":{"label":"Work"},"fetchedAt":"2026-11-12T10:00:00Z","report":{"status":"available","windows":[]}}`)
	onReport(raw)
	return []json.RawMessage{raw}, errors.New("another source failed")
}
func TestUsageModernPartialFailureKeepsStreamedReportsWithoutLegacyRetry(t *testing.T) {
	c := &usageStreamTestCaller{}
	var streamed []usageEntry
	entries, err := loadUsageEntries(context.Background(), c, json.RawMessage(`{"features":{"usageSources":true,"providerUsageList":true}}`), true, []string{"source:work"}, func(e usageEntry) { streamed = append(streamed, e) })
	if err == nil || err.Error() != "another source failed" || len(entries) != 1 || len(streamed) != 1 || entries[0].Account.Label != "Work" || c.legacyCalls != 0 {
		t.Fatalf("entries=%+v streamed=%+v err=%v legacy=%d", entries, streamed, err, c.legacyCalls)
	}
}
func TestUsageConfigRejectsMissingSaveAcknowledgement(t *testing.T) {
	for _, raw := range []string{`{}`, `{"config":null}`, `{"config":{"usage":{"enabled":"yes"}}}`} {
		if _, err := parseUsageEnabled(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	enabled, err := parseUsageEnabled(json.RawMessage(`{"config":{"usage":{"enabled":false}}}`))
	if err != nil || enabled {
		t.Fatalf("disabled policy = %v %v", enabled, err)
	}
}

func TestUsagePinsApplyToEveryAccountAndCanSelectNone(t *testing.T) {
	entries := []usageEntry{
		{ID: "one", SourceID: "claude", SourceLabel: "Claude", Report: usageReport{Status: "available", Windows: []usageWindow{{ID: "session", Label: "Session", UsedPct: new(float64(20))}}}},
		{ID: "two", SourceID: "claude", SourceLabel: "Claude", Report: usageReport{Status: "available", Windows: []usageWindow{{ID: "session", Label: "Session", UsedPct: new(float64(50))}}}},
	}
	if got := usageSummary(entries, nil, false); got != "Claude Session 20% used · Claude Session 50% used" {
		t.Fatal(got)
	}
	pins := customUsagePins(entries)
	pins[usagePinKey("claude", "session")] = false
	if got := usageSummary(entries, pins, false); got != "" {
		t.Fatalf("explicitly unpinned windows reappeared: %s", got)
	}
	raw, err := json.Marshal(pins)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]bool
	if err = json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if got := usageSummary(entries, saved, false); got != "" {
		t.Fatalf("saved empty selection reverted to defaults: %s", got)
	}
	entries[0].Report.Status = "error"
	if got := usageSummary(entries, nil, false); got != "Claude Session 50% used" {
		t.Fatalf("failed account appeared in summary: %s", got)
	}
}

func TestUsageFreshnessComparesInstantsAcrossTimezoneOffsets(t *testing.T) {
	shown := []usageEntry{{ID: "account", FetchedAt: "2026-11-12T12:00:00+02:00", SourceLabel: "older"}}
	got := upsertUsage(shown, usageEntry{ID: "account", FetchedAt: "2026-11-12T10:30:00Z", SourceLabel: "newer"})
	if got[0].SourceLabel != "newer" {
		t.Fatal("timezone text order replaced chronological freshness")
	}
}
