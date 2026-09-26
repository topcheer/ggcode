package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
)

const recitationFixture = "Todo state: 3 total (1 pending, 1 in_progress, 1 done)\nActive todos:\n- 2 (in_progress): implement parser"

func TestTodoReciterFirstSightingRecites(t *testing.T) {
	r := newTodoReciter()
	if got := r.maybe(recitationFixture); got != recitationFixture {
		t.Fatalf("first sighting must recite verbatim, got %q", got)
	}
}

func TestTodoReciterUnchangedWaitsForInterval(t *testing.T) {
	r := newTodoReciter()
	if got := r.maybe(recitationFixture); got == "" {
		t.Fatal("precondition: first sighting recites")
	}
	for i := 1; i < todoRecitationInterval; i++ {
		if got := r.maybe(recitationFixture); got != "" {
			t.Fatalf("send %d: unexpected recitation %q", i, got)
		}
	}
	if got := r.maybe(recitationFixture); got != recitationFixture {
		t.Fatalf("interval send must recite, got %q", got)
	}
	// Counter must have reset: the next interval-1 sends stay silent.
	for i := 1; i < todoRecitationInterval; i++ {
		if got := r.maybe(recitationFixture); got != "" {
			t.Fatalf("post-reset send %d: unexpected recitation %q", i, got)
		}
	}
}

func TestTodoReciterChangedContentRecitesImmediately(t *testing.T) {
	r := newTodoReciter()
	s1 := "Todo state: 1 total (1 pending, 0 in_progress, 0 done)"
	s2 := s1 + "\nActive todos:\n- 1 (in_progress): implement parser"
	r.maybe(s1)
	if got := r.maybe(s1); got != "" {
		t.Fatalf("unchanged plan must stay silent, got %q", got)
	}
	if got := r.maybe(s2); got != s2 {
		t.Fatalf("changed plan must recite immediately, got %q", got)
	}
}

func TestTodoReciterEmptyResetsAndClearsState(t *testing.T) {
	r := newTodoReciter()
	r.maybe(recitationFixture)
	r.maybe(recitationFixture)
	if got := r.maybe(""); got != "" {
		t.Fatalf("empty summary must never recite, got %q", got)
	}
	// After the reset, the same plan is a first sighting again.
	if got := r.maybe(recitationFixture); got != recitationFixture {
		t.Fatalf("post-reset plan must recite as first sighting, got %q", got)
	}
}

func TestTodoReciterTrimsSummary(t *testing.T) {
	r := newTodoReciter()
	if got := r.maybe("  " + recitationFixture + "  "); got != recitationFixture {
		t.Fatalf("summary must be trimmed before recite/comparison, got %q", got)
	}
	if got := r.maybe(recitationFixture); got != "" {
		t.Fatalf("trimmed duplicate must not recite, got %q", got)
	}
}

func TestTodoRecitationMsgEnvelope(t *testing.T) {
	msg := todoRecitationMsg("PLAN")
	if msg.Role != "user" {
		t.Fatalf("recitation must be a user-role message, got %q", msg.Role)
	}
	if len(msg.Content) != 1 || msg.Content[0].Type != "text" {
		t.Fatalf("recitation must carry exactly one text block, got %+v", msg.Content)
	}
	want := "<system-reminder>\nPLAN\n</system-reminder>"
	if msg.Content[0].Text != want {
		t.Fatalf("unexpected envelope: got %q want %q", msg.Content[0].Text, want)
	}
}

// appendTodoRecitation end-to-end through a real context.Manager: the
// recitation is appended to the request slice, the original slice and the
// manager's own history stay untouched (ephemeral guarantee).
func TestAppendTodoRecitationEphemeral(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "todo.json")
	todoJSON := `[{"id":"1","content":"implement parser","status":"in_progress"},{"id":"2","content":"write tests","status":"pending"}]`
	if err := os.WriteFile(tmp, []byte(todoJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	cm := ctxpkg.NewManager(200000)
	cm.SetTodoFilePath(tmp)
	a := &Agent{todoRecite: newTodoReciter(), contextManager: cm}

	base := []provider.Message{{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}}}
	msgs := append([]provider.Message(nil), base...)

	got := a.appendTodoRecitation(1, msgs)
	if len(got) != len(base)+1 {
		t.Fatalf("first send must recite: got %d messages, want %d", len(got), len(base)+1)
	}
	last := got[len(got)-1]
	if last.Role != "user" || !strings.Contains(last.Content[0].Text, "<system-reminder>") {
		t.Fatalf("appended message must be wrapped user reminder, got %+v", last)
	}
	if !strings.Contains(last.Content[0].Text, "implement parser") {
		t.Fatalf("recitation must carry plan content verbatim, got %q", last.Content[0].Text)
	}
	// Ephemeral: original slice unchanged; manager history still empty.
	if len(base) != 1 {
		t.Fatalf("caller slice must not be mutated, got %d messages", len(base))
	}
	if len(cm.Messages()) != 0 {
		t.Fatalf("recitation must not be persisted into the manager, got %d messages", len(cm.Messages()))
	}
	// Unchanged plan + interval not reached: no duplication on the next send.
	if got := a.appendTodoRecitation(2, append([]provider.Message(nil), base...)); len(got) != len(base) {
		t.Fatalf("unchanged plan must not recite before interval, got %d messages", len(got))
	}
}

func TestAppendTodoRecitationNilGuards(t *testing.T) {
	cm := ctxpkg.NewManager(200000)
	// nil reciter: no-op.
	a := &Agent{contextManager: cm}
	msgs := []provider.Message{{Role: "user"}}
	if got := a.appendTodoRecitation(1, msgs); len(got) != 1 {
		t.Fatalf("nil reciter must be a no-op, got %d messages", len(got))
	}
	// No todo file bound: summary empty, no recitation.
	a2 := &Agent{todoRecite: newTodoReciter(), contextManager: cm}
	if got := a2.appendTodoRecitation(1, msgs); len(got) != 1 {
		t.Fatalf("unbound todo path must be a no-op, got %d messages", len(got))
	}
}
