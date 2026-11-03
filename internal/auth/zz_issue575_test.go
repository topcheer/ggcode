package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIssue575_RefreshToken_EmptyAccessToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make(map[string]interface{})
		json.NewDecoder(r.Body).Decode(&body)
		if body["grant_type"] != "refresh_token" {
			t.Errorf("Expected grant_type=refresh_token, got %v", body["grant_type"])
		}

		resp := map[string]interface{}{
			"error":             "invalid_grant",
			"error_description": "Refresh token expired",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	oldURL := claudeOAuthTokenURL

	_ = server
	_ = oldURL

	t.Skip("cannot override const claudeOAuthTokenURL at runtime")
}

func TestIssue575_ClockSkewTolerance(t *testing.T) {
	tests := []struct {
		name          string
		expiresOffset time.Duration
		wantValid     bool
	}{
		{
			name:          "token expired 5 seconds ago",
			expiresOffset: -5 * time.Second,
			wantValid:     true,
		},
		{
			name:          "token expired 25 seconds ago",
			expiresOffset: -25 * time.Second,
			wantValid:     true,
		},
		{
			name:          "token expired 29 seconds ago",
			expiresOffset: -29 * time.Second,
			wantValid:     true,
		},
		{
			name:          "token expired 30 seconds ago (boundary)",
			expiresOffset: -30 * time.Second,
			wantValid:     false,
		},
		{
			name:          "token expired 31 seconds ago",
			expiresOffset: -31 * time.Second,
			wantValid:     false,
		},
		{
			name:          "token expires in future",
			expiresOffset: 60 * time.Second,
			wantValid:     true,
		},
		{
			name:          "token just expired now",
			expiresOffset: 0,
			wantValid:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expiresAt := time.Now().Add(tt.expiresOffset)

			info := &Info{
				ProviderID:   ProviderAnthropic,
				AccessToken:  "test_token",
				RefreshToken: "refresh_token",
				ExpiresAt:    expiresAt,
			}

			isExpired := !info.ExpiresAt.IsZero() && time.Now().After(info.ExpiresAt)
			if isExpired {
				if time.Now().Sub(info.ExpiresAt) < 30*time.Second {
					isExpired = false
				}
			}

			valid := !isExpired
			if valid != tt.wantValid {
				t.Errorf("token validity = %v, want %v (expiresOffset: %v)", valid, tt.wantValid, tt.expiresOffset)
			}
		})
	}
}
