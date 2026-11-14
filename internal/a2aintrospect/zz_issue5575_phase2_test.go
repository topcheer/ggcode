package a2aintrospect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/a2a"
)

func TestPhase2_AggregatesAllFaces(t *testing.T) {
	src := &Sources{
		Busy:  func() *a2a.RuntimeBusy { return &a2a.RuntimeBusy{Busy: true, InFlightTool: "run_command"} },
		Todos: func() []a2a.RuntimeTodo { return []a2a.RuntimeTodo{{ID: "1", Subject: "s", Status: "pending"}} },
		Recent: func() []a2a.RuntimeToolTrace {
			return []a2a.RuntimeToolTrace{{TS: "t", Tool: "grep", Summary: "hit"}}
		},
		Conversation: func() []a2a.RuntimeConversationTurn {
			return []a2a.RuntimeConversationTurn{{Role: "user", Summary: "do it"}}
		},
		Crons: func() []a2a.RuntimeCron { return []a2a.RuntimeCron{{ID: "c1", Cron: "0 * * * *"}} },
	}
	snap, err := src.RuntimeSnapshot(a2a.IntrospectionFull)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Busy == nil || len(snap.Todos) != 1 || len(snap.Recent) != 1 || len(snap.Conversation) != 1 || len(snap.Crons) != 1 {
		t.Fatalf("faces incomplete: %+v", snap)
	}
}

func TestPhase2_MetadataModeDropsContentFaces(t *testing.T) {
	src := &Sources{
		Busy:         func() *a2a.RuntimeBusy { return &a2a.RuntimeBusy{Busy: true} },
		Todos:        func() []a2a.RuntimeTodo { return []a2a.RuntimeTodo{{ID: "1"}} },
		Recent:       func() []a2a.RuntimeToolTrace { return []a2a.RuntimeToolTrace{{Tool: "x"}} },
		Conversation: func() []a2a.RuntimeConversationTurn { return []a2a.RuntimeConversationTurn{{Summary: "secret"}} },
	}
	snap, _ := src.RuntimeSnapshot(a2a.IntrospectionMetadata)
	if snap.Conversation != nil || snap.Recent != nil {
		t.Fatal("metadata mode must drop conversation/recent content faces")
	}
	if snap.Busy == nil || len(snap.Todos) != 1 {
		t.Fatal("identity faces (busy/todos) must survive metadata mode")
	}
}

func TestPhase2_TailJSONLAndConversationShaping(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "session.jsonl")
	long := strings.Repeat("x", 300)
	os.WriteFile(p, []byte("{\"role\":\"user\",\"text\":\"hi\"}\n{\"role\":\"assistant\",\"text\":\""+long+"\"}\n"), 0o644)
	lines := TailJSONL(p, 8)
	if len(lines) != 2 {
		t.Fatalf("lines = %d", len(lines))
	}
	if TailJSONL(filepath.Join(dir, "missing.jsonl"), 8) != nil {
		t.Fatal("missing file must yield nil face, not error")
	}
	turns := ConversationFromLines(lines, 8)
	if turns[0].Role != "user" || turns[1].Role != "assistant" {
		t.Fatalf("roles: %+v", turns)
	}
	if got := len([]rune(turns[1].Summary)); got != 200 {
		t.Fatalf("summary len = %d, want 200-cap", got)
	}
}

func TestPhase2_SecretRedactionPassesThrough(t *testing.T) {
	src := &Sources{
		Conversation: func() []a2a.RuntimeConversationTurn {
			return []a2a.RuntimeConversationTurn{{Role: "user", Summary: "api_key=sk-live-leak build"}}
		},
	}
	snap, _ := src.RuntimeSnapshot(a2a.IntrospectionFull)
	if strings.Contains(snap.Conversation[0].Summary, "sk-live-leak") {
		t.Fatal("secret leaked through aggregation")
	}
}

func TestPhase2c_RedactorPrePassLayers(t *testing.T) {
	var seen string
	src := &Sources{
		Redactor: func(s string) string { seen = s; return "cleaned " + s },
		Recent: func() []a2a.RuntimeToolTrace {
			return []a2a.RuntimeToolTrace{{TS: "t", Tool: "run_command", Summary: "deploy api_key=sk-live-xyz"}}
		},
	}
	snap, _ := src.RuntimeSnapshot(a2a.IntrospectionFull)
	if !strings.HasPrefix(snap.Recent[0].Summary, "cleaned") {
		t.Fatalf("redactor pre-pass missing: %q", snap.Recent[0].Summary)
	}
	if !strings.Contains(seen, "sk-live-xyz") {
		t.Fatal("redactor must receive raw content before masking")
	}
}
