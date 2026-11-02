package client

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// Set PASEO_SMOKE_URL to a running existing daemon's WebSocket endpoint.
// PASEO_SMOKE_PASSWORD or PASEO_SMOKE_LOCAL_CREDENTIAL may supply authentication.
func TestExistingDaemonSmoke(t *testing.T) {
	url := os.Getenv("PASEO_SMOKE_URL")
	if url == "" {
		t.Skip("PASEO_SMOKE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c := new(Client)
	if err := c.Connect(ctx, Target{URL: url, ClientID: "native-desktop-smoke", Password: os.Getenv("PASEO_SMOKE_PASSWORD"), LocalCredential: os.Getenv("PASEO_SMOKE_LOCAL_CREDENTIAL")}); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var info struct {
		ServerID string `json:"serverId"`
	}
	if err := json.Unmarshal(c.ServerInfo(), &info); err != nil || info.ServerID == "" {
		t.Fatalf("invalid server info: %s, %v", c.ServerInfo(), err)
	}
	payload, err := c.Call(ctx, "fetch_agents_request", json.RawMessage(`{"scope":"active"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(payload) {
		t.Fatalf("invalid agent response: %s", payload)
	}
	if agentID := os.Getenv("PASEO_SMOKE_AGENT"); agentID != "" {
		sub, err := c.Observe(ctx, Observation{AgentIDs: []string{agentID}})
		if err != nil {
			t.Fatal(err)
		}
		request, _ := json.Marshal(map[string]any{"agentId": agentID, "direction": "tail", "limit": 40})
		history, err := c.Call(ctx, "fetch_agent_timeline_request", request)
		if err != nil {
			t.Fatal(err)
		}
		var response struct {
			AgentID string `json:"agentId"`
			Epoch   string `json:"epoch"`
		}
		if err := json.Unmarshal(history, &response); err != nil || response.AgentID != agentID || response.Epoch == "" {
			t.Fatalf("timeline identity mismatch: %s, %v", history, err)
		}
		if err := sub.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
