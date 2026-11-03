package knight

import (
	"os"
	"strings"
	"testing"
)

func TestCheckDuplicateSubstringFalsePositive(t *testing.T) {

	existing := []*SkillEntry{{
		Scope: "project", Name: "go-test-compress",
		Meta: SkillMeta{Description: "Compress build output when running go test and capture failures with retry logic for flaky tests"},
	}}
	candidate := &SkillEntry{
		Scope: "project", Name: "flake-reporter", Staging: true,
		Meta: SkillMeta{Description: "capture failures with retry"},
	}
	if CheckDuplicate(candidate, existing) {
		t.Fatal("#1271: embedded short phrase must not count as duplicate (one-way substring was too loose)")
	}
}

func TestCheckDuplicateNearVerbatimStillCaught(t *testing.T) {
	base := "Automatically compress build output when running go test and capture failures"
	existing := []*SkillEntry{{
		Scope: "project", Name: "build-compress",
		Meta: SkillMeta{Description: base},
	}}

	verbatim := &SkillEntry{
		Scope: "project", Name: "compress-v2", Staging: true,
		Meta: SkillMeta{Description: strings.ToUpper(base)},
	}
	if !CheckDuplicate(verbatim, existing) {
		t.Fatal("verbatim same description (case-insensitive) must remain a duplicate")
	}

	near := &SkillEntry{
		Scope: "project", Name: "compress-v3", Staging: true,
		Meta: SkillMeta{Description: "Automatically compress build output when running go build and capture failures"},
	}
	if !CheckDuplicate(near, existing) {
		t.Fatal("near-verbatim (one word changed) must remain a duplicate")
	}

	superset := &SkillEntry{
		Scope: "project", Name: "full-ci-reporter", Staging: true,
		Meta: SkillMeta{Description: base + " then publish a summary report to the team channel with flake statistics"},
	}
	if CheckDuplicate(superset, existing) {
		t.Fatal("superset description with substantial new content must not be a duplicate")
	}
}

func TestSkillScenarioLogRotatesOnWrite(t *testing.T) {
	proj := t.TempDir()
	k := &Knight{projDir: proj}
	big := strings.Repeat("x", maxSkillScenarioTaskLen)

	for i := 0; i < 700; i++ {
		if err := k.appendSkillScenario(SkillScenarioLogEntry{
			Task:    big + "-marker-" + itoa(i),
			Success: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(k.skillScenarioLogPath())
	if err != nil {
		t.Fatal(err)
	}

	if info.Size() > skillScenarioRotateBytes {
		t.Fatalf("#1270: log grew past rotate threshold without compaction: %d bytes", info.Size())
	}

	entries, err := k.RecentSkillScenarios(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no scenarios after rotation")
	}
	if !strings.Contains(entries[0].Task, "marker-699") {
		t.Fatalf("newest entry lost after rotation: newest task tail = %q", tailOf(entries[0].Task, 40))
	}

	two, err := k.RecentSkillScenarios(2)
	if err != nil || len(two) != 2 {
		t.Fatalf("expected 2 entries, got %d (err=%v)", len(two), err)
	}
	if !strings.Contains(two[1].Task, "marker-698") {
		t.Fatalf("second-newest entry wrong after rotation: %q", tailOf(two[1].Task, 40))
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

func tailOf(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
