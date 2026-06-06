package im

import (
	"strings"

	"github.com/topcheer/ggcode/internal/agentruntime"
)

type SummaryRoundState struct {
	agentruntime.IMRoundState
	AskUserText  string
	PendingTools []ToolResultInfo
}

func (s *SummaryRoundState) SetAskUser(text string) {
	s.AskUserText = strings.TrimSpace(text)
}

func (s *SummaryRoundState) HasVisibleOutput() bool {
	return strings.TrimSpace(s.Text()) != ""
}

func (s *SummaryRoundState) Reset() {
	s.IMRoundState.Reset()
	s.AskUserText = ""
	s.PendingTools = nil
}
