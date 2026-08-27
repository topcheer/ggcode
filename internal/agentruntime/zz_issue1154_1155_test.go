package agentruntime

// Tests covering GitHub issues #1154 and #1155 in internal/agentruntime.
//
// #1154: SectionCollector Stop was not idempotent (bare close panicked on the
// second caller) and the package-global collector pointer was read/written
// without synchronization. The install/removal/snapshot paths are now guarded
// by globalCollectorMu and Stop uses sync.Once.
//
// #1155: Skill description truncation sliced bytes instead of runes, so CJK
// descriptions could be cut mid-rune, producing invalid UTF-8 that strict
// providers reject with HTTP 400. Truncation now goes through truncateRunes.

import (
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/commands"
)

// issue1155MaxDescChars mirrors the truncation cap used by
// BuildSkillsSystemPromptWithPromptRefs (local const there).
const issue1155MaxDescChars = 180

// findSkillLine extracts the rendered "- name: ..." entry from a skills prompt.
func findSkillLine(t *testing.T, prompt, name string) string {
	t.Helper()
	prefix := "- " + name + ": "
	for _, ln := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(ln, prefix) {
			return ln
		}
	}
	t.Fatalf("skill line %q not found in prompt:\n%s", name, prompt)
	return ""
}

// TestIssue1155SkillDescriptionTruncationRuneSafe feeds a far-over-limit CJK
// description through the skills prompt builder and asserts the truncated
// description stays valid UTF-8 and respects the rune cap (#1155). Under the
// old byte slicing, desc[:179] landed inside a 3-byte rune almost surely.
func TestIssue1155SkillDescriptionTruncationRuneSafe(t *testing.T) {
	longCJK := strings.Repeat("汉", 250) // 750 bytes, 250 runes
	skills := []*commands.Command{
		{
			Name:        "cn-heavy",
			Description: longCJK,
			Enabled:     true,
			LoadedFrom:  commands.LoadedFromSkills,
			Source:      commands.SourceProject,
		},
	}
	prompt := BuildSkillsSystemPrompt(skills)

	if !utf8.ValidString(prompt) {
		t.Fatal("skills prompt contains invalid UTF-8")
	}

	line := findSkillLine(t, prompt, "cn-heavy")
	descPart := strings.TrimPrefix(line, "- cn-heavy: ")
	if n := utf8.RuneCountInString(descPart); n != issue1155MaxDescChars {
		t.Errorf("truncated description = %d runes, want %d", n, issue1155MaxDescChars)
	}
	if !strings.HasSuffix(descPart, "...") {
		t.Errorf("truncated description missing ellipsis suffix: %q", descPart)
	}
	content := strings.TrimSuffix(descPart, "...")
	if n := utf8.RuneCountInString(content); n != issue1155MaxDescChars-3 {
		t.Errorf("content before suffix = %d runes, want %d", n, issue1155MaxDescChars-3)
	}
	if !utf8.ValidString(descPart) {
		t.Errorf("truncated description is not valid UTF-8: %q", descPart)
	}
}

// TestIssue1155WhenToUseMergeTruncationRuneSafe covers the merged
// Description+" - "+WhenToUse path crossing the cap (#1155).
func TestIssue1155WhenToUseMergeTruncationRuneSafe(t *testing.T) {
	skills := []*commands.Command{
		{
			Name:        "cn-merge",
			Description: "Short summary",
			WhenToUse:   strings.Repeat("测", 200),
			Enabled:     true,
		},
	}
	prompt := BuildSkillsSystemPrompt(skills)

	if !utf8.ValidString(prompt) {
		t.Fatal("skills prompt contains invalid UTF-8")
	}
	line := findSkillLine(t, prompt, "cn-merge")
	descPart := strings.TrimPrefix(line, "- cn-merge: ")
	if !utf8.ValidString(descPart) {
		t.Errorf("merged truncated description is not valid UTF-8: %q", descPart)
	}
	if n := utf8.RuneCountInString(descPart); n != issue1155MaxDescChars {
		t.Errorf("merged truncated description = %d runes, want %d", n, issue1155MaxDescChars)
	}
}

