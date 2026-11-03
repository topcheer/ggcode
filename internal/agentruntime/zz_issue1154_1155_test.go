package agentruntime

import (
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/commands"
)

const issue1155MaxDescChars = 180

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

func TestIssue1155SkillDescriptionTruncationRuneSafe(t *testing.T) {
	longCJK := strings.Repeat("汉", 250)
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

	sc.Stop()
	sc.Stop()
}

func TestIssue1154SectionCollectorLifecycleEdges(t *testing.T) {
	sc := newSectionCollector(t.TempDir())
	sc.Start()
	sc.Start()
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
		sc2.Stop()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(15 * time.Second):
		t.Fatal("Stop without Start hung waiting for done")
	}

	sc2.Start()
	select {
	case <-sc2.done:
	case <-time.After(15 * time.Second):
		t.Fatal("loop started after Stop did not observe closed stop channel")
	}
}

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

	InitGlobalSectionCollector(dirA)
	if _, ok := GlobalSectionSnapshot(); !ok {
		t.Fatal("expected available snapshot after final Init")
	}
}
