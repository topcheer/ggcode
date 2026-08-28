package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/codehealth"
)

func TestComplexityGate_NoGoFiles(t *testing.T) {
	a := &Agent{complexityGate: newComplexityGateState()}
	stats := &RunStats{
		FilesEdited: []string{"foo.py", "bar.ts"},
	}
	if msg := a.checkComplexityGate(stats); msg != "" {
		t.Fatalf("expected empty message for non-Go files, got: %s", msg)
	}
}

func TestComplexityGate_FireCapReached(t *testing.T) {
	a := &Agent{
		complexityGate: &complexityGateState{fires: maxComplexityGateFires},
	}
	stats := &RunStats{
		FilesEdited: []string{"some.go"},
	}
	if msg := a.checkComplexityGate(stats); msg != "" {
		t.Fatalf("expected empty when fire cap reached, got: %s", msg)
	}
}

func TestComplexityGate_NoFiles(t *testing.T) {
	a := &Agent{complexityGate: newComplexityGateState()}
	stats := &RunStats{}
	if msg := a.checkComplexityGate(stats); msg != "" {
		t.Fatalf("expected empty for no files edited, got: %s", msg)
	}
}

func TestComplexityGate_HighComplexityDetected(t *testing.T) {
	tmp := t.TempDir()
	goFile := filepath.Join(tmp, "complex.go")

	// Write a Go file with a high-complexity function (many if-statements).
	content := `package testpkg

func ComplexFunc(a, b, c, d int) int {
	if a > 0 && b > 0 || c > 0 {
		return 1
	}
	if a < 0 && b < 0 {
		return 2
	}
	if c < 0 || d < 0 {
		return 3
	}
	if a == b && b == c {
		return 4
	}
	if c == d || a == d {
		return 5
	}
	if a > 100 {
		return 6
	}
	if b > 100 {
		return 7
	}
	if c > 100 {
		return 8
	}
	if d > 100 {
		return 9
	}
	if d > 200 && a > 200 {
		return 10
	}
	if d < 0 && b < 0 {
		return 11
	}
	return 0
}
`
	if err := os.WriteFile(goFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	a := &Agent{
		complexityGate: newComplexityGateState(),
		workingDir:     tmp,
	}
	stats := &RunStats{
		FilesEdited: []string{goFile},
	}

	msg := a.checkComplexityGate(stats)
	if msg == "" {
		t.Fatal("expected non-empty message for high-complexity function")
	}
	if a.complexityGate.fires != 1 {
		t.Fatalf("expected gate to have fired once, got %d", a.complexityGate.fires)
	}
	// Should mention the function name or file.
	if !contains(msg, "ComplexFunc") && !contains(msg, "complex.go") {
		t.Errorf("message should mention function or file, got: %s", msg)
	}
}

func TestComplexityGate_LowComplexityNoWarning(t *testing.T) {
	tmp := t.TempDir()
	goFile := filepath.Join(tmp, "simple.go")

	content := `package testpkg

func SimpleFunc(a int) int {
	return a + 1
}
`
	if err := os.WriteFile(goFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	a := &Agent{
		complexityGate: newComplexityGateState(),
		workingDir:     tmp,
	}
	stats := &RunStats{
		FilesEdited: []string{goFile},
	}

	msg := a.checkComplexityGate(stats)
	if msg != "" {
		t.Fatalf("expected empty for simple function, got: %s", msg)
	}
	if a.complexityGate.fires != 0 {
		t.Fatal("gate should not fire for simple function")
	}
}

func TestComplexityGate_FiresOnlyOncePerFunction(t *testing.T) {
	tmp := t.TempDir()
	goFile := filepath.Join(tmp, "complex.go")

	writeComplexFunc(t, goFile)

	a := &Agent{
		complexityGate: newComplexityGateState(),
		workingDir:     tmp,
	}
	stats := &RunStats{
		FilesEdited: []string{goFile},
	}

	msg1 := a.checkComplexityGate(stats)
	if msg1 == "" {
		t.Fatal("expected non-empty on first call")
	}

	msg2 := a.checkComplexityGate(stats)
	if msg2 != "" {
		t.Fatal("expected empty on second call (same hotspot already reported)")
	}
}

// TestComplexityGate_NewRegressionInOtherFileStillFires covers #1202 defect 2:
// the old global one-shot fired flag suppressed ALL later advisories; the
// per-function dedup must let a new regression in a different file fire.
func TestComplexityGate_NewRegressionInOtherFileStillFires(t *testing.T) {
	tmp := t.TempDir()
	fileA := filepath.Join(tmp, "a.go")
	fileB := filepath.Join(tmp, "b.go")

	writeComplexFunc(t, fileA)
	writeComplexFunc(t, fileB)

	a := &Agent{
		complexityGate: newComplexityGateState(),
		workingDir:     tmp,
	}

	msg1 := a.checkComplexityGate(&RunStats{FilesEdited: []string{fileA}})
	if msg1 == "" {
		t.Fatal("expected advisory on first regression")
	}
	if a.complexityGate.fires != 1 {
		t.Fatalf("expected 1 fire, got %d", a.complexityGate.fires)
	}

	msg2 := a.checkComplexityGate(&RunStats{FilesEdited: []string{fileB}})
	if msg2 == "" {
		t.Fatal("expected advisory for NEW regression in a different file (#1202: fired flag suppressed later real regressions)")
	}
	if a.complexityGate.fires != 2 {
		t.Fatalf("expected 2 fires, got %d", a.complexityGate.fires)
	}
}

// TestComplexityGate_LegacyHotspotNotBlamed covers #1202 defect 1: a hotspot
// unchanged relative to its baseline (pre-existing legacy code the agent did
// not touch) must NOT be reported.
func TestComplexityGate_LegacyHotspotNotBlamed(t *testing.T) {
	tmp := t.TempDir()
	goFile := filepath.Join(tmp, "legacy.go")

	content := complexGateSample(t, tmp, "legacy.go")

	// Baseline identical to current content: function pre-exists unchanged.
	restore := injectComplexityBaseline(func(absPath, workingDir string) (map[string]codehealth.FuncMetrics, bool) {
		report, err := codehealth.AnalyzeSource(absPath, []byte(content), codehealth.Options{
			ThresholdComplexity: complexityGateAnalyzeThreshold,
			MaxFunctions:        complexityGateAnalyzeMaxFuncs,
		})
		if err != nil {
			t.Fatalf("baseline parse failed: %v", err)
		}
		m := make(map[string]codehealth.FuncMetrics, len(report.TopFunctions))
		for _, fn := range report.TopFunctions {
			m[fn.Function] = fn
		}
		return m, true
	})
	defer restore()

	a := &Agent{
		complexityGate: newComplexityGateState(),
		workingDir:     tmp,
	}
	msg := a.checkComplexityGate(&RunStats{FilesEdited: []string{goFile}})
	if msg != "" {
		t.Fatalf("expected no advisory for unchanged legacy hotspot, got: %s", msg)
	}
}

// TestComplexityGate_WorsenedHotspotReported ensures the baseline comparison
// still catches a function the agent made worse.
func TestComplexityGate_WorsenedHotspotReported(t *testing.T) {
	tmp := t.TempDir()
	goFile := filepath.Join(tmp, "worse.go")

	// Current content: complexity-25 function.
	writeComplexFunc(t, goFile)

	// Baseline: the same function was simple before the agent's edit.
	baselineSrc := `package testpkg

func ComplexFunc(a, b, c, d int) int {
	return 0
}
`
	restore := injectComplexityBaseline(func(absPath, workingDir string) (map[string]codehealth.FuncMetrics, bool) {
		report, err := codehealth.AnalyzeSource(absPath, []byte(baselineSrc), codehealth.Options{
			ThresholdComplexity: complexityGateAnalyzeThreshold,
			MaxFunctions:        complexityGateAnalyzeMaxFuncs,
		})
		if err != nil {
			t.Fatalf("baseline parse failed: %v", err)
		}
		m := make(map[string]codehealth.FuncMetrics, len(report.TopFunctions))
		for _, fn := range report.TopFunctions {
			m[fn.Function] = fn
		}
		return m, true
	})
	defer restore()

	a := &Agent{
		complexityGate: newComplexityGateState(),
		workingDir:     tmp,
	}
	msg := a.checkComplexityGate(&RunStats{FilesEdited: []string{goFile}})
	if msg == "" {
		t.Fatal("expected advisory for worsened function")
	}
	if !strings.Contains(msg, "ComplexFunc") {
		t.Errorf("advisory should name the worsened function, got: %s", msg)
	}
}

// TestComplexityGate_GeneratedFilesSkipped covers #1202 defect 3: advisories
// must not fire for generated code (.pb.go suffix and "Code generated" marker).
func TestComplexityGate_GeneratedFilesSkipped(t *testing.T) {
	tmp := t.TempDir()
	pbFile := filepath.Join(tmp, "api.pb.go")
	markerFile := filepath.Join(tmp, "gen_types.go")

	content := complexGateSample(t, tmp, "unused.go")
	if err := os.WriteFile(pbFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	markerContent := "// Code generated by protoc-gen-go. DO NOT EDIT.\n" + content
	if err := os.WriteFile(markerFile, []byte(markerContent), 0644); err != nil {
		t.Fatal(err)
	}

	a := &Agent{
		complexityGate: newComplexityGateState(),
		workingDir:     tmp,
	}
	msg := a.checkComplexityGate(&RunStats{FilesEdited: []string{pbFile, markerFile}})
	if msg != "" {
		t.Fatalf("expected no advisory for generated files, got: %s", msg)
	}
}

// TestComplexityGate_LengthOnlyHotspotDetected covers #1202 defect 4: a
// long-but-simple function (complexity far below the threshold, length > 120)
// must be detected now that the analyze threshold no longer pre-filters it out.
func TestComplexityGate_LengthOnlyHotspotDetected(t *testing.T) {
	tmp := t.TempDir()
	goFile := filepath.Join(tmp, "long.go")

	var b strings.Builder
	b.WriteString("package testpkg\n\nfunc LongFunc() int {\n\tx := 0\n")
	for i := 0; i < 150; i++ {
		b.WriteString("\tx += 1\n")
	}
	b.WriteString("\treturn x\n}\n")

	if err := os.WriteFile(goFile, []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}

	a := &Agent{
		complexityGate: newComplexityGateState(),
		workingDir:     tmp,
	}
	msg := a.checkComplexityGate(&RunStats{FilesEdited: []string{goFile}})
	if msg == "" {
		t.Fatal("expected advisory for length-only hotspot (low complexity, >120 lines)")
	}
	if !strings.Contains(msg, "length=") {
		t.Errorf("advisory should report the length dimension, got: %s", msg)
	}
}

func TestComplexityGate_TestFilesSkipped(t *testing.T) {
	a := &Agent{complexityGate: newComplexityGateState()}
	stats := &RunStats{
		FilesEdited: []string{"foo_test.go"},
	}
	if msg := a.checkComplexityGate(stats); msg != "" {
		t.Fatalf("expected empty for test files, got: %s", msg)
	}
}

// TestGitBaselineMetrics_TrackedFile exercises the real git baseline path
// against this repository: the gate's own source file is tracked, so a
// baseline must be available and non-empty.
func TestGitBaselineMetrics_TrackedFile(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	absPath := filepath.Join(wd, "complexity_gate.go")
	baseline, ok := gitBaselineMetrics(absPath, wd)
	if !ok {
		t.Fatal("expected baseline to be available for a tracked file in a git repo")
	}
	if len(baseline) == 0 {
		t.Fatal("expected non-empty baseline for complexity_gate.go")
	}
	found := false
	for name := range baseline {
		if strings.Contains(name, "checkComplexityGate") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected checkComplexityGate in baseline, have %d functions: %v", len(baseline), baseline)
	}
}

// TestGitBaselineMetrics_RepoMetadataEdge checks that the "exists on disk but
// not in HEAD" wording used to classify untracked files matches reality for a
// genuinely untracked temp file inside a git repo.
func TestGitBaselineMetrics_UntrackedFileInRepo(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	untracked := filepath.Join(wd, "zz_tmp_untracked_gate_fixture.go")
	if err := os.WriteFile(untracked, []byte("package agent\n"), 0644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(untracked)

	baseline, ok := gitBaselineMetrics(untracked, wd)
	if !ok {
		t.Fatal("expected available (empty) baseline for untracked file, got unavailable")
	}
	if len(baseline) != 0 {
		t.Fatalf("expected empty baseline for untracked file, got %d funcs", len(baseline))
	}
}

func TestFilterGoSourceFiles(t *testing.T) {
	input := []string{
		"foo.go",
		"bar_test.go",
		"baz.py",
		"qux.ts",
		"deep/nested.go",
		"skip_test.go",
	}
	result := filterGoSourceFiles(input)
	expected := []string{"foo.go", "deep/nested.go"}
	if len(result) != len(expected) {
		t.Fatalf("expected %d files, got %d: %v", len(expected), len(result), result)
	}
	for i, e := range expected {
		if result[i] != e {
			t.Errorf("expected[%d]=%s, got %s", i, e, result[i])
		}
	}
}

func TestIsComplexityHotspot(t *testing.T) {
	tests := []struct {
		name     string
		complex  int
		length   int
		nesting  int
		expected bool
	}{
		{"healthy", 5, 20, 2, false},
		{"high complexity", 25, 30, 3, true},
		{"too long despite low complexity", 5, 130, 2, true},
		{"long but under", 5, 100, 2, false},
		{"too nested", 5, 20, 7, true},
		{"at complexity threshold", 20, 50, 3, true},
		{"just under all", 19, 119, 5, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := codehealth.FuncMetrics{
				Complexity:   tt.complex,
				Length:       tt.length,
				NestingDepth: tt.nesting,
			}
			if got := isComplexityHotspot(fn); got != tt.expected {
				t.Errorf("isComplexityHotspot() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// --- helpers ---

// writeComplexFunc writes a Go file containing a complexity-25 function.
func writeComplexFunc(t *testing.T, path string) {
	t.Helper()
	src := `package testpkg

func ComplexFunc(a, b, c, d int) int {
	if a > 0 && b > 0 || c > 0 {
		return 1
	}
	if a < 0 && b < 0 {
		return 2
	}
	if c < 0 || d < 0 {
		return 3
	}
	if a == b && b == c {
		return 4
	}
	if c == d || a == d {
		return 5
	}
	if a > 100 {
		return 6
	}
	if b > 100 {
		return 7
	}
	if c > 100 {
		return 8
	}
	if d > 100 {
		return 9
	}
	if d > 200 && a > 200 {
		return 10
	}
	if d < 0 && b < 0 {
		return 11
	}
	return 0
}
`
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
}

// complexGateSample writes a complexity-25 function into name and returns its
// content (for baseline comparisons).
func complexGateSample(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err != nil {
		writeComplexFunc(t, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// injectComplexityBaseline swaps the gate's baseline resolver for a test fake.
func injectComplexityBaseline(fn func(absPath, workingDir string) (map[string]codehealth.FuncMetrics, bool)) func() {
	prev := complexityBaselineFn
	complexityBaselineFn = fn
	return func() { complexityBaselineFn = prev }
}
