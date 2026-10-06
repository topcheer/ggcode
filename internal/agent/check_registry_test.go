package agent

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDetectLanguage(t *testing.T) {
	cases := []struct {
		path string
		want Language
	}{
		{"main.go", LangGo},
		{"test.py", LangPython},
		{"app.tsx", LangJSTS},
		{"index.html", LangMarkup},
		{"config.yaml", LangConfig},
		{"Makefile", LangAny},
		{"/path/to/file.go", LangGo},
		{"data.jsonc", LangConfig},
	}
	for _, c := range cases {
		got := detectLanguage(c.path)
		if got != c.want {
			t.Errorf("detectLanguage(%q) = %d, want %d", c.path, got, c.want)
		}
	}
}

func TestIntegrityCheck_AppliesTo(t *testing.T) {
	checks := []struct {
		langs []Language
		query Language
		want  bool
	}{
		{nil, LangGo, true}, // no filter => all
		{[]Language{LangGo}, LangGo, true},
		{[]Language{LangGo}, LangPython, false},
		{[]Language{LangGo, LangJSTS}, LangJSTS, true},
		{[]Language{LangGo, LangJSTS}, LangPython, false},
	}
	for _, c := range checks {
		ck := IntegrityCheck{Langs: c.langs}
		got := ck.appliesTo(c.query)
		if got != c.want {
			t.Errorf("appliesTo(%d) with langs %v = %v, want %v", c.query, c.langs, got, c.want)
		}
	}
}

func TestRunChecksParallel_PanicRecovery(t *testing.T) {
	// Save and restore allChecks
	saved := allChecks
	t.Cleanup(func() { allChecks = saved })

	allChecks = []IntegrityCheck{
		{Name: "panicker", Run: func(ctx CheckContext) []string {
			panic("boom")
		}},
		{Name: "healthy", Run: func(ctx CheckContext) []string {
			return []string{"healthy warning"}
		}},
	}

	ctx := CheckContext{Lang: LangAny}
	warnings := runChecksParallel(ctx)

	if len(warnings) != 1 || warnings[0] != "healthy warning" {
		t.Errorf("expected only healthy warning, got %v", warnings)
	}
}

func TestRunChecksParallel_LanguageFilter(t *testing.T) {
	saved := allChecks
	t.Cleanup(func() { allChecks = saved })

	goRan := false
	pyRan := false

	allChecks = []IntegrityCheck{
		{Name: "go-only", Langs: []Language{LangGo}, Run: func(ctx CheckContext) []string {
			goRan = true
			return nil
		}},
		{Name: "py-only", Langs: []Language{LangPython}, Run: func(ctx CheckContext) []string {
			pyRan = true
			return nil
		}},
	}

	ctx := CheckContext{Lang: LangGo}
	runChecksParallel(ctx)

	if !goRan {
		t.Error("Go check should have run for LangGo")
	}
	if pyRan {
		t.Error("Python check should NOT have run for LangGo")
	}
}

func TestRunChecksParallel_ConcurrentSafety(t *testing.T) {
	saved := allChecks
	t.Cleanup(func() { allChecks = saved })

	var counter int32
	var mu sync.Mutex

	// Register many checks that increment a shared counter
	allChecks = make([]IntegrityCheck, 20)
	for i := range allChecks {
		allChecks[i] = IntegrityCheck{
			Name: "concurrent-check",
			Run: func(ctx CheckContext) []string {
				mu.Lock()
				counter++
				mu.Unlock()
				return nil
			},
		}
	}

	ctx := CheckContext{Lang: LangAny}
	runChecksParallel(ctx)

	if counter != 20 {
		t.Errorf("expected 20 check executions, got %d", counter)
	}
}

func TestRunChecksParallel_DeterministicOrder(t *testing.T) {
	saved := allChecks
	t.Cleanup(func() { allChecks = saved })

	allChecks = []IntegrityCheck{
		{Name: "a", Run: func(ctx CheckContext) []string { return []string{"a"} }},
		{Name: "b", Run: func(ctx CheckContext) []string { return []string{"b"} }},
		{Name: "c", Run: func(ctx CheckContext) []string { return []string{"c"} }},
	}

	ctx := CheckContext{Lang: LangAny}
	// Run multiple times - order must be deterministic despite parallel execution
	for i := 0; i < 10; i++ {
		warnings := runChecksParallel(ctx)
		if len(warnings) != 3 {
			t.Fatalf("run %d: expected 3 warnings, got %d", i, len(warnings))
		}
		expected := []string{"a", "b", "c"}
		for j, w := range warnings {
			if w != expected[j] {
				t.Errorf("run %d: warning[%d] = %q, want %q", i, j, w, expected[j])
			}
		}
	}
}

