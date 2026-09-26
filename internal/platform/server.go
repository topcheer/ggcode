package platform

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// loginFailDelay blunts online password guessing without a rate limiter.
const loginFailDelay = 250 * time.Millisecond

type ctxUserKey struct{}

// Server is the platform HTTP control plane.
type Server struct {
	users  *UserStore
	jobs   *JobStore
	ws     *WorkspaceSet
	exec   *Executor
	secret []byte
	http   *http.Server
}

// NewServer assembles the API. secret is the HS256 signing key (see
// LoadOrCreateSecret).
func NewServer(users *UserStore, jobs *JobStore, ws *WorkspaceSet, exec *Executor, secret []byte) *Server {
	return &Server{users: users, jobs: jobs, ws: ws, exec: exec, secret: secret}
}

// Handler returns the routed HTTP handler (exported for tests).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/v1/jobs", s.auth(s.handleCreateJob))
	mux.HandleFunc("GET /api/v1/jobs", s.auth(s.handleListJobs))
	mux.HandleFunc("GET /api/v1/jobs/{id}", s.auth(s.handleGetJob))
	mux.HandleFunc("DELETE /api/v1/jobs/{id}", s.auth(s.handleCancelJob))
	mux.HandleFunc("GET /api/v1/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	return mux
}

// ListenAndServe blocks serving on addr.
func (s *Server) ListenAndServe(addr string) error {
	s.http = &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	err := s.http.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.http == nil {
		return nil
	}
	return s.http.Shutdown(ctx)
}

// handleLogin exchanges username/password for a JWT.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	user, ok := s.users.Authenticate(req.Username, req.Password)
	if !ok {
		time.Sleep(loginFailDelay)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	token, err := SignToken(s.secret, user.Name, DefaultTokenTTL)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token signing failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

// auth wraps a handler with JWT bearer authentication.
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		h := r.Header.Get("Authorization")
		if len(h) <= len(prefix) || h[:len(prefix)] != prefix {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing bearer token"})
			return
		}
		claims, err := VerifyToken(s.secret, h[len(prefix):])
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			return
		}
		if _, ok := s.users.Get(claims.Sub); !ok {
			// User deleted after token issuance.
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxUserKey{}, claims.Sub)))
	}
}

func requestUser(r *http.Request) string {
	u, _ := r.Context().Value(ctxUserKey{}).(string)
	return u
}

// handleCreateJob validates the workspace against the allowlist and queues a
// headless run.
func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Workspace string `json:"workspace"`
		Prompt    string `json:"prompt"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.Prompt == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "prompt is required"})
		return
	}
	workspace, err := s.ws.Resolve(req.Workspace)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace not allowed"})
		return
	}
	job, err := s.jobs.Create(requestUser(r), workspace, req.Prompt)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "job creation failed"})
		return
	}
	s.exec.Submit(job)
	writeJSON(w, http.StatusCreated, job)
}

// handleListJobs returns the caller's jobs (admins see all). Output is
// omitted in list responses; fetch the detail endpoint for it.
func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.jobs.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "job listing failed"})
		return
	}
	user := requestUser(r)
	isAdmin := s.isAdmin(user)
	out := make([]*Job, 0, len(jobs))
	for _, j := range jobs {
		if !isAdmin && j.User != user {
			continue
		}
		clone := *j
		clone.Output = ""
		out = append(out, &clone)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGetJob returns full job detail (own or admin; 404 otherwise so job
// IDs are not enumerable across users).
func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	job, ok := s.ownJobOrNotFound(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// handleCancelJob cancels a queued/running job (own or admin).
func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	job, ok := s.ownJobOrNotFound(w, r)
	if !ok {
		return
	}
	if job.Status.Terminal() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "job already finished"})
		return
	}
	s.exec.Cancel(job.ID)
	fresh, err := s.jobs.Get(job.ID)
	if err != nil {
		fresh = job
	}
	writeJSON(w, http.StatusOK, fresh)
}

func (s *Server) ownJobOrNotFound(w http.ResponseWriter, r *http.Request) (*Job, bool) {
	job, err := s.jobs.Get(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "job not found"})
		return nil, false
	}
	user := requestUser(r)
	if job.User != user && !s.isAdmin(user) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "job not found"})
		return nil, false
	}
	return job, true
}

func (s *Server) isAdmin(name string) bool {
	u, ok := s.users.Get(name)
	return ok && u.Admin
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// LoadOrCreateSecret reads (or creates with 0600) the 32-byte signing key.
func LoadOrCreateSecret(dir string) ([]byte, error) {
	path := filepath.Join(dir, "secret.key")
	if b, err := os.ReadFile(path); err == nil {
		if key, derr := hex.DecodeString(string(b)); derr == nil && len(key) == 32 {
			return key, nil
		}
		return nil, fmt.Errorf("platform: malformed secret %s", path)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("platform: secret generation: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("platform: platform dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(key)), 0o600); err != nil {
		return nil, fmt.Errorf("platform: secret persist: %w", err)
	}
	return key, nil
}

// timeNowUTC is a seam for tests.
var timeNowUTC = func() time.Time { return time.Now().UTC() }
