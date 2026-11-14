package a2a

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// #5575 same-workspace runtime introspection: zero-LLM runtime/* namespace.
//
// Trust model (issue #5575 final design, five-round convergence):
//   - Domain key lives at <gitdir>/a2a.key (0600, inside .git so it is never
//     committed). Worktrees resolve their .git pointer file to the main
//     gitdir, so the whole .git family shares one key automatically.
//   - Cross-machine clones carry different .git => different key => natural
//     isolation; scope checks trust the key only, never workspace strings.
//   - HMAC(key, nonce+method) is a stateless per-request proof stacked ONLY
//     on runtime/* methods; legacy methods never see this check.

// DomainKeyFile is the key name inside the resolved git directory.
const DomainKeyFile = "a2a.key"

// Introspection modes for a2a.introspection config ("" = off = handler not
// registered, byte-identical behavior with today).
const (
	IntrospectionOff      = ""
	IntrospectionMetadata = "metadata"
	IntrospectionFull     = "full"
)

// ErrIntrospectionDenied is returned when the runtime/* proof fails. It uses
// the JSON-RPC error envelope; message is distinct from Method-not-found so
// operators can tell "feature off" from "wrong key".
var ErrIntrospectionDenied = &JSONRPCError{Code: -32001, Message: "Introspection denied"}

// ResolveGitDir returns the real git directory for dir. In a linked worktree
// dir/.git is a pointer file ("gitdir: /path/to/main/.git/wt"); the domain
// key must live in the shared main gitdir so the family shares one key.
func ResolveGitDir(dir string) (string, error) {
	dot := filepath.Join(dir, ".git")
	fi, err := os.Stat(dot)
	if err != nil {
		return "", err
	}
	if fi.IsDir() {
		return dot, nil
	}
	raw, err := os.ReadFile(dot)
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(raw))
	const p = "gitdir:"
	if !strings.HasPrefix(s, p) {
		return "", errors.New("a2a: malformed .git pointer file")
	}
	gd := strings.TrimSpace(strings.TrimPrefix(s, p))
	// Linked worktrees point at <main>/.git/worktrees/<name>; the domain
	// key lives at the shared main .git root so the whole family trusts one
	// key (#5575). Walk the worktrees suffix up to the main gitdir.
	if i := strings.LastIndex(gd, "/worktrees/"); i >= 0 {
		gd = gd[:i]
	}
	return gd, nil
}

