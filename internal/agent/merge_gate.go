package agent

// mergeGateState: the configurable merge completion gate (gates.merge).
// Recovered from irrev_gate.go during the advisory-gate removal sweep:
// this is the merge_preflight snapshot data plane (MergeGatePreflightStatus
// feeds the merge_preflight tool and config gates.test), NOT an advisory
// injector - user-configured, fires only on merge-shaped commands.
import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
)

type mergeGateState struct {
	mu        sync.Mutex
	snapshots map[int]mergePassSnapshot
	cfgFn     func() config.MergeGateConfig
	now       func() time.Time
}

type mergePassSnapshot struct {
	headSHA string
	at      time.Time
}

func newMergeGateState() *mergeGateState {
	return &mergeGateState{
		snapshots: make(map[int]mergePassSnapshot),
		now:       time.Now,
	}
}

// SetMergeGateConfigProvider wires a live gates config reader (set by the
// runtime once the config access layer exists). Nil/never-set means the
// built-in defaults: enabled, ["gh pr merge", "gh pr close"], 60s TTL.
func (s *mergeGateState) SetMergeGateConfigProvider(fn func() config.MergeGateConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfgFn = fn
}

func (s *mergeGateState) mergeGateConfig() config.MergeGateConfig {
	s.mu.Lock()
	fn := s.cfgFn
	s.mu.Unlock()
	if fn == nil {
		return config.MergeGateConfig{}
	}
	return fn()
}

// RecordPreflightResult parses a merge_preflight tool result and, when the
// verdict is PASS, records a freshness snapshot for that PR and head.
// The first output line of the tool is the machine-readable report JSON.
func (s *mergeGateState) RecordPreflightResult(toolName, content string) {
	if toolName != "merge_preflight" {
		return
	}
	first := content
	if i := strings.IndexByte(content, '\n'); i >= 0 {
		first = content[:i]
	}
	first = strings.TrimSpace(first)
	if !strings.HasPrefix(first, "{") {
		return
	}
	var report struct {
		PR      int    `json:"pr"`
		HeadSHA string `json:"head_sha"`
		Verdict string `json:"verdict"`
	}
	if err := json.Unmarshal([]byte(first), &report); err != nil {
		return
	}
	if report.Verdict != "PASS" || report.PR <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[report.PR] = mergePassSnapshot{headSHA: report.HeadSHA, at: s.now()}
}

// preflightFresh reports whether a PASS snapshot for pr is younger than
// ttl, plus the head SHA it pinned.
func (s *mergeGateState) preflightFresh(pr int, ttl time.Duration) (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok := s.snapshots[pr]
	if !ok {
		return false, ""
	}
	if s.now().Sub(snap.at) > ttl {
		return false, ""
	}
	return true, snap.headSHA
}

// checkCommand returns the REQUIREMENT prompt for a protected lifecycle
// command without a fresh PASS snapshot, or "" when no requirement
// applies (non-protected command, gate disabled, cruise/bypass mode, or
// a fresh snapshot). Reuses the B-side first-token/subcommand extraction
// infra (extractCommandFromArgs + stripLeadingShellComment).
func (s *mergeGateState) checkCommand(mode permission.PermissionMode, toolName, args string) string {
	switch toolName {
	case "run_command", "start_command":
	default:
		return ""
	}
	if mode.IsCruiseMode() {
		// bypass/cruise: pass through, never prompt, never block
		return ""
	}
	cmd := extractCommandFromArgs(json.RawMessage(args))
	if cmd == "" {
		return ""
	}
	cfg := s.mergeGateConfig()
	if !cfg.EffectiveEnabled() {
		return ""
	}
	pr, hit := config.MatchMergeGateCommand(cmd, cfg.EffectiveCommands())
	if !hit {
		return ""
	}
	if fresh, _ := s.preflightFresh(pr, cfg.EffectivePreflightTTL()); fresh {
		return ""
	}
	return mergeGateRequirement(cmd, pr, cfg.EffectivePreflightTTL())
}

func mergeGateRequirement(cmd string, pr int, ttl time.Duration) string {
	// #5401: selector-less/branch forms ("gh pr merge", "gh pr merge <branch>")
	// gate with PR 0 - the agent must resolve the target PR first instead of
	// preflighting PR number 0.
	step1 := "1. Call the merge_preflight tool with {\"pr_number\":" + strconv.Itoa(pr) +
		"} and confirm the result line is PREFLIGHT: PASS.\n"
	if pr <= 0 {
		step1 = "1. Identify the target PR (the command carries no explicit PR number - " +
			"e.g. from the current branch), then call the merge_preflight tool with that " +
			"{\"pr_number\":N} and confirm the result line is PREFLIGHT: PASS.\n"
	}
	return "[completion-gate] The command \"" + cmd + "\" is a gated lifecycle action " +
		"(gates.merge). REQUIREMENT before executing it:\n" +
		step1 +
		"2. Pin the action to the head SHA preflight reports by adding " +
		"--match-head-commit <head_sha>.\n" +
		"A PASS snapshot for this PR stays valid for " + ttl.String() +
		" (preflight_ttl); while it is fresh this gate does not re-trigger. " +
		"This gate states requirements only; it does not block execution."
}

// SetMergeGateConfigProvider wires a live gates.merge config reader into
// the agent's completion gate (called by the runtime after startup and on
// config reload).
func (a *Agent) SetMergeGateConfigProvider(fn func() config.MergeGateConfig) {
	if a.mergeGate == nil {
		return
	}
	a.mergeGate.SetMergeGateConfigProvider(fn)
}

// MergeGatePreflightStatus reports whether a fresh PREFLIGHT: PASS
// snapshot exists for the PR (within the configured TTL) and the head SHA
// it pinned. Exported for the merge_preflight tool's test action.
func (a *Agent) MergeGatePreflightStatus(prNumber int) (fresh bool, headSHA string) {
	if a.mergeGate == nil || prNumber <= 0 {
		return false, ""
	}
	cfg := a.mergeGate.mergeGateConfig()
	return a.mergeGate.preflightFresh(prNumber, cfg.EffectivePreflightTTL())
}
