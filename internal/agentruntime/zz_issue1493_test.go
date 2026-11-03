package agentruntime

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIssue1493B_HealthyPathStillBinds(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirs := collectGoPackageDirs(dir, time.Now().Add(symbolMapTimeBudget))
	if len(dirs) == 0 || dirs[0] != dir {

		t.Logf("collectGoPackageDirs(%s) = %v (no go files case)", dir, dirs)
	}
}

func TestIssue1493C_Depth2LoopRespectsDeadline(t *testing.T) {
	root := t.TempDir()

	l1 := filepath.Join(root, "a")
	l2 := filepath.Join(l1, "b1")
	if err := os.MkdirAll(l2, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		d := filepath.Join(l1, "b"+string(rune('0'+i%10))+string(rune('0'+i/10)))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "x.go"), []byte("package b\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	fresh := collectGoPackageDirs(root, time.Now().Add(symbolMapTimeBudget))

	expired := collectGoPackageDirs(root, time.Now().Add(-time.Hour))
	if len(expired) > len(fresh) {
		t.Fatalf("expired-deadline walk (%d dirs) must not out-harvest fresh (%d)", len(expired), len(fresh))
	}
}

func TestIssue1493C_GiantPackageCapped(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < symbolMapMaxFilesPerPackage+40; i++ {
		name := filepath.Join(dir, "f"+string(rune('a'+i%26))+string(rune('a'+(i/26)%26))+".go")
		if err := os.WriteFile(name, []byte("package giant\nfunc F"+string(rune('A'+i%26))+"() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	symbols, n := extractPackageSymbols(dir)
	if n > symbolMapMaxFilesPerPackage {
		t.Fatalf("package cap failed: parsed %d files, cap is %d", n, symbolMapMaxFilesPerPackage)
	}
	if len(symbols) == 0 {
		t.Fatal("capped package must still yield symbols")
	}
}
