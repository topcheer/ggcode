package a2a

// Issue #5575 (P0): same-workspace runtime introspection — domain gate +
// runtime/snapshot minimal surface.
//
// Additive contract: with a2a.introspection unset (default off) no handler is
// registered and every code path below is byte-for-byte identical to today.
// Existing A2A semantics (methods, card, discovery, mdns, per-card auth)
// remain the first door for ALL traffic; the workspace-key HMAC proof is
// layered ONLY on the runtime/* namespace.
//
// Trust model (key IS the domain):
//   - <git-common-dir>/a2a.key, 32 random bytes, 0600, auto-generated on
//     first use. Lives inside .git so it is never committed.
//   - The main checkout and every .ggcode/worktrees/* member share the same
//     git common dir, hence the same key: automatic intra-family trust.
//   - A different machine holds a different .git, hence a different key:
//     naturally isolated. Cross-machine introspection stays on the existing
//     lanchat/A2A channels (or the explicit escape hatch: copy the key).

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/security"
)

const (
	// IntrospectionMethodPrefix namespaces all zero-LLM runtime methods.
	IntrospectionMethodPrefix = "runtime/"
	// MethodRuntimeSnapshot returns the additive runtime snapshot (v2 info
	// face, minimal in P0).
	MethodRuntimeSnapshot = "runtime/snapshot"

	// IntrospectionModeMetadata is the reduced disclosure mode: conversation
	// excerpts shortened, activity ring omitted.
	IntrospectionModeMetadata = "metadata"
	// IntrospectionModeFull is the full (still truncated + redacted) face.
	IntrospectionModeFull = "full"

	// IntrospectNonceHeader carries "<unixmilli>:<hexrand>".
	IntrospectNonceHeader = "X-ggcode-Introspect-Nonce"
	// IntrospectProofHeader carries hex(HMAC-SHA256(key, nonce+"|"+method)).
	IntrospectProofHeader = "X-ggcode-Introspect-Proof"

	// introspectNonceWindow bounds nonce freshness. The proof is stateless
	// by design (#5575: no session state machine); the timestamp window is
	// the only replay bound.
	introspectNonceWindow = 5 * time.Minute

	domainKeyFile   = "a2a.key"
	domainKeyLength = 32

	// Truncation discipline: never dump full payloads.
	introspectClampFull     = 200
	introspectClampMetadata = 80
)

// ValidIntrospectionMode reports whether mode is a recognized non-empty mode.
func ValidIntrospectionMode(mode string) bool {
	return mode == IntrospectionModeMetadata || mode == IntrospectionModeFull
}

// resolveGitCommonDir returns the shared git dir for workspace: the plain
// .git directory for a main checkout, or the owning main .git for a linked
// worktree (whose .git is a "gitdir: <path>" file pointing at
// <main>/.git/worktrees/<name>).
func resolveGitCommonDir(workspace string) (string, error) {
	gitPath := filepath.Join(workspace, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return "", fmt.Errorf("introspection: no .git under %s: %w", workspace, err)
	}
	if info.IsDir() {
		return gitPath, nil
	}
	// Linked worktree: .git is a file with "gitdir: <abs path>".
	raw, err := os.ReadFile(gitPath)
	if err != nil {
		return "", fmt.Errorf("introspection: read worktree pointer: %w", err)
	}
	line := strings.TrimSpace(string(raw))
	const prefix = "gitdir:"
	if !strings.HasPrefix(line, prefix) {
		return "", errors.New("introspection: malformed worktree .git pointer")
	}
	wtDir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	// wtDir = <main>/.git/worktrees/<name> → common dir = <main>/.git
	common := filepath.Dir(filepath.Dir(wtDir))
	if st, err := os.Stat(common); err != nil || !st.IsDir() {
		return "", fmt.Errorf("introspection: resolved git common dir %s unusable", common)
	}
	return common, nil
}

