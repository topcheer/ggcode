package codehealth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCyclomaticComplexity(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "empty function",
			src: `package p
func foo() {}`,
			want: 1,
		},
		{
			name: "single if",
			src: `package p
func foo(x int) {
	if x > 0 {
		_ = x
	}
}`,
			want: 2,
		},
		{
			name: "if-else with for loop",
			src: `package p
func foo(x int) {
	if x > 0 {
		for i := 0; i < x; i++ {
			_ = i
		}
	} else {
		_ = x
	}
}`,
			want: 3,
		},
		{
			name: "switch with cases",
			src: `package p
func foo(x int) {
	switch x {
	case 1:
		_ = 1
	case 2:
		_ = 2
	default:
		_ = 0
	}
}`,
			want: 3, // 1 base + 2 case clauses (default doesn't add)
		},
		{
			name: "binary operators",
			src: `package p
func foo(a, b, c bool) {
	if a && b || c {
		_ = a
	}
}`,
			want: 4, // 1 base + 1 if + 1 && + 1 ||
		},
		{
			name: "range loop",
			src: `package p
func foo(items []int) {
	for i := range items {
		_ = i
	}
}`,
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "test.go")
			if err := os.WriteFile(path, []byte(tt.src), 0644); err != nil {
				t.Fatal(err)
			}
			funcs, err := analyzeFile(path)
			if err != nil {
				t.Fatalf("analyzeFile failed: %v", err)
			}
			if len(funcs) != 1 {
				t.Fatalf("expected 1 function, got %d", len(funcs))
			}
			if funcs[0].Complexity != tt.want {
				t.Errorf("complexity = %d, want %d", funcs[0].Complexity, tt.want)
			}
		})
	}
}

func TestNestingDepth(t *testing.T) {
	src := `package p
func foo(x int) {
	if x > 0 {
		if x > 10 {
			if x > 100 {
				_ = x
			}
		}
	}
}`
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	os.WriteFile(path, []byte(src), 0644)

	funcs, err := analyzeFile(path)
	if err != nil {
		t.Fatalf("analyzeFile failed: %v", err)
	}
	if len(funcs) != 1 {
		t.Fatalf("expected 1 function, got %d", len(funcs))
	}
	if funcs[0].NestingDepth != 3 {
		t.Errorf("nesting depth = %d, want 3", funcs[0].NestingDepth)
	}
}

func TestFuncName(t *testing.T) {
	src := `package p
type Foo struct{}
func (f Foo) Bar() {}
func (f *Foo) Baz() {}
func Qux() {}`
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	os.WriteFile(path, []byte(src), 0644)

	funcs, err := analyzeFile(path)
	if err != nil {
		t.Fatalf("analyzeFile failed: %v", err)
	}
	if len(funcs) != 3 {
		t.Fatalf("expected 3 functions, got %d", len(funcs))
	}
	expected := []string{"Foo.Bar", "*Foo.Baz", "Qux"}
	for i, name := range expected {
		if funcs[i].Function != name {
			t.Errorf("funcs[%d].Function = %q, want %q", i, funcs[i].Function, name)
		}
	}
}

func TestAnalyzeDirectory(t *testing.T) {
	dir := t.TempDir()

	// Write a simple file
	src := `package p
func simple() {
	_ = 1
}
func complex(x int) {
	if x > 0 {
		for i := 0; i < x; i++ {
			if i%2 == 0 && i > 5 {
				_ = i
			}
		}
	}
}`
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	// Write a generated file that should be skipped
	gen := `// Code generated DO NOT EDIT
package p
func genFunc() {}`
	if err := os.WriteFile(filepath.Join(dir, "gen.go"), []byte(gen), 0644); err != nil {
		t.Fatal(err)
	}

	// Write a non-go file that should be skipped
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hello"), 0644)

	opts := DefaultOptions()
	opts.ThresholdComplexity = 3 // lower threshold to flag the medium-complexity function
	report, err := Analyze(dir, opts)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if report.FilesScanned != 1 {
		t.Errorf("FilesScanned = %d, want 1 (generated file should be skipped)", report.FilesScanned)
	}
	if report.Functions != 2 {
		t.Errorf("Functions = %d, want 2", report.Functions)
	}
	if report.MaxComplexity < 4 {
		t.Errorf("MaxComplexity = %d, expected >= 4", report.MaxComplexity)
	}
	if len(report.TopFunctions) < 1 {
		t.Fatal("expected at least 1 flagged function")
	}
	// The complex function should be ranked first
	if report.TopFunctions[0].Function != "complex" {
		t.Errorf("expected 'complex' as top function, got %q", report.TopFunctions[0].Function)
	}
}

