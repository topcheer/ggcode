package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSession(t *testing.T, dir, name, toolCalls string) {
	t.Helper()
	line := `{"type":"tool_call","tool_name":"` + strings.ReplaceAll(toolCalls, ",", `"}`+"\n"+`{"type":"tool_call","tool_name":"`) + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestAnalyzeToolFlows_MinesRecurringWorkflow: a grep→read_file→edit_file
// chain repeated across sessions must surface as a trigram with full
// confidence plus its bigram prefixes.
func TestAnalyzeToolFlows_MinesRecurringWorkflow(t *testing.T) {
	dir := t.TempDir()
	// 5 sessions each containing one full chain: support = 5 (>= threshold).
	for i := 0; i < 5; i++ {
		writeSession(t, dir, "s"+string(rune('a'+i))+".jsonl", "grep,read_file,edit_file")
	}
	pats, err := AnalyzeToolFlows(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	var sawTri, sawBi bool
	for _, p := range pats {
		if len(p.Prefix) == 2 && p.Prefix[0] == "grep" && p.Prefix[1] == "read_file" && p.Next == "edit_file" {
			sawTri = true
			if p.Count != 5 {
				t.Errorf("trigram count = %d, want 5", p.Count)
			}
			if p.Confidence != 1.0 {
				t.Errorf("trigram confidence = %.2f, want 1.0", p.Confidence)
			}
		}
		if len(p.Prefix) == 1 && p.Prefix[0] == "grep" && p.Next == "read_file" {
			sawBi = true
		}
	}
	if !sawTri {
		t.Error("expected grep→read_file→edit_file trigram pattern")
	}
	if !sawBi {
		t.Error("expected grep→read_file bigram prefix pattern")
	}
}

// TestAnalyzeToolFlows_SupportThreshold: one-off sequences are noise, not
// workflow - below support threshold nothing is reported.
func TestAnalyzeToolFlows_SupportThreshold(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "lonely.jsonl", "a,b,c,d,e,f")
	pats, err := AnalyzeToolFlows(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pats) != 0 {
		t.Errorf("single-occurrence session must yield no patterns, got %d", len(pats))
	}
}

// TestAnalyzeToolFlows_NoCrossSessionTransition: a session ending on tool X
// must not chain into the next session's opener Y.
func TestAnalyzeToolFlows_NoCrossSessionTransition(t *testing.T) {
	dir := t.TempDir()
	// Six sessions: s1 ends with "grep", s2 opens with "read_file"...
	// arranged so the ONLY possible x→y transitions are cross-file.
	for i := 0; i < 6; i++ {
		writeSession(t, dir, "s"+string(rune('a'+i))+".jsonl", "grep")
	}
	for i := 0; i < 6; i++ {
		writeSession(t, dir, "t"+string(rune('a'+i))+".jsonl", "read_file")
	}
	pats, err := AnalyzeToolFlows(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pats {
		if p.Prefix[len(p.Prefix)-1] == "grep" && p.Next == "read_file" {
			t.Fatal("cross-session transition leaked into pattern set")
		}
	}
}

// TestAnalyzeToolFlows_ConfidenceSplits: a prefix with split continuations
// (50/50) must not report either as dominant.
func TestAnalyzeToolFlows_ConfidenceSplits(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		writeSession(t, dir, "u"+string(rune('a'+i))+".jsonl", "search,x")
		writeSession(t, dir, "v"+string(rune('a'+i))+".jsonl", "search,y")
	}
	pats, err := AnalyzeToolFlows(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pats {
		if p.Prefix[0] == "search" && (p.Next == "x" || p.Next == "y") {
			t.Fatalf("50/50 continuation reported as dominant: %+v", p)
		}
	}
}

func TestAnalyzeToolFlows_EmptyDir(t *testing.T) {
	pats, err := AnalyzeToolFlows(t.TempDir(), 5)
	if err != nil || len(pats) != 0 {
		t.Fatalf("empty dir: pats=%v err=%v", pats, err)
	}
}