// LoadOrCreateDomainKey loads (or generates, 0600) the workspace family key.
// It returns the raw key bytes and the path it lives at.
func LoadOrCreateDomainKey(workspace string) ([]byte, string, error) {
	common, err := resolveGitCommonDir(workspace)
	if err != nil {
		return nil, "", err
	}
	keyPath := filepath.Join(common, domainKeyFile)
	if raw, err := os.ReadFile(keyPath); err == nil {
		if len(raw) == domainKeyLength {
			return raw, keyPath, nil
		}
		return nil, "", fmt.Errorf("introspection: %s has %d bytes, want %d", keyPath, len(raw), domainKeyLength)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("introspection: read %s: %w", keyPath, err)
	}
	key := make([]byte, domainKeyLength)
	if _, err := rand.Read(key); err != nil {
		return nil, "", fmt.Errorf("introspection: entropy: %w", err)
	}
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		return nil, "", fmt.Errorf("introspection: write %s: %w", keyPath, err)
	}
	return key, keyPath, nil
}

// BuildIntrospectionProof derives the stateless per-request proof headers for
// the caller (client side). nonce is "<unixmilli>:<hexrand>"; proof is
// hex(HMAC-SHA256(key, nonce+"|"+method)).
func BuildIntrospectionProof(key []byte, method string, now time.Time) (nonce, proof string) {
	var rnd [8]byte
	_, _ = rand.Read(rnd[:])
	nonce = strconv.FormatInt(now.UnixMilli(), 10) + ":" + hex.EncodeToString(rnd[:])
	return nonce, introspectHMAC(key, nonce, method)
}

func introspectHMAC(key []byte, nonce, method string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(nonce + "|" + method))
	return hex.EncodeToString(mac.Sum(nil))
}

// verifyIntrospectionProof validates the domain-key proof for method.
func verifyIntrospectionProof(key []byte, method, nonce, proofHex string, now time.Time) error {
	tsPart, _, ok := strings.Cut(nonce, ":")
	if !ok {
		return errors.New("introspection: malformed nonce")
	}
	ts, err := strconv.ParseInt(tsPart, 10, 64)
	if err != nil {
		return errors.New("introspection: malformed nonce timestamp")
	}
	if d := now.Sub(time.UnixMilli(ts)); d > introspectNonceWindow || d < -introspectNonceWindow {
		return errors.New("introspection: nonce outside freshness window")
	}
	want := introspectHMAC(key, nonce, method)
	got, err := hex.DecodeString(proofHex)
	if err != nil {
		return errors.New("introspection: malformed proof")
	}
	wantBytes, _ := hex.DecodeString(want)
	if !hmac.Equal(got, wantBytes) {
		return errors.New("introspection: proof mismatch (different workspace domain key)")
	}
	return nil
}

// ---- runtime/snapshot info face (all zero-LLM) ----

type IntrospectConversationTurn struct {
	Role string    `json:"role"`
	Text string    `json:"text"`
	TS   time.Time `json:"ts,omitempty"`
}

type IntrospectTodo struct {
	ID         string `json:"id"`
	Subject    string `json:"subject"`
	Status     string `json:"status"`
	ActiveForm string `json:"activeForm,omitempty"`
}

type IntrospectCron struct {
	ID         string `json:"id"`
	Cron       string `json:"cron"`
	NextFire   string `json:"nextFire,omitempty"`
	PromptHead string `json:"promptHead,omitempty"`
}

type IntrospectBusy struct {
	Busy         bool      `json:"busy"`
	BusySince    time.Time `json:"busySince,omitempty"`
	Spinner      string    `json:"spinner,omitempty"`
	InFlightTool string    `json:"inFlightTool,omitempty"`
	Iteration    int       `json:"iteration,omitempty"`
	ContextPct   int       `json:"contextPct,omitempty"`
}

type IntrospectActivity struct {
	TS         time.Time `json:"ts"`
	Tool       string    `json:"tool"`
	Summary    string    `json:"summary"`
	DurationMS int64     `json:"durationMS,omitempty"`
}

type IntrospectTaskBrief struct {
	ID    string `json:"id"`
	Skill string `json:"skill,omitempty"`
	State string `json:"state"`
}

// RuntimeSnapshot is the runtime/snapshot result. All strings are clamped and
// secret-redacted before leaving the process (sanitizeIntrospectSnapshot).
type RuntimeSnapshot struct {
	Mode         string                       `json:"mode"`
	TakenAt      time.Time                    `json:"takenAt"`
	Conversation []IntrospectConversationTurn `json:"conversation,omitempty"`
	Todos        []IntrospectTodo             `json:"todos,omitempty"`
	Crons        []IntrospectCron             `json:"crons,omitempty"`
	Busy         *IntrospectBusy              `json:"busy,omitempty"`
	Recent       []IntrospectActivity         `json:"recent,omitempty"`
	Tasks        []IntrospectTaskBrief        `json:"tasks,omitempty"`
}

