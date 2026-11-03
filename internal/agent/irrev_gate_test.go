package agent

import (
	"strconv"
	"testing"
)

func TestIrrevClassifyTool(t *testing.T) {
	tests := []struct {
		tool string
		args string
		want int
	}{

		{"read_file", `{"path":"/tmp/f.go"}`, irrevTierNone},
		{"grep", `{"pattern":"foo"}`, irrevTierNone},
		{"git_status", `{}`, irrevTierNone},
		{"git_diff", `{}`, irrevTierNone},

		{"edit_file", `{"file_path":"/tmp/f.go"}`, irrevTierLow},
		{"write_file", `{"path":"/tmp/f.go"}`, irrevTierLow},
		{"git_add", `{"files":["a.go"]}`, irrevTierLow},
		{"undo_edit", `{}`, irrevTierLow},

		{"git_commit", `{"message":"x"}`, irrevTierMedium},
		{"file_ops", `{"ops":[]}`, irrevTierMedium},
		{"batch_replace", `{"pattern":"x","replacement":"y","files":[]}`, irrevTierMedium},

		{"git_reset", `{"mode":"soft"}`, irrevTierLow},
		{"git_reset", `{"args":"git reset --hard HEAD~3"}`, irrevTierHigh},

		{"git_reset", `{"mode":"hard","target":"HEAD~3"}`, irrevTierHigh},
		{"git_reset", `{"mode":"soft","target":"HEAD"}`, irrevTierLow},
		{"git_reset", `{"mode":"mixed"}`, irrevTierLow},
		{"git_push", `{"force":true}`, irrevTierHigh},

		{"desktop_control", `{"action":"quit_app","text":"Xcode"}`, irrevTierHigh},
		{"desktop_control", `{"action":"snapshot_ui","max_depth":8}`, irrevTierNone},
		{"desktop_control", `{"action":"find_element","text":"Save"}`, irrevTierNone},
		{"desktop_control", `{"action":"click","x":100,"y":200}`, irrevTierLow},
		{"desktop_control", `{"action":"type","text":"hello"}`, irrevTierLow},
		{"mobile_device", `{"action":"uninstall","bundle_id":"com.example.app"}`, irrevTierHigh},
		{"mobile_device", `{"action":"install","app":"/tmp/app.apk"}`, irrevTierMedium},
		{"mobile_device", `{"action":"devices"}`, irrevTierNone},
		{"mobile_device", `{"action":"tap","x":50,"y":80}`, irrevTierLow},
		{"browser", `{"action":"evaluate","expression":"document.title"}`, irrevTierMedium},
		{"browser", `{"action":"upload","path":"/tmp/f.png"}`, irrevTierMedium},
		{"browser", `{"action":"navigate","url":"https://example.com"}`, irrevTierLow},
		{"browser", `{"action":"links"}`, irrevTierNone},
		{"browser", `{"action":"click","selector":"button.submit"}`, irrevTierLow},

		{"desktop_control", `not-json`, irrevTierLow},

		{"run_command", `{"command":"git fetch origin main 2>&1 | tail -2; git log --oneline -25 origin/main"}`, irrevTierNone},
		{"run_command", `{"command":"git status"}`, irrevTierNone},
		{"run_command", `{"command":"go build -o /tmp/ggcode ./cmd/ggcode"}`, irrevTierNone},
		{"run_command", `{"command":"GOMEMLIMIT=2GiB GOGC=50 go test -tags goolm -p 1 ./internal/agent/"}`, irrevTierNone},
		{"run_command", `{"command":"cd /tmp && go vet ./..."}`, irrevTierNone},
		{"run_command", `{"command":"cat a.go | grep foo | wc -l"}`, irrevTierNone},
		{"run_command", "{\"command\":\"# fetch latest\\n cd /tmp && git status\"}", irrevTierNone},

		{"run_command", `{"command":"git push --force origin main"}`, irrevTierHigh},
		{"run_command", `{"command":"git reset --hard HEAD~3"}`, irrevTierHigh},
		{"run_command", `{"command":"rm -rf /tmp/foo"}`, irrevTierHigh},
		{"run_command", `{"command":"echo hello"}`, irrevTierMedium},

		{"unknown_tool", `{}`, irrevTierLow},
	}

	for _, tt := range tests {
		got := irrevClassifyTool(tt.tool, tt.args)
		if got != tt.want {
			t.Errorf("irrevClassifyTool(%q, ...) = %d, want %d", tt.tool, got, tt.want)
		}
	}
}

