package memory

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestAutoMemory_SaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	am := &AutoMemory{dir: tmpDir}

	err := am.SaveMemory("test-key", "test content")
	if err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	if _, err := os.Stat(filepath.Join(tmpDir, "test-key.md")); err != nil {
		t.Fatal("file not created")
	}

	content, files, err := am.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(files) != 1 {
		t.Errorf("expected 1 file, got %d", len(files))
	}
	if content == "" {
		t.Error("expected non-empty content")
	}
}

func TestAutoMemory_LoadIndex(t *testing.T) {
	tmpDir := t.TempDir()
	am := &AutoMemory{dir: tmpDir}

	am.SaveMemory("alpha", "alpha content")
	am.SaveMemory("beta", "beta content")

	index, files, err := am.LoadIndex()
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d", len(files))
	}
	if strings.Contains(index, "alpha content") || strings.Contains(index, "beta content") {
		t.Error("LoadIndex should not include file contents")
	}
	if !strings.Contains(index, "- alpha") || !strings.Contains(index, "- beta") {
		t.Errorf("LoadIndex should list memory titles; got: %s", index)
	}
}

func TestAutoMemory_ListAndClear(t *testing.T) {
	tmpDir := t.TempDir()
	am := &AutoMemory{dir: tmpDir}

	am.SaveMemory("alpha", "a")
	am.SaveMemory("beta", "b")

	keys, err := am.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	sort.Strings(keys)
	if len(keys) != 2 || keys[0] != "alpha" || keys[1] != "beta" {
		t.Errorf("unexpected keys: %v", keys)
	}

	am.Clear()
	keys, err = am.List()
	if err != nil {
		t.Fatalf("List after clear: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected 0 keys after clear, got %d", len(keys))
	}
}

func TestSanitizeKey(t *testing.T) {
	tests := []struct{ in, want string }{
		{"hello", "hello"},
		{"Hello World!", "Hello-World"},
		{"", ""},
		{"a--b", "a-b"},
	}
	for _, tc := range tests {
		got := sanitizeKey(tc.in)
		if got != tc.want {
			t.Errorf("sanitizeKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDisambiguateKey(t *testing.T) {

	if got := disambiguateKey("build-process", sanitizeKey("build-process")); got != "build-process" {
		t.Errorf("clean key should stay readable, got %q", got)
	}

	seen := map[string]string{}
	for _, key := range []string{"a/b", "a.b", "a b", "a-b"} {
		got := disambiguateKey(key, sanitizeKey(key))
		if prev, dup := seen[got]; dup {
			t.Errorf("keys %q and %q collided on file %q", prev, key, got)
		}
		seen[got] = key
	}

	cn1 := disambiguateKey("构建流程", sanitizeKey("构建流程"))
	cn2 := disambiguateKey("发版流程", sanitizeKey("发版流程"))
	if cn1 == cn2 {
		t.Errorf("distinct CJK keys share one file %q -- silent overwrite regression", cn1)
	}
	if !strings.HasPrefix(cn1, "untitled-") {
		t.Errorf("CJK key should keep untitled-<hash> form, got %q", cn1)
	}
}

func TestSaveAndDeleteRoundTripNonInjective(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	if err := am.SaveMemory("hello world", "content-1"); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}
	if err := am.SaveMemory("release flow", "content-2"); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}
	if err := am.DeleteMemory("hello world"); err != nil {
		t.Fatalf("DeleteMemory must resolve the same file SaveMemory wrote: %v", err)
	}
	if err := am.DeleteMemory("release flow"); err != nil {
		t.Fatalf("DeleteMemory second: %v", err)
	}
}

func TestLoadKeySingleKeyIsolation(t *testing.T) {
	am := NewAutoMemory()
	dir := t.TempDir()
	am.dir = dir

	if err := am.SaveMemory("mine", "MY CONTENT ONLY"); err != nil {
		t.Fatal(err)
	}
	if err := am.SaveMemory("other", "UNRELATED MEMORY"); err != nil {
		t.Fatal(err)
	}

	got, err := am.LoadKey("mine")
	if err != nil {
		t.Fatal(err)
	}
	if got != "MY CONTENT ONLY" {
		t.Fatalf("LoadKey leaked other keys or wrong content: %q", got)
	}

	got, err = am.LoadKey("never-saved")
	if err != nil || got != "" {
		t.Fatalf("missing key should be (\"\", nil), got (%q, %v)", got, err)
	}
}
