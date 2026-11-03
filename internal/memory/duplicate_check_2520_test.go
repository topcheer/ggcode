package memory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckDuplicateCJKKeyExactMatch(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}

	key := "构建流程"
	content := "记忆内容：先格式化再测试"
	if err := am.SaveMemory(key, content); err != nil {
		t.Fatal(err)
	}
	disk := disambiguateKey(key, sanitizeKey(key))
	if _, err := os.Stat(filepath.Join(dir, disk+".md")); err != nil {
		t.Fatalf("expected disk key %q: %v", disk, err)
	}

	got := am.CheckDuplicate(key, content)
	if got.SimilarTo != disk {
		t.Fatalf("SimilarTo = %q, want disk key %q", got.SimilarTo, disk)
	}
	if got.Similarity != 1.0 {
		t.Fatalf("Similarity = %v, want 1.0 for an exact key match (#2520)", got.Similarity)
	}
}
