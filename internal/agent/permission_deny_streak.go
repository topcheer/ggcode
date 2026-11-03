package agent

import (
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/tool"
)

const (
	permDenyStreakThreshold = 3
	permDenyStreakMaxFires  = 2
	permDenyRefireGap       = 5
)

type permDenyStreakState struct {
	mu         sync.Mutex
	streak     int
	userDenied int
	fires      int
	lastFireAt int
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

func isPermissionDeniedResult(r tool.Result) bool {
	if !r.IsError {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(r.Content), "Permission denied for tool")
}

func (s *permDenyStreakState) record(mode permission.PermissionMode, r tool.Result) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !isPermissionDeniedResult(r) {
		s.streak = 0
		s.userDenied = 0
		return ""
	}
	s.streak++

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

	if s.userDenied*2 >= s.streak {
		return fmt.Sprintf("[mode-guard] %d consecutive tool calls were denied - the MAJORITY (%d) were explicit USER rejections, not policy blocks. Do NOT retry the denied operation and do NOT switch permission modes - the mode is not the problem. Ask the user for clarification or choose a different approach.", s.streak, s.userDenied)
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
