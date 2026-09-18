package memory

// #2520: the duplicate-check exact-match branch compared disk keys against
// sanitizeKey(key), but disk keys are disambiguateKey(key, sanitizeKey(key))
// outputs - sanitize-colliding keys (CJK/spaces/dots) carry a hash suffix and
// never matched. Saving such a key twice reported similarity 0.00 instead of
// the 1.0 duplicate it was. Same fix shape as #1280 (contradiction_check).

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckDuplicateCJKKeyExactMatch(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}

	// A CJK key: sanitizeKey("构建流程") collides into an empty-ish folded
	// form, so the disk filename is untitled-<hash>.md (disambiguated).
	key := "构建流程"
	content := "记忆内容：先格式化再测试"
	if err := am.SaveMemory(key, content); err != nil {
		t.Fatal(err)
	}
	disk := disambiguateKey(key, sanitizeKey(key))
	if _, err := os.Stat(filepath.Join(dir, disk+".md")); err != nil {
		t.Fatalf("expected disk key %q: %v", disk, err)
	}

	// The exact same key saved again must report a 1.0 duplicate (#2520);
	// before the fix the hash-suffixed disk key never matched and the
	// similarity fell back to fuzzy (0.00 for pure CJK tokens).
	got := am.CheckDuplicate(key, content)
	if got.SimilarTo != disk {
		t.Fatalf("SimilarTo = %q, want disk key %q", got.SimilarTo, disk)
	}
	if got.Similarity != 1.0 {
		t.Fatalf("Similarity = %v, want 1.0 for an exact key match (#2520)", got.Similarity)
	}
}