func TestIrrevIsDestructiveCommand(t *testing.T) {
	cases := []string{
		`{"command":"git push --force"}`,
		`{"command":"git reset --hard"}`,
		`{"command":"rm -rf /"}`,
		`{"command":"DROP TABLE users"}`,
		`{"command":"git push -f"}`,
	}
	for _, c := range cases {
		if !irrevIsDestructiveCommand(c) {
			t.Errorf("irrevIsDestructiveCommand(%q) = false, want true", c)
		}
	}

	if irrevIsDestructiveCommand(`{"command":"echo hello"}`) {
		t.Error("echo should not be destructive")
	}
}

func TestIrrevGate_FirstReadOnlyCommandNoAdvisory(t *testing.T) {
	s := newIrrevGateState()
	warn := s.recordAction("run_command",
		`{"command":"git fetch origin main && git log --oneline -5","description":"fetch"}`)
	if warn != "" {
		t.Errorf("first read-only command must not trigger advisory, got %q", warn)
	}
	if s.warnings != 0 {
		t.Errorf("warning count must stay 0, got %d", s.warnings)
	}
}

func TestIrrevReadOnlyCommandConservative(t *testing.T) {
	stayNonReadOnly := []string{
		"echo hello",
		"go test ./... > out.txt",
		"git status 2>err.txt",
		"cat $(pwd)/x",
		"make build",
		"curl -s https://example.com",
		"go run ./cmd/x",
		"go fmt ./...",
		"npm install",
		"sed -i '' s/a/b/ f.txt",
		"git config user.name val",
	}
	for _, c := range stayNonReadOnly {
		if irrevIsReadOnlyCommand(`{"command":` + strconv.Quote(c) + `}`) {
			t.Errorf("irrevIsReadOnlyCommand(%q) = true, want false", c)
		}
	}
}

func TestIrrevReadOnlyDestructivePrecedence(t *testing.T) {
	if got := irrevClassifyTool("run_command", `{"command":"git fetch origin main && rm -rf /tmp/x"}`); got != irrevTierHigh {
		t.Errorf("destructive pattern must win over read-only, got %d", got)
	}
	if got := irrevClassifyTool("run_command", `{"command":"git branch -d tmp"}`); got != irrevTierHigh {
		t.Errorf("git branch -d must stay High, got %d", got)
	}
}

func TestIrrevIsGroundingAction(t *testing.T) {
	grounding := []string{"read_file", "grep", "search_files", "git_diff", "code_health", "git_status"}
	for _, tool := range grounding {
		if !irrevIsGroundingAction(tool) {
			t.Errorf("irrevIsGroundingAction(%q) = false, want true", tool)
		}
	}
	notGrounding := []string{"edit_file", "write_file", "git_commit"}

	if !irrevIsGroundingAction("run_command") {
		t.Error("run_command should count as grounding (#1468-A)")
	}
	for _, tool := range notGrounding {
		if irrevIsGroundingAction(tool) {
			t.Errorf("irrevIsGroundingAction(%q) = true, want false", tool)
		}
	}
}

func TestIrrevGate_HighImpactNoGrounding(t *testing.T) {
	s := newIrrevGateState()

	warn := s.recordAction("git_reset", `{"args":"git reset --hard HEAD~3"}`)
	if warn == "" {
		t.Error("expected warning for high-impact action with no grounding")
	}
}

func TestIrrevGate_HighImpactWithGrounding(t *testing.T) {
	s := newIrrevGateState()

	s.recordAction("git_status", `{}`)
	s.recordAction("git_diff", `{}`)

	warn := s.recordAction("git_reset", `{"args":"git reset --hard HEAD~3"}`)
	if warn != "" {
		t.Error("expected no warning for high-impact action after 2 grounding actions")
	}
}

func TestIrrevGate_MediumImpactInsufficientGrounding(t *testing.T) {
	s := newIrrevGateState()

	warn := s.recordAction("git_commit", `{"message":"x"}`)
	if warn == "" {
		t.Error("expected warning for medium-impact action with no grounding")
	}
}

func TestIrrevGate_MediumImpactWithGrounding(t *testing.T) {
	s := newIrrevGateState()

	s.recordAction("read_file", `{"path":"a.go"}`)

	warn := s.recordAction("git_commit", `{"message":"x"}`)
	if warn != "" {
		t.Error("expected no warning for medium-impact after 1 grounding")
	}
}

func TestIrrevGate_ReadOnlyNoWarning(t *testing.T) {
	s := newIrrevGateState()
	warn := s.recordAction("read_file", `{"path":"a.go"}`)
	if warn != "" {
		t.Error("read-only tools should never trigger warning")
	}
}

