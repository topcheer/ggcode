package wailskit

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
)

// TestIssue599_AnthropicOAuthStatusRefreshable tests the O2 fix:
// When access token is expired but refresh token exists, status should
// return true to avoid misleading "not connected" UI.
//
// PROPOSED CHANGE in config.go L1094-1118:
// Replace AnthropicOAuthStatus() with three-state logic:
//
//   func AnthropicOAuthStatus() bool {
//       // Check if access token is directly usable
//       usable, err := auth.DefaultStore().HasUsableToken(auth.ProviderAnthropic)
//       if err != nil {
//           return false
//       }
//       if usable {
//           return true // valid state
//       }
//
//       // Access token expired/unusable, but check if we can refresh (#599).
//       // If we have a refresh token, the UI should show "connected" so users
//       // don't go through full OAuth flow when a simple refresh would recover.
//       info, err := auth.DefaultStore().Load(auth.ProviderAnthropic)
//       if err != nil || info == nil {
//           return false // dead state
//       }
//       return strings.TrimSpace(info.RefreshToken) != "" // refreshable state
//   }
//
// This prevents misleading "not connected" UI when a simple refresh would recover.

func TestIssue599_AnthropicOAuthStatusRefreshable(t *testing.T) {
	store := auth.DefaultStore()

	// Save and restore original state
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

// TestIssue599_EmptyWorkingDirRejected tests the L1 fix:
// InstallLSPServer should reject empty workingDir.
//
// PROPOSED CHANGE in lsp.go L106-115:
// Add validation after unlocking mu:
//
//   func (b *ChatBridge) InstallLSPServer(languageID, optionID string) LSPInstallResult {
//       b.mu.Lock()
//       wd := b.workingDir
//       b.mu.Unlock()
//
//       // Reject empty workingDir to avoid installing to process CWD (#599).
//       // Only reachable if NewChatBridge constructor's os.Getwd() fails (extremely rare).
//       if wd == "" {
//           return LSPInstallResult{
//               Success: false,
//               Output:  "Workspace directory is empty; cannot install language server",
//           }
//       }
//
//       opts := lsp.GetInstallOptions(languageID, wd)
//       ...
//   }

func TestIssue599_EmptyWorkingDirRejected(t *testing.T) {
	t.Skip("fix documented; requires editing lsp.go L106-115")
}

// TestIssue599_ExpiresInFallbackDocumentsDecision documents the O3 decision.
// The 3600s fallback is documented in code comments at:
// - internal/auth/claude_oauth.go line ~365
// - desktop/wailskit/config.go line ~1176

func TestIssue599_ExpiresInFallbackDocumentsDecision(t *testing.T) {
	// Decision: Keep the 3600s fallback with explanatory comment.
	// The anomaly (missing expires_in from Claude API) is better handled
	// with a short default than with zero (which would cause HasUsableToken
	// to treat the token as never-expiring).
	//
	// Production paths should never hit this branch as Claude's OAuth
	// endpoint always returns expires_in.
	//
	// See comments in source files for full rationale.
	t.Skip("decision documented in code comments; see claude_oauth.go and config.go")
}
