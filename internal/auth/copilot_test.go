package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNormalizeEnterpriseURL(t *testing.T) {
	got, err := NormalizeEnterpriseURL("https://company.ghe.com/")
	if err != nil {
		t.Fatalf("NormalizeEnterpriseURL() error = %v", err)
	}
	if got != "company.ghe.com" {
		t.Fatalf("expected company.ghe.com, got %q", got)
	}
}

func TestCopilotAPIBaseURL(t *testing.T) {
	if got := CopilotAPIBaseURL(""); got != "https://api.githubcopilot.com" {
		t.Fatalf("unexpected github.com base URL %q", got)
	}
	if got := CopilotAPIBaseURL("company.ghe.com"); got != "https://copilot-api.company.ghe.com" {
		t.Fatalf("unexpected enterprise base URL %q", got)
	}
}

func TestCopilotDeviceFlowStartAndPoll(t *testing.T) {
	t.Setenv("GGCODE_GITHUB_COPILOT_CLIENT_ID", "ggcode-client-id")
	var polls int
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/device/code":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("failed to decode device payload: %v", err)
			}
			if payload["client_id"] != "ggcode-client-id" {
				t.Fatalf("expected overridden client_id, got %#v", payload["client_id"])
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"verification_uri": server.URL + "/activate",
				"user_code":        "ABCD-EFGH",
				"device_code":      "device-123",
				"interval":         0,
			})
		case "/login/oauth/access_token":
			polls++
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("failed to decode token payload: %v", err)
			}
			if payload["client_id"] != "ggcode-client-id" {
				t.Fatalf("expected overridden client_id, got %#v", payload["client_id"])
			}
			if polls == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "authorization_pending"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token-123"})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := server.Client()
	flow, err := startCopilotDeviceFlow(context.Background(), client, server.URL)
	if err != nil {
		t.Fatalf("startCopilotDeviceFlow() error = %v", err)
	}
	if flow.UserCode != "ABCD-EFGH" {
		t.Fatalf("expected user code, got %#v", flow)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	info, err := pollCopilotDeviceFlow(ctx, client, flow)
	if err != nil {
		t.Fatalf("pollCopilotDeviceFlow() error = %v", err)
	}
	if info.AccessToken != "token-123" {
		t.Fatalf("expected token, got %#v", info)
	}
}
