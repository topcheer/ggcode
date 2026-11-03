package agent

import (
	"strconv"
	"strings"
)

const (
	autonomyPromoteStreak   = 30
	autonomyDemoteDenies    = 3
	autonomyDemoteErrStreak = 5
)

type autonomyDialState struct {
	consecutiveOK int
	errStreak     int
	denyCount     int
	suggestedUp   bool
	suggestedDown bool
}

func newAutonomyDialState() *autonomyDialState { return &autonomyDialState{} }

func (s *autonomyDialState) reset() {
	s.consecutiveOK = 0
	s.errStreak = 0
	s.denyCount = 0
	s.suggestedUp = false
	s.suggestedDown = false
}

var autonomyDenialMarkers = []string{
	"permission denied",
	"denied by permission",
	"rejected by permission",
	"approval denied",
	"approval rejected",
	"user rejected",
	"user denied",
	"was blocked by",
}

func looksLikeDenial(content string) bool {
	if content == "" {
		return false
	}
	lc := strings.ToLower(content)
	for _, m := range autonomyDenialMarkers {
		if strings.Contains(lc, m) {
			return true
		}
	}
	return false
}

func (s *autonomyDialState) record(isErr bool, content string) {
	if isErr {
		if looksLikeDenial(content) {
			s.denyCount++
		} else {
			s.errStreak++
		}
		s.consecutiveOK = 0
		return
	}
	s.consecutiveOK++
	s.errStreak = 0
}

func (s *autonomyDialState) suggest() string {
	if !s.suggestedUp && s.consecutiveOK >= autonomyPromoteStreak && s.denyCount == 0 && s.errStreak == 0 {
		s.suggestedUp = true
		return "[autonomy-dial] " + strconv.Itoa(s.consecutiveOK) + " consecutive clean tool calls with zero denials this run - " +
			"demonstrated reliability supports more autonomy. Consider switching to a less supervised mode " +
			"(switch_mode / /mode) if this matches your trust level. Advisory only; you stay in control."
	}
	if !s.suggestedDown && (s.denyCount >= autonomyDemoteDenies || s.errStreak >= autonomyDemoteErrStreak) {
		s.suggestedDown = true
		reason := strconv.Itoa(s.denyCount) + " permission denials"
		if s.errStreak >= autonomyDemoteErrStreak {
			reason = strconv.Itoa(s.errStreak) + " consecutive failed tool calls"
		}
		return "[autonomy-dial] " + reason + " observed - the current mode may be outrunning demonstrated reliability. " +
			"Consider a more supervised mode (switch_mode / /mode) or course-correcting the approach. Advisory only."
	}
	return ""
}