// RuntimeSnapshotSource supplies the local faces for runtime/snapshot. The
// server falls back to a TaskHandler-derived snapshot when nil.
type RuntimeSnapshotSource interface {
	RuntimeSnapshot(mode string) (RuntimeSnapshot, error)
}

func introspectClamp(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// sanitizeIntrospectSnapshot applies the disclosure policy in-place:
// RedactForDisplay on every text field, truncation discipline, and the
// metadata-mode reduction (shorter excerpts, activity ring dropped).
func sanitizeIntrospectSnapshot(snap *RuntimeSnapshot) {
	clamp := introspectClampFull
	if snap.Mode == IntrospectionModeMetadata {
		clamp = introspectClampMetadata
	}
	for i := range snap.Conversation {
		snap.Conversation[i].Text = introspectClamp(security.RedactForDisplay(snap.Conversation[i].Text), clamp)
	}
	for i := range snap.Todos {
		snap.Todos[i].Subject = introspectClamp(security.RedactForDisplay(snap.Todos[i].Subject), clamp)
		snap.Todos[i].ActiveForm = introspectClamp(security.RedactForDisplay(snap.Todos[i].ActiveForm), clamp)
	}
	for i := range snap.Crons {
		snap.Crons[i].PromptHead = introspectClamp(security.RedactForDisplay(snap.Crons[i].PromptHead), clamp)
	}
	if snap.Busy != nil {
		snap.Busy.Spinner = introspectClamp(security.RedactForDisplay(snap.Busy.Spinner), clamp)
		snap.Busy.InFlightTool = introspectClamp(security.RedactForDisplay(snap.Busy.InFlightTool), clamp)
	}
	for i := range snap.Recent {
		snap.Recent[i].Summary = introspectClamp(security.RedactForDisplay(snap.Recent[i].Summary), clamp)
	}
	if snap.Mode == IntrospectionModeMetadata {
		snap.Recent = nil
	}
}

// introspectionGate is nil unless the feature flag is on.
type introspectionGate struct {
	key    []byte
	mode   string
	source RuntimeSnapshotSource
}

// handlerIntrospectionFallback derives the minimal always-available face from
// the task handler (active task briefs) when no richer source is wired.
func handlerIntrospectionFallback(h *TaskHandler, mode string) RuntimeSnapshot {
	snap := RuntimeSnapshot{Mode: mode, TakenAt: time.Now().UTC()}
	if h == nil {
		return snap
	}
	for _, t := range h.ActiveTasks() {
		snap.Tasks = append(snap.Tasks, IntrospectTaskBrief{
			ID:    t.ID,
			Skill: t.Skill,
			State: string(t.Status.State),
		})
	}
	return snap
}

// handleIntrospection serves runtime/* (only runtime/snapshot in P0).
// Gate order: existing per-card auth already ran upstream (routeRPC is only
// reached post-auth); here the domain-key proof is the additional door.
func (s *Server) handleIntrospection(w http.ResponseWriter, r *http.Request, req *JSONRPCRequest) {
	if req.Method != MethodRuntimeSnapshot {
		writeRPCError(w, req.ID, ErrMethodNotFound)
		return
	}
	nonce := r.Header.Get(IntrospectNonceHeader)
	proof := r.Header.Get(IntrospectProofHeader)
	if err := verifyIntrospectionProof(s.introspection.key, req.Method, nonce, proof, time.Now()); err != nil {
		writeRPCError(w, req.ID, ErrAuthRequired)
		return
	}
	snap := handlerIntrospectionFallback(s.handler, s.introspection.mode)
	if s.introspection.source != nil {
		if src, err := s.introspection.source.RuntimeSnapshot(s.introspection.mode); err == nil {
			snap = src
		}
	}
	snap.Mode = s.introspection.mode
	snap.TakenAt = time.Now().UTC()
	sanitizeIntrospectSnapshot(&snap)
	writeRPCResult(w, req.ID, snap)
}
