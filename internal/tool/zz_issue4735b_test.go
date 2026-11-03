package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestIssue4735_TruncationTailRecordedInOverflow(t *testing.T) {
	f1 := "package alpha\nfunc betaOne() { gammaDelta := 42; _ = gammaDelta }\n"
	f2 := "package epsilon\nfunc zetaOne() { etaTheta := 7; _ = etaTheta }\n"
	f3 := "package iotaOne\nfunc kappaOne() { lambdaOne := 9; _ = lambdaOne }\n"

	dir := t.TempDir()
	for name, body := range map[string]string{"f1.go": f1, "f2.go": f2, "f3.go": f3} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tf1 := map[string]int{}
	for _, term := range tokenizeForIndex(f1) {
		tf1[term]++
	}
	if len(tf1) < 2 {
		t.Fatalf("fixture too small: f1 has %d distinct terms", len(tf1))
	}
	t.Setenv("GGCODE_CODE_INDEX_MAX_TERMS", strconv.Itoa(len(tf1)+1))

	m := NewCodeIndexManager(dir)
	m.indexPath = filepath.Join(dir, "ci.json")
	m.doBuild(t.Context())

	m.mu.RLock()
	excluded := len(m.budgetExcluded)
	indexedCount := m.stats.IndexedFiles
	m.mu.RUnlock()

	if indexedCount != 1 {
		t.Fatalf("budget must index exactly the first file, got IndexedFiles=%d", indexedCount)
	}

	if excluded != 2 {
		t.Fatalf("truncation tail must be recorded in overflow: want 2 excluded entries (f2+f3), got %d", excluded)
	}

	data, err := os.ReadFile(m.indexPath)
	if err != nil {
		t.Fatal(err)
	}
	var pi persistedIndex
	if err := json.Unmarshal(data, &pi); err != nil {
		t.Fatal(err)
	}
	if len(pi.Excluded) != 2 {
		t.Fatalf("persisted index must carry 2 excluded entries, got %d", len(pi.Excluded))
	}
}
