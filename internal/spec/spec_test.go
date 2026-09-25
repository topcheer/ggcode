package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeSlug(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{"My Feature!", "my-feature", false},
		{"  spaced--out  ", "spaced-out", false},
		{"r87_spec///test", "r87-spec-test", false},
		{"!!!", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := SanitizeSlug(c.in)
		if c.wantErr && err == nil {
			t.Errorf("SanitizeSlug(%q): want error, got %q", c.in, got)
			continue
		}
		if !c.wantErr {
			if err != nil {
				t.Errorf("SanitizeSlug(%q): unexpected error %v", c.in, err)
				continue
			}
			if got != c.want {
				t.Errorf("SanitizeSlug(%q) = %q, want %q", c.in, got, c.want)
			}
		}
	}
	long := strings.Repeat("a", maxSlugLen+20)
	got, err := SanitizeSlug(long)
	if err != nil || len(got) != maxSlugLen {
		t.Errorf("long slug not bounded: len=%d err=%v", len(got), err)
	}
}

func TestCreateAndLoadAll(t *testing.T) {
	wd := t.TempDir()
	specs, err := LoadAll(wd)
	if err != nil || len(specs) != 0 {
		t.Fatalf("empty workspace: specs=%v err=%v", specs, err)
	}
	s, err := Create(wd, "Login Flow!", "User Login")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if s.Slug != "login-flow" {
		t.Errorf("slug = %q", s.Slug)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "requirements.md")); err != nil {
		t.Errorf("requirements.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "tasks.md")); err != nil {
		t.Errorf("tasks.md missing: %v", err)
	}
	if _, err := Create(wd, "login-flow", "x"); err == nil {
		t.Error("duplicate Create should fail")
	}
	if _, err := Create(wd, "api-retry", "API Retry"); err != nil {
		t.Fatalf("Create 2: %v", err)
	}
	specs, err = LoadAll(wd)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(specs) != 2 || specs[0].Slug != "api-retry" || specs[1].Slug != "login-flow" {
		t.Errorf("LoadAll = %v", specs)
	}
	if specs[1].Title != "User Login" {
		t.Errorf("title = %q", specs[1].Title)
	}
	if _, err := Create(wd, "!!!", ""); err == nil {
		t.Error("Create with unusable slug should fail")
	}
	if _, err := Create("", "x", ""); err == nil {
		t.Error("Create with empty workingDir should fail")
	}
}

func TestActiveRoundtrip(t *testing.T) {
	wd := t.TempDir()
	if Active(wd) != nil {
		t.Error("no active spec expected initially")
	}
	if err := SetActive(wd, "ghost"); err == nil {
		t.Error("SetActive on missing spec should fail")
	}
	if _, err := Create(wd, "feat-a", "A"); err != nil {
		t.Fatal(err)
	}
	if err := SetActive(wd, "feat-a"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	act := Active(wd)
	if act == nil || act.Slug != "feat-a" {
		t.Fatalf("Active = %+v", act)
	}
	p := filepath.Join(wd, stateDirName, stateFileName)
	if err := os.WriteFile(p, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if Active(wd) != nil {
		t.Error("corrupt state should deactivate")
	}
	if err := ClearActive(wd); err != nil {
		t.Fatalf("ClearActive: %v", err)
	}
	if err := SetActive(wd, "feat-a"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(wd, specsDirName, "feat-a")); err != nil {
		t.Fatal(err)
	}
	if Active(wd) != nil {
		t.Error("deleted spec dir should deactivate")
	}
}

func TestProgressAndInjection(t *testing.T) {
	wd := t.TempDir()
	if PromptInjection(wd) != "" {
		t.Error("injection must be empty without active spec")
	}
	if PromptInjection("") != "" {
		t.Error("injection must be empty for empty workingDir")
	}
	s, err := Create(wd, "bench", "Bench")
	if err != nil {
		t.Fatal(err)
	}
	tasks := "# Tasks\n\n- [x] done thing\n- [ ] first todo\n- [ ] second todo\n"
	if err := os.WriteFile(filepath.Join(s.Dir, "tasks.md"), []byte(tasks), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetActive(wd, "bench"); err != nil {
		t.Fatal(err)
	}
	act := Active(wd)
	p := act.Progress()
	if p.Done != 1 || p.Total != 3 {
		t.Errorf("Progress = %+v, want 1/3", p)
	}
	inj := PromptInjection(wd)
	if !strings.Contains(inj, "specs"+string(filepath.Separator)+"bench") || !strings.Contains(inj, "1/3") {
		t.Errorf("injection missing slug/progress:\n%s", inj)
	}
	if !strings.Contains(inj, "first todo") || !strings.Contains(inj, "second todo") {
		t.Errorf("injection missing unchecked items:\n%s", inj)
	}
	if strings.Contains(inj, "done thing") {
		t.Errorf("injection should not list checked items:\n%s", inj)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "tasks.md"), []byte("# Tasks\n\n- [x] a\n- [x] b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inj = PromptInjection(wd)
	if !strings.Contains(inj, "2/2") {
		t.Errorf("completed injection missing progress:\n%s", inj)
	}
	if strings.Contains(inj, "Next unchecked") {
		t.Errorf("completed injection should not list unchecked items:\n%s", inj)
	}
}

func TestInjectionBounded(t *testing.T) {
	wd := t.TempDir()
	s, err := Create(wd, "many", "Many")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("# Tasks\n\n")
	for i := 0; i < 50; i++ {
		b.WriteString("- [ ] unchecked item number with some verbose description text\n")
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "tasks.md"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetActive(wd, "many"); err != nil {
		t.Fatal(err)
	}
	inj := PromptInjection(wd)
	if got := strings.Count(inj, "unchecked item number"); got != maxUnchecked {
		t.Errorf("unchecked items listed = %d, want %d", got, maxUnchecked)
	}
}
