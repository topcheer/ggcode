package webui

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// generateAuthToken creates a cryptographically random 32-byte hex string.
//
// crypto/rand.Read is documented to effectively never fail on supported
// platforms (the kernel getrandom/getentropy paths block-until-ready rather
// than error). If it somehow does, we must not panic: NewServer runs inside
// CLI/daemon startup, and a panic here would kill the whole process. Fall
// back to a hash of coarse process entropy instead - weaker than CSPRNG
// output, but the alternative (crash, or running with an empty token that
// requireAuth treats as "auth disabled") is strictly worse. The fallback is
// logged loudly for diagnosis.
func generateAuthToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		fallback := sha256.Sum256([]byte(fmt.Sprintf("ggcode-webui-fallback:%d:%d:%d", os.Getpid(), time.Now().UnixNano(), len(b))))
		debug.Log("webui", "WARNING: crypto/rand failed (%v); using weak fallback auth token", err)
		return hex.EncodeToString(fallback[:])
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
			if authTokenMatches(strings.TrimPrefix(auth, "Bearer "), s.authToken) {
				next(w, r)
				return
			}
		}

		// Fallback: check query parameter (needed for WebSocket upgrade)
		if authTokenMatches(r.URL.Query().Get("token"), s.authToken) {
			next(w, r)
			return
		}

		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
}

func authTokenMatches(provided, expected string) bool {
	if provided == "" || expected == "" {
		return false
	}
	providedHash := sha256.Sum256([]byte(provided))
	expectedHash := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(providedHash[:], expectedHash[:]) == 1
}
