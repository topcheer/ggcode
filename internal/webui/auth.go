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

func generateAuthToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		fallback := sha256.Sum256([]byte(fmt.Sprintf("ggcode-webui-fallback:%d:%d:%d", os.Getpid(), time.Now().UnixNano(), len(b))))
		debug.Log("webui", "WARNING: crypto/rand failed (%v); using weak fallback auth token", err)
		return hex.EncodeToString(fallback[:])
	}
	return hex.EncodeToString(b)
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.authToken == "" {

			http.Error(w, "server auth not configured", http.StatusInternalServerError)
			return
		}

		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			if authTokenMatches(strings.TrimPrefix(auth, "Bearer "), s.authToken) {
				next(w, r)
				return
			}
		}

		if isWebSocketRoute(r.URL.Path) && authTokenMatches(r.URL.Query().Get("token"), s.authToken) {
			next(w, r)
			return
		}

		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
}

func isWebSocketRoute(path string) bool {
	return path == "/api/chat/ws"
}

func authTokenMatches(provided, expected string) bool {
	if provided == "" || expected == "" {
		return false
	}
	providedHash := sha256.Sum256([]byte(provided))
	expectedHash := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(providedHash[:], expectedHash[:]) == 1
}
