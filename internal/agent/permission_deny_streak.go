package agent

import (
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/tool"
)

// Permission-deny streak detector (#1210).
//
// When consecutive tool calls are denied by the permission policy — the
// signature of a model operating in an unintended mode (typically dropped
// into plan mode without realizing it, see #1209) — no existing detector
// names the state, so the model keeps burning context on denied retries
// (SICA trajectory waste). This detector counts consecutive policy-deny
// results and, at the threshold, injects the current permission mode plus
// the self-rescue path. Deterministic, zero LLM cost.

const (
	// permDenyStreakThreshold is the consecutive-deny count that triggers guidance.
	permDenyStreakThreshold = 3
	// permDenyStreakMaxFires caps guidance injections per run.
	permDenyStreakMaxFires = 2
	// permDenyRefireGap is the additional streak needed before refiring.
	permDenyRefireGap = 5
)

// permDenyStreakState tracks consecutive permission-deny tool results.
type permDenyStreakState struct {
	mu         sync.Mutex
	streak     int
	userDenied int // subset of streak: explicit USER rejections (#1478-B)
	fires      int
	lastFireAt int // streak value when guidance last fired (0 = never)
}

func newPermDenyStreakState() *permDenyStreakState {
	return &permDenyStreakState{}
}

func (s *permDenyStreakState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.streak = 0
	s.userDenied = 0
	s.fires = 0
	s.lastFireAt = 0
}

// isPermissionDeniedResult reports whether a tool result is a permission
// policy denial, as rendered by permissionDeniedMessage (agent_tool.go,
// parallel_tools.go) and the user-rejection / no-handler variants.
func isPermissionDeniedResult(r tool.Result) bool {
	if !r.IsError {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(r.Content), "Permission denied for tool")
}

// record observes a tool result and returns mode-guard guidance when the
// consecutive-deny streak crosses the threshold. mode is the agent's current
// permission mode at the time of the result.
func (s *permDenyStreakState) record(mode permission.PermissionMode, r tool.Result) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !isPermissionDeniedResult(r) {
		s.streak = 0
		s.userDenied = 0
		return ""
	}
	s.streak++
	// #1478-B: classify provenance - user rejections must not be narrated
	// as a permission-POLICY problem (the switch_mode suggestion amounts to
	// coaching the agent to bypass the user's explicit judgment).
	if strings.Contains(r.Content, "User rejected") {
		s.userDenied++
	}
	if s.streak < permDenyStreakThreshold || s.fires >= permDenyStreakMaxFires {
		return ""
	}
	if s.lastFireAt != 0 && s.streak < s.lastFireAt+permDenyRefireGap {
		return ""
	}
	s.fires++
	s.lastFireAt = s.streak

	// #1478-B: majority-user-rejection streaks get user-intent guidance,
	// not mode-switch coaching - switch_mode cannot change a user's "no".
	if s.userDenied*2 >= s.streak {
		return fmt.Sprintf("[mode-guard] %d consecutive tool calls were rejected by the USER (explicit denials, not policy). Do NOT retry the denied operation and do NOT switch permission modes - the mode is not the problem. Ask the user for clarification or choose a different approach.", s.streak)
	}

	msg := fmt.Sprintf("[mode-guard] %d consecutive tool calls were denied by the permission policy. Current permission mode: %q.",
		s.streak, mode.String())
	if mode == permission.PlanMode {
		msg += " Plan mode is read-only; if this is unintended, call switch_mode to restore the previous mode, or present your plan via exit_plan_mode. Do not keep retrying denied calls."
	} else {
		msg += " If this mode is not what you intend, call switch_mode to correct it; otherwise stop repeating the denied operation and choose an allowed alternative."
	}
	return msg
}
