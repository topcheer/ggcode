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
	// FilesEdited records file paths actually targeted by write-class tool
	// calls this round (dedup, capped). It powers the transcript-grounded
	// changed-files footer appended to IM/desktop final round messages
	// (OverclaimBench arXiv:2609.20812); it is derived from tool calls, not
	// from model text, so it cannot be overclaimed.
	FilesEdited []string
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

// maxRoundEditedFiles caps the receipt list per round; the footer renderer
// collapses overflow into "+N more", this cap just bounds memory.
const maxRoundEditedFiles = 64

func (s *IMRoundState) NoteEditedFile(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	for _, existing := range s.FilesEdited {
		if existing == path {
			return
		}
	}
	if len(s.FilesEdited) >= maxRoundEditedFiles {
		return
	}
	s.FilesEdited = append(s.FilesEdited, path)
}

func (s *IMRoundState) Reset() {
	s.text.Reset()
	s.ToolCalls = 0
	s.ToolSuccesses = 0
	s.ToolFailures = 0
	s.FilesEdited = nil
}
