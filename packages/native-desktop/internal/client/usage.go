package client

import (
	"context"
	"encoding/json"
	"errors"
)

type usageStream struct {
	notify    chan struct{}
	reports   []json.RawMessage
	delivered int
	closed    bool
	onReport  func(json.RawMessage)
}

func newUsageStream(onReport func(json.RawMessage)) *usageStream {
	return &usageStream{notify: make(chan struct{}, 1), onReport: onReport}
}
func (c *Client) deliverUsage(s *usageStream) {
	c.mu.Lock()
	batch := s.reports[s.delivered:]
	s.delivered = len(s.reports)
	c.mu.Unlock()
	if s.onReport != nil {
		for _, report := range batch {
			s.onReport(report)
		}
	}
}

// ListUsageReports retains request-scoped updates until the matching completion.
// The callback runs on the calling goroutine, never under the connection lock.
func (c *Client) ListUsageReports(ctx context.Context, forceRefresh bool, reportIDs []string, onReport func(json.RawMessage)) ([]json.RawMessage, error) {
	payload, err := json.Marshal(struct {
		ForceRefresh bool     `json:"forceRefresh"`
		ReportIDs    []string `json:"reportIds,omitempty"`
	}{forceRefresh, reportIDs})
	if err != nil {
		return nil, err
	}
	stream := newUsageStream(onReport)
	raw, err := c.callWithUsage(ctx, "usage.list_reports.request", payload, nil, nil, nil, stream)
	c.mu.Lock()
	reports := stream.reports
	c.mu.Unlock()
	if err != nil {
		return reports, err
	}
	var response struct {
		Error json.RawMessage `json:"error"`
	}
	if err = json.Unmarshal(raw, &response); err != nil {
		return reports, err
	}
	if len(response.Error) == 0 {
		return reports, errors.New("usage response is missing its result")
	}
	if string(response.Error) != "null" {
		var message string
		if err = json.Unmarshal(response.Error, &message); err != nil {
			return reports, err
		}
		return reports, errors.New(message)
	}
	return reports, nil
}
