package tool

import (
	"context"
	"strings"
	"testing"
)

// resetEditFixerState gives each test a clean hook, memo, and budget.
func resetEditFixerState(t *testing.T, fn EditFixerFunc) {
	t.Helper()
	t.Setenv("GGCODE_EDIT_FIXER", "")
	SetEditFixer(fn)
	editFixerCalls.Store(0)
	editFixMemoMu.Lock()
	editFixMemo = map[string]struct{}{}
	editFixMemoMu.Unlock()
	t.Cleanup(func() { SetEditFixer(nil) })
}

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	path := t.TempDir() + "/sample.go"
	if err := atomicWriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("setup write failed: %v", err)
	}
	return path
}

// TestEditFixerNormalPath: the repairer corrects a stale old_text and the
// edit lands, with the auto-correction surfaced in the result message.
func TestEditFixerNormalPath(t *testing.T) {
	resetEditFixerState(t, func(ctx context.Context, req EditFixRequest) (string, bool) {
		return "value := compute(x)", true
	})
	path := writeTempFile(t, "package main\n\nfunc main() {\n\tvalue := compute(x)\n\t_ = value\n}\n")
	ef := EditFile{WorkingDir: t.TempDir()}
	// The model sends old_text using a renamed symbol that only exists in
	// its imagination; the repairer restores the original identifier.
	res, err := ef.Execute(context.Background(), mustJSON(t, map[string]any{
		"file_path": path,
		"old_text":  "value := recalc(x)",
		"new_text":  "value := recalc(y)",
	}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "auto-corrected by edit-fixer") {
		t.Fatalf("expected fix note in result, got: %s", res.Content)
	}
}

// TestEditFixerFailurePath: when the repairer produces nothing usable, the
// original diagnostic error is returned unchanged.
func TestEditFixerFailurePath(t *testing.T) {
	resetEditFixerState(t, func(ctx context.Context, req EditFixRequest) (string, bool) {
		return "", false
	})
	path := writeTempFile(t, "package main\n\nfunc main() {}\n")
	ef := EditFile{WorkingDir: t.TempDir()}
	res, err := ef.Execute(context.Background(), mustJSON(t, map[string]any{
		"file_path": path,
		"old_text":  "totally absent text",
		"new_text":  "x",
	}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "old_text not found") {
		t.Fatalf("expected original error, got: %s", res.Content)
	}
}

// TestEditFixerMemoBlocksRetryLoop: a second identical failing edit must
// not trigger a second repair call (loop containment).
func TestEditFixerMemoBlocksRetryLoop(t *testing.T) {
	calls := 0
	resetEditFixerState(t, func(ctx context.Context, req EditFixRequest) (string, bool) {
		calls++
		return "", false
	})
	path := writeTempFile(t, "package main\n\nfunc main() {}\n")
	ef := EditFile{WorkingDir: t.TempDir()}
	args := map[string]any{
		"file_path": path,
		"old_text":  "totally absent text",
		"new_text":  "x",
	}
	for i := 0; i < 2; i++ {
		if _, err := ef.Execute(context.Background(), mustJSON(t, args)); err != nil {
			t.Fatalf("execute %d: %v", i, err)
		}
	}
	if calls != 1 {
		t.Fatalf("expected 1 repair call after repeated identical failure, got %d", calls)
	}
}

// TestEditFixerSkipsOversizeOldText: large old_text never reaches the
// repairer.
func TestEditFixerSkipsOversizeOldText(t *testing.T) {
	calls := 0
	resetEditFixerState(t, func(ctx context.Context, req EditFixRequest) (string, bool) {
		calls++
		return "x", true
	})
	path := writeTempFile(t, "package main\n")
	ef := EditFile{WorkingDir: t.TempDir()}
	res, err := ef.Execute(context.Background(), mustJSON(t, map[string]any{
		"file_path": path,
		"old_text":  strings.Repeat("a", editFixMaxOldText+1),
		"new_text":  "x",
	}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error for oversized old_text")
	}
	if calls != 0 {
		t.Fatalf("repairer must not be called for oversized old_text, calls=%d", calls)
	}
}

// TestEditFixerBudgetCap: after editFixMaxCalls repairs, the hook stops
// spending.
func TestEditFixerBudgetCap(t *testing.T) {
	calls := 0
	resetEditFixerState(t, func(ctx context.Context, req EditFixRequest) (string, bool) {
		calls++
		return "", false
	})
	ef := EditFile{WorkingDir: t.TempDir()}
	for i := 0; i < editFixMaxCalls+5; i++ {
		path := writeTempFile(t, "package main\n\nfunc main() {}\n")
		// Distinct old_text per attempt so the memo does not short-circuit
		// before the budget does.
		_, err := ef.Execute(context.Background(), mustJSON(t, map[string]any{
			"file_path": path,
			"old_text":  "absent text number " + strings.Repeat("x", i+1),
			"new_text":  "y",
		}))
		if err != nil {
			t.Fatalf("execute %d: %v", i, err)
		}
	}
	if calls > editFixMaxCalls {
		t.Fatalf("budget cap exceeded: %d repair calls", calls)
	}
}

// TestEditFixerDisabledByEnv: the kill switch bypasses the hook entirely.
func TestEditFixerDisabledByEnv(t *testing.T) {
	calls := 0
	resetEditFixerState(t, func(ctx context.Context, req EditFixRequest) (string, bool) {
		calls++
		return "value := compute(x)", true
	})
	t.Setenv("GGCODE_EDIT_FIXER", "0")
	path := writeTempFile(t, "package main\n\nfunc main() {\n\tvalue := compute(x)\n}\n")
	ef := EditFile{WorkingDir: t.TempDir()}
	res, err := ef.Execute(context.Background(), mustJSON(t, map[string]any{
		"file_path": path,
		"old_text":  "value := recalc(x)",
		"new_text":  "value := recalc(y)",
	}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error with fixer disabled")
	}
	if calls != 0 {
		t.Fatalf("repairer must not run when disabled, calls=%d", calls)
	}
}

// TestEditFixerCorrectedMustPassUniqueness: a correction that matches
// multiple locations is rejected by the normal ambiguity gate - the fixer
// gets no shortcut around edit safety.
func TestEditFixerCorrectedMustPassUniqueness(t *testing.T) {
	resetEditFixerState(t, func(ctx context.Context, req EditFixRequest) (string, bool) {
		return "return nil", true // matches twice in the file
	})
	path := writeTempFile(t, "package main\n\nfunc a() error {\n\treturn nil\n}\n\nfunc b() error {\n\treturn nil\n}\n")
	ef := EditFile{WorkingDir: t.TempDir()}
	res, err := ef.Execute(context.Background(), mustJSON(t, map[string]any{
		"file_path": path,
		"old_text":  "return nothing here",
		"new_text":  "return errors.New(\"x\")",
	}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "must be unique") {
		t.Fatalf("expected uniqueness error, got: %s", res.Content)
	}
}

// TestBuildFixExcerpt: the excerpt is numbered, centered on the nearest
// region, and bounded.
func TestBuildFixExcerpt(t *testing.T) {
	var b strings.Builder
	b.WriteString("package main\n")
	for i := 0; i < 1000; i++ {
		b.WriteString("// filler line\n")
	}
	b.WriteString("\ttarget := uniqueAnchor(x)\n")
	content := b.String()
	excerpt := buildFixExcerpt(content, "uniqueAnchor(x)")
	if !strings.Contains(excerpt, "uniqueAnchor") {
		t.Fatalf("excerpt must contain the nearest region")
	}
	if !strings.Contains(excerpt, "lines ") {
		t.Fatalf("excerpt must carry line-range header, got: %.80s", excerpt)
	}
	if len(excerpt) > 40*1024 {
		t.Fatalf("excerpt too large: %d bytes", len(excerpt))
	}
}
