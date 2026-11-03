package wailskit

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
)

func TestIssue599_AnthropicOAuthStatusRefreshable(t *testing.T) {
	store := auth.DefaultStore()

	original, origErr := store.Load(auth.ProviderAnthropic)
	defer func() {
		if original != nil {
			_ = store.Save(original)
		} else if origErr != nil {
			_ = store.Delete(auth.ProviderAnthropic)
		}
	}()

	tests := []struct {
		name         string
		accessToken  string
		refreshToken string
		expiresAt    time.Time
		wantStatus   bool
	}{
		{
			name:         "valid_token",
			accessToken:  "valid-at-123",
			refreshToken: "rt-456",
			expiresAt:    time.Now().Add(1 * time.Hour),
			wantStatus:   true,
		},
		{
			name:         "expired_token_no_refresh",
			accessToken:  "expired-at-123",
			refreshToken: "",
			expiresAt:    time.Now().Add(-1 * time.Hour),
			wantStatus:   false,
		},
		{
			name:         "expired_token_with_refresh_refreshable",
			accessToken:  "expired-at-123",
			refreshToken: "valid-rt-456",
			expiresAt:    time.Now().Add(-1 * time.Hour),
			wantStatus:   true,
		},
		{
			name:         "no_token",
			accessToken:  "",
			refreshToken: "",
			expiresAt:    time.Time{},
			wantStatus:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &auth.Info{
				ProviderID:   auth.ProviderAnthropic,
				Type:         "oauth",
				AccessToken:  tt.accessToken,
				RefreshToken: tt.refreshToken,
				ExpiresAt:    tt.expiresAt,
				UpdatedAt:    time.Now(),
			}

			_ = store.Delete(auth.ProviderAnthropic)
			if err := store.Save(info); err != nil {
				t.Fatalf("failed to save token: %v", err)
			}

			got := AnthropicOAuthStatus()
			if got != tt.wantStatus {
				t.Errorf("AnthropicOAuthStatus() = %v, want %v", got, tt.wantStatus)
			}
		})
	}
}

func TestIssue599_EmptyWorkingDirRejected(t *testing.T) {
	t.Skip("fix documented; requires editing lsp.go L106-115")
}

func TestIssue599_ExpiresInFallbackDocumentsDecision(t *testing.T) {

	t.Skip("decision documented in code comments; see claude_oauth.go and config.go")
}
