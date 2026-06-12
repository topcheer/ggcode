package agentruntime

import "strings"

// IMRoundState tracks assistant text and tool counters for a single LLM turn.
// Filtering policy stays in the caller so different frontends can decide which
// tool events should count toward the round.
type IMRoundState struct {
	text          strings.Builder
	ToolCalls     int
	ToolSuccesses int
	ToolFailures  int
}

func (s *IMRoundState) AppendText(text string) {
	s.text.WriteString(text)
}

func (s *IMRoundState) Text() string {
	return s.text.String()
}

func (s *IMRoundState) NoteToolCall() {
	s.ToolCalls++
}

func (s *IMRoundState) NoteToolResult(isError bool) {
	if isError {
		s.ToolFailures++
		return
	}
	s.ToolSuccesses++
}

func (s *IMRoundState) Reset() {
	s.text.Reset()
	s.ToolCalls = 0
	s.ToolSuccesses = 0
	s.ToolFailures = 0
}