func TestRunChecksParallel_HungCheckTimeout(t *testing.T) {
	saved := allChecks
	t.Cleanup(func() { allChecks = saved })

	// block is never closed during the run, simulating a wedged check
	// (pathological input, blocked external command).
	block := make(chan struct{})
	allChecks = []IntegrityCheck{
		{Name: "hung", Run: func(ctx CheckContext) []string {
			<-block
			return []string{"never"}
		}},
		{Name: "healthy", Run: func(ctx CheckContext) []string {
			return []string{"healthy warning"}
		}},
	}

	ctx := CheckContext{Lang: LangAny}
	start := time.Now()
	warnings := runChecksParallel(ctx)
	elapsed := time.Since(start)

	// The hung check must be skipped, not suppress the healthy one.
	if len(warnings) != 1 || warnings[0] != "healthy warning" {
		t.Errorf("expected only healthy warning, got %v", warnings)
	}
	// The runner must give up on the hung check within roughly one timeout
	// budget instead of stalling forever. Margin covers scheduling jitter.
	if budget := checkTimeout + 2*time.Second; elapsed > budget {
		t.Errorf("runChecksParallel blocked %v on a hung check (budget %v); timeout guard failed", elapsed, budget)
	}
	// Release the wedged goroutine so the test leaves nothing behind.
	close(block)
}

func TestFormatWarnings_Cap(t *testing.T) {
	// More warnings than maxIntegrityWarnings: keep the most critical
	// (first, per upstream severity ordering) plus one suppression indicator
	// with an accurate suppressed count.
	warnings := []string{"w1", "w2", "w3", "w4", "w5"}
	result := formatWarnings(warnings)

	lines := strings.Split(result, "\n")
	if len(lines) < 2 || lines[0] != "[Post-write integrity check]" {
		t.Fatalf("expected header first, got %q", result)
	}
	body := lines[1:]

	indicator := fmt.Sprintf("... and %d more integrity warning(s) suppressed (see debug log: integrity)",
		len(warnings)-maxIntegrityWarnings)
	substantive, indicators := 0, 0
	for _, l := range body {
		if l == indicator {
			indicators++
			continue
		}
		substantive++
	}
	if substantive != maxIntegrityWarnings {
		t.Errorf("expected %d substantive warnings, got %d: %q", maxIntegrityWarnings, substantive, result)
	}
	if indicators != 1 {
		t.Errorf("expected exactly 1 suppression indicator, got %d: %q", indicators, result)
	}
	// The cap must keep the first (highest-severity) warning, not a later one.
	if body[0] != "w1" {
		t.Errorf("expected first warning w1 to survive the cap, got %q", body[0])
	}
}

func TestFormatWarnings_ExactCapNoIndicator(t *testing.T) {
	// At exactly maxIntegrityWarnings nothing is suppressed: no indicator line.
	result := formatWarnings([]string{"w1"})
	if strings.Contains(result, "suppressed") {
		t.Errorf("unexpected suppression indicator at exactly maxIntegrityWarnings: %q", result)
	}
}

func TestFormatWarnings_Empty(t *testing.T) {
	result := formatWarnings(nil)
	if result != "" {
		t.Errorf("expected empty string for nil warnings, got %q", result)
	}
}

func TestNewCheckContext_GoAST(t *testing.T) {
	ctx := newCheckContext("test.go", "", "package main\n")
	if ctx.GoAST == nil {
		t.Error("expected GoAST to be parsed for .go file")
	}
	if ctx.GoFset == nil {
		t.Error("expected GoFset to be set for .go file")
	}
	if ctx.Lang != LangGo {
		t.Errorf("expected LangGo, got %d", ctx.Lang)
	}
}

func TestNewCheckContext_NonGoNoAST(t *testing.T) {
	ctx := newCheckContext("test.py", "", "print('hello')\n")
	if ctx.GoAST != nil {
		t.Error("expected GoAST to be nil for .py file")
	}
	if ctx.Lang != LangPython {
		t.Errorf("expected LangPython, got %d", ctx.Lang)
	}
}

func TestNewCheckContext_GoSyntaxError(t *testing.T) {
	// Invalid Go code - AST should be nil but language still detected
	ctx := newCheckContext("test.go", "", "package main\n\nfunc broken( {\n")
	if ctx.GoAST != nil {
		t.Error("expected GoAST to be nil for invalid Go code")
	}
	if ctx.Lang != LangGo {
		t.Errorf("expected LangGo even with syntax error, got %d", ctx.Lang)
	}
}

func TestAllChecksRegistered(t *testing.T) {
	// Verify that checks are registered (24 after reduction to critical-only)
	if len(allChecks) < 20 {
		t.Errorf("expected at least 20 registered checks, got %d", len(allChecks))
	}

	// #1020: a length floor cannot detect a specific check silently going
	// missing (fc5c4aad stripped regex-loop/nplus1-loop undetected for 15
	// days). Pin the names whose absence would resurrect that failure mode.
	mustRegistered := []string{
		"regex-loop", "nplus1-loop", // #1020 revival
		"binary-corruption", "merge-conflict-markers", // #601 delta gates
		"nil-map-write", "append-ignored", // #499 revival class
		"magic-numbers", "map-prealloc", "missing-prealloc", // #1102 wired orphans
	}
	for _, name := range mustRegistered {
		found := false
		for _, c := range allChecks {
			if c.Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("check %q is not registered (silent-strip regression)", name)
		}
	}

	// Verify no duplicate names
	seen := make(map[string]bool)
	for _, c := range allChecks {
		if seen[c.Name] {
			t.Errorf("duplicate check name: %q", c.Name)
		}
		seen[c.Name] = true
	}
}
