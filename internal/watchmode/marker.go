// Package watchmode implements ggcode watch mode: event-triggered agent runs
// from in-file annotations. While `ggcode watch` is running, the user can
// leave an `@ggcode` annotation in any project file (from any editor) and the
// agent picks it up and executes it non-interactively, similar to Aider's
// watch files / AI comments workflow.
package watchmode

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// MarkerToken is the in-file annotation token that triggers a watch-mode run.
// It may appear anywhere in a line, typically inside a comment:
//
//	// @ggcode! fix the nil check below and add a test
//	# @ggcode? why is this function O(n^2)
const MarkerToken = "@ggcode"

// Mode is the requested execution style for a marker.
type Mode string

const (
	// ModeAsk means the user wants an explanation only; the agent should not
	// modify files ("@ggcode? ...").
	ModeAsk Mode = "ask"
	// ModeAct means the user wants the task performed ("@ggcode! ..." or a
	// bare "@ggcode ...").
	ModeAct Mode = "act"
)

// Marker is a single detected annotation.
type Marker struct {
	// Path is the file path as passed to the scanner (relative or absolute,
	// matching how the file was listed).
	Path string
	// Line is the 1-based line number of the annotation.
	Line int
	// Text is the full raw line containing the token.
	Text string
	// Instruction is the free-text request following the token.
	Instruction string
	// Mode is ModeAsk for "@ggcode?" and ModeAct otherwise.
	Mode Mode
}

// Key returns a stable identity for the marker used for de-duplication.
// It is line-number independent so that unrelated edits above the annotation
// do not re-trigger a run; editing the instruction text produces a new key.
func (m Marker) Key() string {
	sum := sha256.Sum256([]byte(m.Instruction))
	return m.Path + "\x00" + hex.EncodeToString(sum[:8])
}

// markerRe matches the token, an optional mode suffix (? or !), and the
// remainder of the line as the instruction.
var markerRe = regexp.MustCompile(regexp.QuoteMeta(MarkerToken) + `\b\s*([!?])?\s*(.*)`)

// ExtractMarkersFromLines returns all annotations contained in lines (1-based
// numbering starts at the returned Line values). Lines with a token but an
// empty instruction are ignored — the token alone carries no request.
func ExtractMarkersFromLines(path string, lines []string) []Marker {
	var out []Marker
	for i, line := range lines {
		m := markerRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		instruction := strings.TrimSpace(m[2])
		instruction = strings.TrimSuffix(instruction, "*/")
		instruction = strings.TrimSpace(instruction)
		if instruction == "" {
			continue
		}
		mode := ModeAct
		if m[1] == "?" {
			mode = ModeAsk
		}
		out = append(out, Marker{
			Path:        path,
			Line:        i + 1,
			Text:        strings.TrimRight(line, "\r\n"),
			Instruction: instruction,
			Mode:        mode,
		})
	}
	return out
}

// String renders a marker for logs/UI.
func (m Marker) String() string {
	return fmt.Sprintf("%s:%d [%s] %s", m.Path, m.Line, m.Mode, m.Instruction)
}
