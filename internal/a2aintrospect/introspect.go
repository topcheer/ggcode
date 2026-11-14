// Package a2aintrospect aggregates runtime sources into the a2a
// RuntimeSnapshotV2 (#5575 phase 2). Sources are injected as functions so
// this package stays free of lanchat/cron/agent imports; the assembly layer
// wires real readers once the runtime chains are up.
package a2aintrospect

import (
	"bufio"
	"os"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/a2a"
)

// Sources are optional readers; nil members simply omit the face.
type Sources struct {
	Busy         func() *a2a.RuntimeBusy
	Todos        func() []a2a.RuntimeTodo
	Recent       func() []a2a.RuntimeToolTrace
	Conversation func() []a2a.RuntimeConversationTurn
	Crons        func() []a2a.RuntimeCron

	// Redactor is an optional pre-pass over conversation/recent summaries
	// (e.g. agent.RedactExternalContent). Applied BEFORE a2a's own pattern
	// redaction + truncation, so content faces get two layers (#5575 2c).
	Redactor func(string) string

	// Now overrides the snapshot clock in tests.
	Now func() time.Time
}

// RuntimeSnapshot aggregates all wired faces and applies the mode
// degradation: metadata keeps counters/identities but drops content faces
// (conversation/recent) per the #5575 design.
func (s *Sources) RuntimeSnapshot(mode string) (*a2a.RuntimeSnapshotV2, error) {
	snap := &a2a.RuntimeSnapshotV2{Schema: "v2", Mode: mode}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	snap.TakenAt = now().UTC().Format(time.RFC3339)
	if s.Busy != nil {
		snap.Busy = s.Busy()
	}
	if s.Todos != nil {
		snap.Todos = s.Todos()
	}
	if s.Crons != nil {
		snap.Crons = s.Crons()
	}
	if mode != a2a.IntrospectionMetadata {
		if s.Recent != nil {
			snap.Recent = s.Recent()
		}
		if s.Conversation != nil {
			snap.Conversation = s.Conversation()
		}
	}
	if s.Redactor != nil {
		// #5575 phase 3 ruling (user decision 2026-11-14): the conversation
		// face ships WITHOUT content redaction — the .git/a2a.key domain
		// gate IS the trust boundary, so same-key peers are trusted with raw
		// context. Redaction stays on the other content faces (recent/todos/
		// crons keep their shipped behavior); truncation remains as size
		// discipline, not redaction. The domain gate is the trust boundary.
		for i := range snap.Recent {
			snap.Recent[i].Summary = s.Redactor(snap.Recent[i].Summary)
		}
		for i := range snap.Todos {
			snap.Todos[i].Subject = s.Redactor(snap.Todos[i].Subject)
			snap.Todos[i].ActiveForm = s.Redactor(snap.Todos[i].ActiveForm)
		}
		for i := range snap.Crons {
			snap.Crons[i].Prompt = s.Redactor(snap.Crons[i].Prompt)
		}
	}
	a2a.RedactSnapshotStrings(snap)
	return snap, nil
}

// TailJSONL reads the last n lines of a JSONL file and returns one
// summary string per line (the raw line capped by the shared redaction
// discipline downstream). Missing files yield nil, nil — an absent source
// is an empty face, not an error.
func TailJSONL(path string, n int) []string {
	if path == "" || n <= 0 {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// ConversationFromLines shapes raw JSONL tail lines into conversation
// turns: role detection is best-effort ("user"/"assistant"), the summary is
// the first 200 runes of the raw payload (final redaction happens in
// a2a.RedactSnapshotStrings).
func ConversationFromLines(lines []string, max int) []a2a.RuntimeConversationTurn {
	if len(lines) > max {
		lines = lines[len(lines)-max:]
	}
	out := make([]a2a.RuntimeConversationTurn, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		role := "assistant"
		low := strings.ToLower(l)
		if strings.Contains(low, `"role":"user"`) || strings.Contains(low, `"role": "user"`) {
			role = "user"
		}
		r := []rune(l)
		if len(r) > 200 {
			r = r[:200]
		}
		out = append(out, a2a.RuntimeConversationTurn{Role: role, Summary: string(r)})
	}
	return out
}