func TestHealthScore(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		minScore int
		maxScore int
	}{
		{
			name: "simple code - high score",
			src: `package p
func simple() {
	_ = 1
}`,
			minScore: 90,
			maxScore: 100,
		},
		{
			name: "complex code - lower score",
			src: `package p
func complex(a, b, c, d int) int {
	if a > 0 {
		if b > 0 {
			for i := 0; i < a; i++ {
				if i%2 == 0 && i > b || c > d {
					switch i {
					case 1:
						return 1
					case 2:
						return 2
					default:
						return 0
					}
				}
			}
		}
	}
	return -1
}`,
			minScore: 0,
			maxScore: 85,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, "test.go"), []byte(tt.src), 0644)
			opts := DefaultOptions()
			opts.ThresholdComplexity = 5 // lower threshold so complex functions get penalized
			report, err := Analyze(dir, opts)
			if err != nil {
				t.Fatalf("Analyze failed: %v", err)
			}
			if report.HealthScore < tt.minScore || report.HealthScore > tt.maxScore {
				t.Errorf("HealthScore = %d, want in [%d, %d]", report.HealthScore, tt.minScore, tt.maxScore)
			}
		})
	}
}

func TestExcludeDirs(t *testing.T) {
	dir := t.TempDir()

	// Main package
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package p
func main() {}`), 0644)

	// Vendor directory
	vendorDir := filepath.Join(dir, "vendor", "lib")
	os.MkdirAll(vendorDir, 0755)
	os.WriteFile(filepath.Join(vendorDir, "lib.go"), []byte(`package lib
func VendoredFunc() {}`), 0644)

	report, err := Analyze(dir, DefaultOptions())
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if report.FilesScanned != 1 {
		t.Errorf("FilesScanned = %d, want 1 (vendor should be excluded)", report.FilesScanned)
	}
}

func TestNonExistentPath(t *testing.T) {
	_, err := Analyze("/nonexistent/path/xyz123", DefaultOptions())
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}

func TestMaxFilesLimit(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		os.WriteFile(filepath.Join(dir, "file_"+string(rune('a'+i))+".go"),
			[]byte(`package p
func f() {}`), 0644)
	}

	opts := DefaultOptions()
	opts.MaxFiles = 2
	report, err := Analyze(dir, opts)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}
	if report.FilesScanned > 2 {
		t.Errorf("FilesScanned = %d, expected <= 2", report.FilesScanned)
	}
}

func TestThresholdFiltering(t *testing.T) {
	dir := t.TempDir()
	src := `package p
func simple() { _ = 1 }
func medium(x int) { if x > 0 { if x > 1 { _ = x } } }
func complex(x int) {
	for i := 0; i < x; i++ {
		if i > 0 {
			if i > 1 {
				if i > 2 {
					if i > 3 {
						_ = i
					}
				}
			}
		}
	}
}`
	os.WriteFile(filepath.Join(dir, "test.go"), []byte(src), 0644)

	opts := DefaultOptions()
	opts.ThresholdComplexity = 5
	report, err := Analyze(dir, opts)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}
	// Only medium (complexity 3) and complex (complexity 6+) should be flagged
	// Actually medium has complexity 3, so only complex should be above 5
	for _, f := range report.TopFunctions {
		if f.Complexity < 5 {
			t.Errorf("function %q has complexity %d but threshold is 5", f.Function, f.Complexity)
		}
	}
}

func TestParamCount(t *testing.T) {
	src := `package p
func noParams() {}
func twoParams(a, b int) {}
func namedParams(a, b, c string, d, e bool) {}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	os.WriteFile(path, []byte(src), 0644)

	funcs, err := analyzeFile(path)
	if err != nil {
		t.Fatalf("analyzeFile failed: %v", err)
	}
	if len(funcs) != 3 {
		t.Fatalf("expected 3 functions, got %d", len(funcs))
	}
	expected := []int{0, 2, 5}
	for i, want := range expected {
		if funcs[i].Params != want {
			t.Errorf("funcs[%d].Params = %d, want %d", i, funcs[i].Params, want)
		}
	}
}
