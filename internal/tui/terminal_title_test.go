package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/session"
)

func TestDesiredTerminalTitleUsesActivityAndContext(t *testing.T) {
	m := Model{
		session: &session.Session{
			Workspace: "/tmp/ggcode",
			Model:     "glm-5.1",
		},
		statusActivity: "Searching code",
	}

	if got, want := m.desiredTerminalTitle(), "> Searching code — ggcode [glm-5.1]"; got != want {
		t.Fatalf("desiredTerminalTitle() = %q, want %q", got, want)
	}
}

func TestDesiredTerminalTitleFallsBackToIdleTitle(t *testing.T) {
	m := Model{
		config: &config.Config{Model: "gpt-5.4"},
	}

	if got, want := m.desiredTerminalTitle(), "> ggcode — gpt-5.4"; got != want {
		t.Fatalf("desiredTerminalTitle() = %q, want %q", got, want)
	}
}

func TestWithTerminalTitleCmdDeduplicatesWrites(t *testing.T) {
	var titles []string
	m := Model{
		session: &session.Session{
			Workspace: "/tmp/demo",
			Model:     "glm-5.1",
		},
		terminalTitleWriter: func(title string) {
			titles = append(titles, title)
		},
	}

	m, teaCmd := m.withTerminalTitleCmd(nil)
	if teaCmd == nil {
		t.Fatal("expected initial title cmd")
	}
	_ = teaCmd()

	if got, want := len(titles), 1; got != want {
		t.Fatalf("title writes = %d, want %d", got, want)
	}
	if got, want := titles[0], "> ggcode — demo [glm-5.1]"; got != want {
		t.Fatalf("initial title = %q, want %q", got, want)
	}

	m, teaCmd = m.withTerminalTitleCmd(nil)
	if teaCmd != nil {
		t.Fatal("expected duplicate title to skip write")
	}

	m.statusActivity = "Running tests"
	m, teaCmd = m.withTerminalTitleCmd(nil)
	if teaCmd == nil {
		t.Fatal("expected activity title cmd")
	}
	_ = teaCmd()

	if got, want := len(titles), 2; got != want {
		t.Fatalf("title writes = %d, want %d", got, want)
	}
	if got, want := titles[1], "> Running tests — demo [glm-5.1]"; got != want {
		t.Fatalf("activity title = %q, want %q", got, want)
	}
}

func TestSanitizeTerminalTitleStripsControlCharacters(t *testing.T) {
	if got, want := sanitizeTerminalTitle("hi\x1b]0;bad\x07\nthere"), "hi]0;bad there"; got != want {
		t.Fatalf("sanitizeTerminalTitle() = %q, want %q", got, want)
	}
}
