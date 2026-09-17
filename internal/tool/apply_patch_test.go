package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseV4APatch(t *testing.T) {
	diff := "*** Begin Patch\n" +
		"*** Update File: src/app.go\n" +
		"@@ func main()\n" +
		"-old()\n" +
		"+new()\n" +
		"*** Move to: src/main.go\n" +
		"*** Add File: docs/new.md\n" +
		"+# Title\n" +
		"+body\n" +
		"*** Delete File: junk.txt\n" +
		"*** End Patch\n"
	secs, err := parseV4APatch(diff)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(secs) != 3 {
		t.Fatalf("want 3 sections, got %d", len(secs))
	}
	if secs[0].kind != "update" || secs[0].path != "src/app.go" || secs[0].moveTo != "src/main.go" {
		t.Errorf("update section wrong: %+v", secs[0])
	}
	if secs[1].kind != "add" || len(secs[1].lines) != 2 {
		t.Errorf("add section wrong: %+v", secs[1])
	}
	if secs[2].kind != "delete" || secs[2].path != "junk.txt" {
		t.Errorf("delete section wrong: %+v", secs[2])
	}

	// Envelope optional.
	secs, err = parseV4APatch("*** Add File: a.txt\n+hi\n")
	if err != nil || len(secs) != 1 {
		t.Fatalf("envelopeless patch: %v %+v", err, secs)
	}

	// Unknown directive fails closed.
	if _, err := parseV4APatch("*** Rewrite World: x\n+q\n"); err == nil {
		t.Fatal("want error for unknown directive")
	}
	// Content before header fails.
	if _, err := parseV4APatch("+stray\n"); err == nil {
		t.Fatal("want error for stray content")
	}
}

func TestApplyV4AHunks(t *testing.T) {
	src := "line1\nline2\nline3\nline2\nline5\n"
	hunks := []v4aHunk{
		{lines: []v4aLine{
			{kind: ' ', text: "line1"},
			{kind: '-', text: "line2"},
			{kind: '+', text: "TWO"},
		}},
		// Monotonic: the second hunk must match the SECOND "line2" occurrence.
		{lines: []v4aLine{
			{kind: ' ', text: "line2"},
			{kind: ' ', text: "line5"},
			{kind: '+', text: "inserted"},
		}},
	}
	got, err := applyV4AHunks(src, hunks)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := "line1\nTWO\nline3\nline2\nline5\ninserted\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	// Trailing-whitespace-tolerant context matching.
	h := []v4aHunk{{lines: []v4aLine{
		{kind: ' ', text: "line1  "}, // extra trailing ws in patch context
		{kind: '-', text: "line2"},
		{kind: '+', text: "X"},
	}}}
	if got, err := applyV4AHunks(src, h); err != nil {
		t.Fatalf("ws-tolerant match: %v", err)
	} else {
		lines := strings.Split(got, "\n")
		if len(lines) < 2 || !strings.HasPrefix(lines[0], "line1") || lines[1] != "X" {
			t.Fatalf("ws-tolerant result: %q", got)
		}
	}

	// Context not found fails with a clear message.
	bad := []v4aHunk{{lines: []v4aLine{{kind: ' ', text: "nope"}}}}
	if _, err := applyV4AHunks(src, bad); err == nil || !strings.Contains(err.Error(), "context not found") {
		t.Fatalf("want context-not-found error, got %v", err)
	}

	// Pure-addition hunk (no ctx/del) is rejected - cannot be located.
	pure := []v4aHunk{{lines: []v4aLine{{kind: '+', text: "x"}}}}
	if _, err := applyV4AHunks(src, pure); err == nil {
		t.Fatal("want error for context-less hunk")
	}
}

func TestApplyPatchExecute(t *testing.T) {
	dir := t.TempDir()
	p := ApplyPatch{WorkingDir: dir}

	call := func(t *testing.T, input string) Result {
		t.Helper()
		res, err := p.Execute(context.Background(), json.RawMessage(input))
		if err != nil {
			t.Fatalf("system error: %v", err)
		}
		return res
	}

	// create_file
	res := call(t, `{"type":"create_file","path":"docs/hello.md","diff":"*** Add File: docs/hello.md\n+# Hi\n+body\n"}`)
	if res.IsError {
		t.Fatalf("create failed: %s", res.Content)
	}
	data, err := os.ReadFile(filepath.Join(dir, "docs/hello.md"))
	if err != nil || string(data) != "# Hi\nbody\n" {
		t.Fatalf("created content: %q err=%v", data, err)
	}
	// create on existing file errors.
	if res := call(t, `{"type":"create_file","path":"docs/hello.md","diff":"*** Add File: docs/hello.md\n+x\n"}`); !res.IsError {
		t.Fatal("create over existing should fail")
	}

	// update_file
	patch := "*** Update File: docs/hello.md\n@@\n # Hi\n-body\n+body!\n"
	res = call(t, `{"type":"update_file","path":"docs/hello.md","diff":`+quote(patch)+`}`)
	if res.IsError {
		t.Fatalf("update failed: %s", res.Content)
	}
	data, _ = os.ReadFile(filepath.Join(dir, "docs/hello.md"))
	if string(data) != "# Hi\nbody!\n" {
		t.Fatalf("updated content: %q", data)
	}

	// update with unapplicable context -> IsError result (not Go error).
	res = call(t, `{"type":"update_file","path":"docs/hello.md","diff":"*** Update File: docs/hello.md\n@@\n ghost\n-x\n+y\n"}`)
	if !res.IsError || !strings.Contains(res.Content, "context not found") {
		t.Fatalf("want context-not-found IsError, got %+v", res)
	}

	// delete_file
	res = call(t, `{"type":"delete_file","path":"docs/hello.md","diff":"*** Delete File: docs/hello.md"}`)
	if res.IsError {
		t.Fatalf("delete failed: %s", res.Content)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs/hello.md")); !os.IsNotExist(err) {
		t.Fatal("file should be gone")
	}

	// Bundled multi-section diff: only the addressed section applies.
	res = call(t, `{"type":"update_file","path":"a.txt","diff":"*** Update File: a.txt\n-x\n+y\n*** Delete File: b.txt\n"}`)
	if !res.IsError {
		// a.txt doesn't exist yet; expectation: section selected, open fails.
		t.Fatalf("want error for missing file, got %+v", res)
	}
}

func TestApplyPatchSandboxAndHidden(t *testing.T) {
	dir := t.TempDir()
	blocked := 0
	p := ApplyPatch{
		WorkingDir:   dir,
		SandboxCheck: func(string) bool { blocked++; return false },
	}
	res, err := p.Execute(context.Background(), json.RawMessage(
		`{"type":"create_file","path":"x.txt","diff":"*** Add File: x.txt\n+q\n"}`))
	if err != nil {
		t.Fatalf("system error: %v", err)
	}
	if !res.IsError || blocked != 1 {
		t.Fatalf("sandbox must gate writes: res=%+v blocked=%d", res, blocked)
	}
	// Hidden from definitions - reached only via the Responses apply_patch tool.
	if p.Available() {
		t.Fatal("apply_patch must stay hidden from ToDefinitions")
	}
	if p.Name() != "apply_patch" {
		t.Fatalf("name: %q", p.Name())
	}
	// Nested operation shape also accepted.
	p2 := ApplyPatch{WorkingDir: dir}
	res, err = p2.Execute(context.Background(), json.RawMessage(
		`{"operation":{"type":"create_file","path":"nested.txt","diff":"*** Add File: nested.txt\n+ok\n"}}`))
	if err != nil || res.IsError {
		t.Fatalf("nested op: %+v err=%v", res, err)
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
