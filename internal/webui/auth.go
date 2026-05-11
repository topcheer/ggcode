package webui

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
)

// generateAuthToken creates a cryptographically random 32-byte hex string.
func generateAuthToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// Fallback should never happen with crypto/rand
		panic("webui: failed to generate auth token: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// requireAuth is middleware that validates the request carries a valid auth token.
// Token can be provided via:
//   - Authorization: Bearer <token> header (REST API)
//   - ?token=<token> query parameter (WebSocket, fallback)
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.authToken == "" {
			// No token configured (shouldn't happen, but defense in depth)
			next(w, r)
			return
		}

		// Check Bearer token in Authorization header
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			if strings.TrimPrefix(auth, "Bearer ") == s.authToken {
				next(w, r)
				return
			}
		}

		// Fallback: check query parameter (needed for WebSocket upgrade)
		if r.URL.Query().Get("token") == s.authToken {
			next(w, r)
			return
		}

		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
}