func TestIrrevGate_LowImpactNoWarning(t *testing.T) {
	s := newIrrevGateState()
	warn := s.recordAction("edit_file", `{"file_path":"a.go"}`)
	if warn != "" {
		t.Error("low-impact tools should never trigger warning")
	}
}

func TestIrrevGate_MaxWarnings(t *testing.T) {
	s := newIrrevGateState()

	for i := 0; i < irrevMaxWarnings; i++ {
		s.recordAction("git_commit", `{"message":"x"}`)
		s.reset()
	}

	s.reset()
	warn := s.recordAction("git_commit", `{"message":"x"}`)

	_ = warn
}

func TestIrrevGate_MaxWarningsNoReset(t *testing.T) {
	s := newIrrevGateState()
	count := 0
	for i := 0; i < irrevMaxWarnings+2; i++ {
		warn := s.recordAction("git_commit", `{"message":"x"}`)
		if warn != "" {
			count++
		}
	}
	if count != irrevMaxWarnings {
		t.Errorf("expected %d warnings, got %d", irrevMaxWarnings, count)
	}
}

func TestIrrevGate_Reset(t *testing.T) {
	s := newIrrevGateState()
	s.recordAction("git_commit", `{"message":"x"}`)
	if s.warnings != 1 {
		t.Errorf("expected 1 warning, got %d", s.warnings)
	}
	s.reset()
	if s.warnings != 0 {
		t.Errorf("expected 0 warnings after reset, got %d", s.warnings)
	}
	if s.totalGrounded != 0 {
		t.Errorf("expected 0 grounded after reset, got %d", s.totalGrounded)
	}
	if len(s.grounding) != 0 {
		t.Errorf("expected empty grounding after reset, got %d", len(s.grounding))
	}
}

func TestIrrevGate_GroundingWindowDecay(t *testing.T) {
	s := newIrrevGateState()

	s.recordAction("git_diff", `{}`)
	s.recordAction("git_status", `{}`)

	for i := 0; i < irrevGroundingWindow+2; i++ {
		s.recordAction("edit_file", `{"file_path":"a.go"}`)
	}

	warn := s.recordAction("git_reset", `{"args":"git reset --hard HEAD~3"}`)
	if warn == "" {
		t.Error("expected warning after grounding decayed out of window")
	}
}

func TestStashDropAndTagDeleteTierUp(t *testing.T) {
	if got := irrevClassifyTool("git_stash", `{"action":"drop","index":3}`); got != irrevTierHigh {
		t.Fatalf("stash drop must be High, got %v", got)
	}
	if got := irrevClassifyTool("git_stash", `{"action":"list"}`); got != irrevTierLow {
		t.Fatalf("stash list must stay Low, got %v", got)
	}
	if got := irrevClassifyTool("git_tag", `{"action":"delete","name":"v1.0"}`); got != irrevTierMedium {
		t.Fatalf("tag delete must be Medium, got %v", got)
	}
	if got := irrevClassifyTool("git_tag", `{"action":"list"}`); got != irrevTierNone {
		t.Fatalf("tag list must stay None, got %v", got)
	}
}

func TestIrrevGateRecordOutcomeRevokesFailedGrounding(t *testing.T) {
	s := newIrrevGateState()

	s.recordAction("run_command", "go test ./...")
	s.recordOutcome("run_command", false)
	s.recordAction("run_command", "go build ./...")
	s.recordOutcome("run_command", false)
	if s.totalGrounded != 2 {
		t.Fatalf("successful verifications must ground, got %d", s.totalGrounded)
	}

	s.recordAction("run_command", "go test ./pkg/...")
	s.recordOutcome("run_command", true)
	if s.totalGrounded != 2 {
		t.Fatalf("failed verification must be revoked, got %d", s.totalGrounded)
	}
	if s.grounding[len(s.grounding)-1] {
		t.Fatal("grounding ledger tail must be false after revocation")
	}

	s.recordAction("run_command", "go vet ./...")
	before := s.totalGrounded
	s.recordOutcome("grep", true)
	if s.totalGrounded != before {
		t.Fatal("outcome for a different tool must not revoke")
	}
}

func TestIrrevClassifyTool1621ModeIgnoredWithFiles(t *testing.T) {
	if got := irrevClassifyTool("git_reset", `{"mode":"hard","files":["a.go"]}`); got != irrevTierLow {
		t.Fatalf("mode+files must tier Low (mixed unstage), got %d", got)
	}
	if got := irrevClassifyTool("git_reset", `{"mode":"hard"}`); got != irrevTierHigh {
		t.Fatalf("mode hard without files must stay High, got %d", got)
	}
}
