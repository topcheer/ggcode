package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCodeIndexTermBudget (#1625): the total-term budget must truncate the
// build so a working directory that parents many repositories cannot balloon
// resident memory to tens of gigabytes.
func TestCodeIndexTermBudget(t *testing.T) {
	dir := t.TempDir()
	// 200 files, each with ~50 distinct terms => ~10k terms total.
	for i := 0; i < 200; i++ {
		content := "package p\n"
		for j := 0; j < 50; j++ {
			content += "var uniqVar" + itoa(i) + "_" + itoa(j) + " int\n"
		}
		if err := os.WriteFile(filepath.Join(dir, "f"+itoa(i)+".go"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	m := NewCodeIndexManager(dir)
	t.Setenv("GGCODE_CODE_INDEX_MAX_TERMS", "300") // tight budget: ~6 files
	defer m.Stop()

	m.doBuild(context.Background())

	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.ready {
		t.Fatal("index should be ready after build")
	}
	// Each file contributes ~50+ distinct terms; a 300-term budget must stop
	// well before all 200 files (~10k terms) are indexed.
	if got := len(m.index.docs); got > 12 {
		t.Fatalf("term budget not enforced: indexed %d docs (~%d terms), expected <= ~6 docs", got, got*51)
	}
}

// TestCodeIndexTermBudgetUnlimited: GGCODE_CODE_INDEX_MAX_TERMS < 0 restores
// legacy unbounded behavior.
func TestCodeIndexTermBudgetUnlimited(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 10; i++ {
		if err := os.WriteFile(filepath.Join(dir, "f"+itoa(i)+".go"),
			[]byte("package p\nvar uniqVar"+itoa(i)+" int\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	m := NewCodeIndexManager(dir)
	t.Setenv("GGCODE_CODE_INDEX_MAX_TERMS", "-1")
	defer m.Stop()

	m.doBuild(context.Background())

	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.index.docs) != 10 {
		t.Fatalf("unbounded mode should index all 10 files, got %d", len(m.index.docs))
	}
}

func TestCodeIndexMaxTotalTermsOverride(t *testing.T) {
	t.Setenv("GGCODE_CODE_INDEX_MAX_TERMS", "")
	if got := codeIndexMaxTotalTermsOverride(); got != codeIndexMaxTotalTerms {
		t.Fatalf("empty env: got %d want default %d", got, codeIndexMaxTotalTerms)
	}
	t.Setenv("GGCODE_CODE_INDEX_MAX_TERMS", "garbage")
	if got := codeIndexMaxTotalTermsOverride(); got != codeIndexMaxTotalTerms {
		t.Fatalf("garbage env: got %d want default %d", got, codeIndexMaxTotalTerms)
	}
	t.Setenv("GGCODE_CODE_INDEX_MAX_TERMS", "0")
	if got := codeIndexMaxTotalTermsOverride(); got != 0 {
		t.Fatalf("zero env: got %d want 0", got)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestNestedGitRepoExcluded pins the walk boundary: a nested git
// repository's sibling sources never enter the index (parent-dir start).
func TestNestedGitRepoExcluded(t *testing.T) {
	root := t.TempDir()
	inner := filepath.Join(root, "vendored-clone")
	if err := os.MkdirAll(filepath.Join(inner, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(inner, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil { // dir form (clone)
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inner, "src", "lib.go"), []byte("package inner\nfunc F() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewCodeIndexManager(root)
	files := m.collectFiles(context.Background())
	for _, f := range files {
		if strings.Contains(f, "vendored-clone") {
			t.Fatalf("nested repo file must be excluded: %s", f)
		}
	}
	found := false
	for _, f := range files {
		if strings.HasSuffix(f, "main.go") {
			found = true
		}
	}
	if !found {
		t.Fatal("root's own files must still be indexed")
	}
}