// TestIssue1155ASCIIDescriptionBoundary pins boundary behavior: exactly at the
// cap nothing changes; above it the result is deterministically capped at 180
// runes ending in "..." (#1155 regression guard for pure-ASCII input).
func TestIssue1155ASCIIDescriptionBoundary(t *testing.T) {
	skills := []*commands.Command{
		{Name: "ascii-exact", Description: strings.Repeat("a", issue1155MaxDescChars), Enabled: true},
		{Name: "ascii-over", Description: strings.Repeat("b", issue1155MaxDescChars+5), Enabled: true},
	}
	prompt := BuildSkillsSystemPrompt(skills)

	exactLine := findSkillLine(t, prompt, "ascii-exact")
	wantExact := "- ascii-exact: " + strings.Repeat("a", issue1155MaxDescChars)
	if exactLine != wantExact {
		t.Errorf("description at cap was modified:\n got %q\nwant %q", exactLine, wantExact)
	}
	if !utf8.ValidString(exactLine) {
		t.Error("exact-cap line is not valid UTF-8")
	}

	overLine := findSkillLine(t, prompt, "ascii-over")
	wantOver := "- ascii-over: " + strings.Repeat("b", issue1155MaxDescChars-3) + "..."
	if overLine != wantOver {
		t.Errorf("over-cap line:\n got %q\nwant %q", overLine, wantOver)
	}
	if !utf8.ValidString(overLine) {
		t.Error("over-cap line is not valid UTF-8")
	}
}

// TestIssue1154SectionCollectorStopIdempotentConcurrent proves Stop can be
// called repeatedly and from many goroutines at once without double-close
// panics or hangs (#1154 desktop multi-ChatBridge scenario).
func TestIssue1154SectionCollectorStopIdempotentConcurrent(t *testing.T) {
	sc := newSectionCollector(t.TempDir())
	sc.Start()

	const callers = 8
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			sc.Stop()
		}()
	}
	stopped := make(chan struct{})
	go func() {
		wg.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(15 * time.Second):
		t.Fatal("concurrent Stop calls did not finish (possible deadlock)")
	}

	select {
	case <-sc.done:
	default:
		t.Error("background loop did not exit after Stop")
	}

	// Extra sequential Stops must stay silent no-ops.
	sc.Stop()
	sc.Stop()
}

// TestIssue1154SectionCollectorLifecycleEdges guards Start/Stop ordering:
// repeated Start must not spawn duplicate loops (which would race to close
// done), and Stop-before-Start must neither hang nor leak a loop (#1154).
func TestIssue1154SectionCollectorLifecycleEdges(t *testing.T) {
	sc := newSectionCollector(t.TempDir())
	sc.Start()
	sc.Start() // duplicate Start: must be a no-op
	doneSignal := make(chan struct{})
	go func() {
		sc.Stop()
		close(doneSignal)
	}()
	select {
	case <-doneSignal:
	case <-time.After(15 * time.Second):
		t.Fatal("Stop after duplicate Start hung")
	}

	sc2 := newSectionCollector(t.TempDir())
	finished := make(chan struct{})
	go func() {
		sc2.Stop() // never started: must return promptly
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(15 * time.Second):
		t.Fatal("Stop without Start hung waiting for done")
	}
	// Starting afterwards still works and exits cleanly via the pre-closed stop.
	sc2.Start()
	select {
	case <-sc2.done:
	case <-time.After(15 * time.Second):
		t.Fatal("loop started after Stop did not observe closed stop channel")
	}
}

// TestIssue1154GlobalCollectorInitStopSnapshotRaceStress hammers the shared
// global collector: concurrent Init (two different dirs), Snapshot, and Stop.
// Run under -race this fails if any access to globalSectionCollector is
// unsynchronized or if Stop double-closes (#1154).
func TestIssue1154GlobalCollectorInitStopSnapshotRaceStress(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	defer StopGlobalSectionCollector()

	dirs := []string{dirA, dirB}
	const workers = 6
	const iterations = 12
	var wg sync.WaitGroup
	stopCh := make(chan struct{})
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				select {
				case <-stopCh:
					return
				default:
				}
				switch w % 3 {
				case 0:
					InitGlobalSectionCollector(dirs[w%len(dirs)])
				case 1:
					GlobalSectionSnapshot()
				default:
					StopGlobalSectionCollector()
				}
				time.Sleep(time.Millisecond)
			}
		}(w)
	}
	allDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(allDone)
	}()
	select {
	case <-allDone:
	case <-time.After(60 * time.Second):
		close(stopCh)
		t.Fatal("global collector stress test exceeded deadline")
	}

	// Converge to a defined live state and verify snapshot availability, then
	// let the deferred Stop clean up for other tests in the package.
	InitGlobalSectionCollector(dirA)
	if _, ok := GlobalSectionSnapshot(); !ok {
		t.Fatal("expected available snapshot after final Init")
	}
}