// LoadOrCreateDomainKey returns the workspace domain key bytes for dir,
// generating a fresh 32-byte key on first use (0600, never committed because
// it lives inside the git directory).
func LoadOrCreateDomainKey(dir string) ([]byte, error) {
	gd, err := ResolveGitDir(dir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(gd, DomainKeyFile)
	if b, err := os.ReadFile(path); err == nil && len(b) >= 32 {
		return b[:32], nil
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	enc := []byte(hex.EncodeToString(raw))
	if err := os.WriteFile(path, enc, 0o600); err != nil {
		return nil, err
	}
	return enc[:32], nil // encoded form doubles as key material (stable)
}

// VerifyIntrospectionProof checks the stateless HMAC second factor for a
// runtime/* request: sig == HMAC-SHA256(key, nonce + "|" + method). Constant-
// time compare; nonce is an input factor only (no session state machine by
// design).
func VerifyIntrospectionProof(key []byte, method, nonce, sigHex string) bool {
	if len(key) == 0 || nonce == "" || sigHex == "" {
		return false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(nonce + "|" + method))
	want := mac.Sum(nil)
	got, err := hex.DecodeString(strings.ToLower(sigHex))
	if err != nil || len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// --- snapshot v2 payload types (five faces; first ship: busy/todos/recent) ---

// RuntimeToolTrace is one recent-activity ring entry.
type RuntimeToolTrace struct {
	TS         string `json:"ts"`
	Tool       string `json:"tool"`
	Summary    string `json:"summary"`
	DurationMs int64  `json:"duration_ms,omitempty"`
}

// RuntimeBusy is the busy/current face from the existing busy-state machine.
type RuntimeBusy struct {
	Busy         bool   `json:"busy"`
	BusySince    string `json:"busy_since,omitempty"`
	Spinner      string `json:"spinner,omitempty"`
	InFlightTool string `json:"in_flight_tool,omitempty"`
	InFlightArgs string `json:"in_flight_args_summary,omitempty"`
	Iteration    int    `json:"iteration,omitempty"`
	TokenUsage   int64  `json:"token_usage,omitempty"`
	ContextPct   int    `json:"context_pct,omitempty"`
}

// RuntimeTodo is one task-board row.
type RuntimeTodo struct {
	ID         string `json:"id"`
	Subject    string `json:"subject"`
	Status     string `json:"status"`
	ActiveForm string `json:"active_form,omitempty"`
}

// RuntimeSnapshotV2 is the runtime/snapshot result. Faces not yet populated
// stay nil so the JSON shape is forward-compatible (conversation/crons land
// in later phases per the staged plan).
type RuntimeSnapshotV2 struct {
	Schema  string             `json:"schema"` // "v2"
	Mode    string             `json:"mode"`   // metadata|full
	TakenAt string             `json:"taken_at"`
	Busy    *RuntimeBusy       `json:"busy,omitempty"`
	Todos   []RuntimeTodo      `json:"todos,omitempty"`
	Recent  []RuntimeToolTrace `json:"recent,omitempty"`
}

// RuntimeIntrospector is injected by the assembly layer (cmd/ggcode root);
// the a2a server itself stays free of agent/lanchat imports. Zero LLM by
// contract: implementations must read in-memory state only.
type RuntimeIntrospector interface {
	RuntimeSnapshot(mode string) (*RuntimeSnapshotV2, error)
}

// secretRedactPatterns are appended by tests via RegisterSecretPattern for
// pin coverage; core set covers common token shapes.
var secretRedactPatterns = []string{
	"api_key=", "apikey=", "token=", "password=", "secret=", "authorization:",
}

// RegisterSecretPattern adds a literal marker redacted from snapshot output
// (test hook for the DoD secrets pin).
func RegisterSecretPattern(p string) { secretRedactPatterns = append(secretRedactPatterns, p) }

// RedactSnapshotStrings masks secret-looking fragments across all text
// faces. Truncation discipline (200-char heads) is applied here too so a
// full dump can never ride through the summary fields.
func RedactSnapshotStrings(s *RuntimeSnapshotV2) {
	const head = 200
	trunc := func(v string) string {
		v = strings.TrimSpace(v)
		for _, p := range secretRedactPatterns {
			if i := strings.Index(strings.ToLower(v), p); i >= 0 {
				v = v[:i] + "[REDACTED]"
			}
		}
		if len(v) > head {
			return v[:head] + "…"
		}
		return v
	}
	if s == nil {
		return
	}
	if s.Busy != nil {
		s.Busy.Spinner = trunc(s.Busy.Spinner)
		s.Busy.InFlightArgs = trunc(s.Busy.InFlightArgs)
	}
	for i := range s.Todos {
		s.Todos[i].Subject = trunc(s.Todos[i].Subject)
		s.Todos[i].ActiveForm = trunc(s.Todos[i].ActiveForm)
	}
	for i := range s.Recent {
		s.Recent[i].Summary = trunc(s.Recent[i].Summary)
	}
}

// NewRuntimeSnapshotHandler wires the introspector and mode; nil introspector
// means the feature is configured but has no source (denies everything).
func NewRuntimeSnapshotHandler(inst RuntimeIntrospector, mode string, now func() time.Time) func(method, nonce, sig string) (*RuntimeSnapshotV2, *JSONRPCError) {
	return func(method, nonce, sig string) (*RuntimeSnapshotV2, *JSONRPCError) {
		if inst == nil {
			return nil, ErrIntrospectionDenied
		}
		if method != "runtime/snapshot" {
			return nil, ErrMethodNotFound
		}
		snap, err := inst.RuntimeSnapshot(mode)
		if err != nil {
			return nil, &JSONRPCError{Code: -32002, Message: "Snapshot source error: " + err.Error()}
		}
		if snap == nil {
			snap = &RuntimeSnapshotV2{Schema: "v2", Mode: mode}
		}
		snap.Schema = "v2"
		snap.Mode = mode
		if now != nil {
			snap.TakenAt = now().UTC().Format(time.RFC3339)
		}
		RedactSnapshotStrings(snap)
		return snap, nil
	}
}

// RuntimeIntrospectorFunc adapts a plain function to RuntimeIntrospector
// (assembly-layer convenience for phase-staged wiring).
type RuntimeIntrospectorFunc func(mode string) (*RuntimeSnapshotV2, error)

func (f RuntimeIntrospectorFunc) RuntimeSnapshot(mode string) (*RuntimeSnapshotV2, error) {
	return f(mode)
}
