package handoff

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func userMsg(text string) provider.Message {
	return provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: text}},
	}
}

func TestRenderFull(t *testing.T) {
	s := Snapshot{
		OldSessionID: "01JABCDEFGH",
		Model:        "zai/glm-4.7",
		GeneratedAt:  time.Unix(1700000000, 0).UTC(),
		Goals:        []string{"fix the login bug", "add retry logic"},
		TasksStats:   "3 completed · 1 in progress · 5 pending",
		TasksDigest:  "#1 fix login (in_progress)",
		Git: GitInfo{
			Available: true,
			Branch:    "r90-frontier",
			Status:    "M internal/foo.go",
			Log:       "29fad0a merge pr",
		},
	}
	out := Render(s)
	if !strings.HasPrefix(out, Marker+"\n") {
		t.Fatalf("artifact must start with marker, got prefix %q", out[:min(30, len(out))])
	}
	for _, want := range []string{
		"01JABCDE",             // short session id
		"zai/glm-4.7",          // model
		"fix the login bug",    // goal 1
		"add retry logic",      // goal 2
		"3 completed",          // board stats
		"#1 fix login",         // digest (indented)
		"branch: r90-frontier", // git branch
		"M internal/foo.go",    // dirty file (indented)
		"29fad0a merge pr",     // recent commit
	} {
		if !strings.Contains(out, want) {
			t.Errorf("artifact missing %q", want)
		}
	}
	// Goal numbering must be sequential from 1.
	if !strings.Contains(out, "1. fix the login bug\n2. add retry logic\n") {
		t.Errorf("goal numbering wrong:\n%s", out)
	}
	// Digest and status lines must be indented.
	if !strings.Contains(out, "\n  #1 fix login") || !strings.Contains(out, "\n  M internal/foo.go") {
		t.Errorf("board digest / git status lines must be indented")
	}
}

func TestRenderEmpty(t *testing.T) {
	out := Render(Snapshot{})
	for _, want := range []string{
		Marker,
		"(none recorded)",
		"(no tasks on the board)",
		"(git unavailable",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("empty artifact missing %q", want)
		}
	}
}

func TestExtractGoalsFiltersAndCaps(t *testing.T) {
	msgs := []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "sys"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}},
		userMsg("goal 1"),
		userMsg("   "), // blank -> skipped
		userMsg("goal 2"),
		userMsg("goal 2"), // consecutive duplicate -> collapsed
		{Role: "user", Content: []provider.ContentBlock{{Type: "tool_result", Text: "tr"}}}, // non-text block skipped
	}
	for i := 0; i < 12; i++ {
		msgs = append(msgs, userMsg(strings.Repeat("g", 10)+string(rune('a'+i))))
	}
	goals := ExtractGoals(msgs)
	if len(goals) != maxGoals {
		t.Fatalf("want %d goals, got %d", maxGoals, len(goals))
	}
	// Keeps the MOST RECENT goals: the last message is the final filler.
	if !strings.HasSuffix(goals[len(goals)-1], "l") {
		t.Errorf("most recent goal not kept last: %q", goals[len(goals)-1])
	}
	// Early goals (goal 1/2, and the first fillers) are dropped.
	for _, g := range goals {
		if g == "goal 1" || g == "goal 2" {
			t.Errorf("stale goal should have been dropped: %q", g)
		}
	}
	// 14 kept goals total (goal 1, goal 2, 12 fillers) -> last 10 kept:
	// fillers c..l, so the first kept goal is filler 'c'.
	if goals[0] != "ggggggggggc" {
		t.Errorf("first kept goal should be filler c, got %q", goals[0])
	}
}

func TestExtractGoalsTruncatesLongText(t *testing.T) {
	long := strings.Repeat("汉", maxGoalRunes+50)
	goals := ExtractGoals([]provider.Message{userMsg(long)})
	if len(goals) != 1 {
		t.Fatalf("want 1 goal, got %d", len(goals))
	}
	runes := len([]rune(goals[0]))
	if runes != maxGoalRunes+1 { // truncated text + ellipsis rune
		t.Errorf("goal not truncated: %d runes", runes)
	}
	if !strings.HasSuffix(goals[0], "…") {
		t.Errorf("truncated goal must end with ellipsis")
	}
}

func TestCollectGitNonGitDir(t *testing.T) {
	info := CollectGit(t.TempDir())
	if info.Available {
		t.Fatalf("non-git dir must yield Available=false: %+v", info)
	}
	if info.Branch != "" || info.Status != "" || info.Log != "" {
		t.Errorf("non-git dir must yield empty fields: %+v", info)
	}
	// Empty dir must be a no-op, not a panic.
	if info := CollectGit(""); info.Available {
		t.Errorf("empty dir must yield Available=false")
	}
}

func TestCollectGitRealRepo(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable or failed (%v): %s", err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	if err := exec.Command("git", "-C", dir, "commit", "--allow-empty", "-q", "-m", "initial").Run(); err != nil {
		t.Skipf("git commit failed: %v", err)
	}
	info := CollectGit(dir)
	if !info.Available {
		t.Fatalf("git repo must yield Available=true")
	}
	if info.Branch == "" {
		t.Errorf("branch must be non-empty")
	}
	if !strings.Contains(info.Log, "initial") {
		t.Errorf("log must contain the initial commit, got %q", info.Log)
	}
}

func TestSystemMessage(t *testing.T) {
	art := Render(Snapshot{Goals: []string{"x"}})
	msg := SystemMessage(art)
	if msg.Role != "system" {
		t.Errorf("role = %q, want system", msg.Role)
	}
	if len(msg.Content) != 1 || msg.Content[0].Type != "text" {
		t.Fatalf("want a single text content block")
	}
	if !strings.HasPrefix(msg.Content[0].Text, Marker) {
		t.Errorf("system message text must start with marker")
	}
	if !strings.Contains(msg.Content[0].Text, "x") {
		t.Errorf("artifact content lost")
	}
}

func TestSanitizeToken(t *testing.T) {
	cases := map[string]string{
		"01JABCDEFGH-XYZ": "01JABCDEFGH-XYZ",
		"a/b\\c:d*e?f":    "abcdef",
		"":                "session",
		"///":             "session",
	}
	for in, want := range cases {
		if got := SanitizeToken(in); got != want {
			t.Errorf("SanitizeToken(%q) = %q, want %q", in, got, want)
		}
	}
	// Long input is capped.
	if got := SanitizeToken(strings.Repeat("x", 100)); len(got) != 24 {
		t.Errorf("long token not capped: %d", len(got))
	}
}

func TestCapLinesAndTruncate(t *testing.T) {
	if got := capLines("a\nb\nc", 2); got != "a\nb\n(… 1 more)" {
		t.Errorf("capLines = %q", got)
	}
	if got := capLines("a", 5); got != "a" {
		t.Errorf("capLines under cap = %q", got)
	}
	if got := capLines("", 5); got != "" {
		t.Errorf("capLines empty = %q", got)
	}
	if got := truncateRunes("hello", 10); got != "hello" {
		t.Errorf("truncateRunes under cap = %q", got)
	}
}
