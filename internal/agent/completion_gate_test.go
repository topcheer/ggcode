package agent

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
)

func newTestMergeGate(ttl time.Duration) *mergeGateState {
	s := newMergeGateState()
	s.SetMergeGateConfigProvider(func() config.MergeGateConfig {
		return config.MergeGateConfig{PreflightTTL: ttl}
	})
	return s
}

func TestMergeGatePreflightIsReadonlyGrounding(t *testing.T) {
	// merge_preflight is a pure read-only gh query: it must never itself
	// gate (tier None) and it counts as grounding so the preflight the
	// completion gate demands also satisfies the irrev gate's evidence
	// requirement for the subsequent merge command.
	if got := irrevClassifyTool("merge_preflight", `{"pr_number":5}`); got != irrevTierNone {
		t.Errorf("merge_preflight tier = %d, want None", got)
	}
	if !irrevIsGroundingAction("merge_preflight") {
		t.Error("merge_preflight must count as a grounding action")
	}
}

func TestMergeGateProtectedCommandRequiresPreflight(t *testing.T) {
	s := newTestMergeGate(time.Minute)
	got := s.checkCommand(permission.SupervisedMode, "run_command", `{"command":"gh pr merge 5356"}`)
	if got == "" {
		t.Fatal("protected command without a snapshot must produce a REQUIREMENT prompt")
	}
	for _, want := range []string{
		"[completion-gate]",
		"merge_preflight",
		`{"pr_number":5356}`,
		"--match-head-commit",
		"does not block",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("requirement prompt missing %q:\n%s", want, got)
		}
	}
}

// #5401: bare merge (no PR number) IS gated - requirement demands an explicit PR
func TestMergeGateBareMergeRequiresExplicitPR(t *testing.T) {
	s := newTestMergeGate(time.Minute)
	got := s.checkCommand(permission.SupervisedMode, "run_command", `{"command":"gh pr merge"}`)
	if got == "" {
		t.Fatal("bare 'gh pr merge' must trigger the merge gate (issue #5401)")
	}
	if !strings.Contains(got, "no PR number can be resolved") || !strings.Contains(got, "explicit PR number") {
		t.Fatalf("pr=0 requirement must demand an explicit PR restatement, got %q", got)
	}
}

func TestMergeGateNonProtectedCommandsPass(t *testing.T) {
	s := newTestMergeGate(time.Minute)
	cases := []struct{ tool, args string }{
		{"run_command", `{"command":"git push origin main"}`},
		{"run_command", `{"command":"gh pr view 5356"}`},
		{"run_command", `{"command":"gh pr merged 5356"}`},
		{"run_command", `{"command":"go build ./..."}`},
		{"edit_file", `{"file_path":"/tmp/x.go"}`},
		{"read_file", `{"path":"/tmp/x.go"}`},
	}
	for _, tc := range cases {
		if got := s.checkCommand(permission.SupervisedMode, tc.tool, tc.args); got != "" {
			t.Errorf("checkCommand(%s, %s) = %q, want empty", tc.tool, tc.args, got)
		}
	}
}

func TestMergeGateDisabledPasses(t *testing.T) {
	s := newMergeGateState()
	f := false
	s.SetMergeGateConfigProvider(func() config.MergeGateConfig {
		return config.MergeGateConfig{Enabled: &f}
	})
	if got := s.checkCommand(permission.SupervisedMode, "run_command", `{"command":"gh pr merge 5356"}`); got != "" {
		t.Errorf("disabled gate must not prompt, got %q", got)
	}
}

func TestMergeGateCustomCommands(t *testing.T) {
	s := newMergeGateState()
	s.SetMergeGateConfigProvider(func() config.MergeGateConfig {
		return config.MergeGateConfig{Commands: []string{"glab mr merge"}}
	})
	if got := s.checkCommand(permission.SupervisedMode, "run_command", `{"command":"gh pr merge 5356"}`); got != "" {
		t.Errorf("default command must not match a custom-only pattern set, got %q", got)
	}
	if got := s.checkCommand(permission.SupervisedMode, "run_command", `{"command":"glab mr merge 12"}`); got == "" {
		t.Error("custom pattern must be protected")
	}
}

func TestMergeGateFreshSnapshotPasses(t *testing.T) {
	s := newTestMergeGate(time.Minute)
	if got := s.checkCommand(permission.SupervisedMode, "run_command", `{"command":"gh pr merge 5356"}`); got == "" {
		t.Fatal("sanity: must prompt before a snapshot exists")
	}
	s.RecordPreflightResult("merge_preflight",
		`{"pr":5356,"head_sha":"eecdb0447000000000000000000000000000000","verdict":"PASS"}`+"\n\nPREFLIGHT: PASS\n")
	if got := s.checkCommand(permission.SupervisedMode, "run_command", `{"command":"gh pr merge 5356"}`); got != "" {
		t.Errorf("fresh PASS snapshot must let the command through, got %q", got)
	}
	// Different PR is not covered by the snapshot.
	if got := s.checkCommand(permission.SupervisedMode, "run_command", `{"command":"gh pr merge 7"}`); got == "" {
		t.Error("snapshot for PR 5356 must not cover PR 7")
	}
	// HOLD verdicts never record a snapshot.
	s.RecordPreflightResult("merge_preflight",
		`{"pr":42,"head_sha":"abc","verdict":"HOLD"}`+"\n\nPREFLIGHT: HOLD(checks_green)\n")
	if got := s.checkCommand(permission.SupervisedMode, "run_command", `{"command":"gh pr merge 42"}`); got == "" {
		t.Error("HOLD verdict must not satisfy the gate")
	}
	// Other tools' results are ignored.
	s.RecordPreflightResult("ci_status", `{"pr":5356,"verdict":"PASS"}`)
}

func TestMergeGateTTLExpiry(t *testing.T) {
	s := newTestMergeGate(50 * time.Millisecond)
	base := time.Now()
	now := base
	s.now = func() time.Time { return now }

	s.RecordPreflightResult("merge_preflight", `{"pr":9,"head_sha":"aaa","verdict":"PASS"}`)
	if got := s.checkCommand(permission.SupervisedMode, "run_command", `{"command":"gh pr merge 9"}`); got != "" {
		t.Fatal("fresh snapshot must pass")
	}
	now = base.Add(49 * time.Millisecond)
	if got := s.checkCommand(permission.SupervisedMode, "run_command", `{"command":"gh pr merge 9"}`); got != "" {
		t.Fatal("snapshot at 49ms of a 50ms TTL must still pass")
	}
	now = base.Add(51 * time.Millisecond)
	if got := s.checkCommand(permission.SupervisedMode, "run_command", `{"command":"gh pr merge 9"}`); got == "" {
		t.Fatal("expired snapshot must re-require preflight")
	}
}

func TestMergeGateBypassModeDoesNotPrompt(t *testing.T) {
	s := newTestMergeGate(time.Minute)
	for _, mode := range []permission.PermissionMode{permission.BypassMode, permission.AutopilotMode} {
		if got := s.checkCommand(mode, "run_command", `{"command":"gh pr merge 5356"}`); got != "" {
			t.Errorf("cruise mode %v must pass through without prompting, got %q", mode, got)
		}
	}
}

func TestMergeGateLeadingCommentCommand(t *testing.T) {
	s := newTestMergeGate(time.Minute)
	args, _ := json.Marshal(map[string]string{
		"command": "# merge after checks\ngh pr merge 5356",
	})
	if got := s.checkCommand(permission.SupervisedMode, "run_command", string(args)); got == "" {
		t.Error("command behind a leading shell comment must still be protected")
	}
}

// TestMergeGateTTLRaceConcurrent exercises the snapshot store under
// concurrent record/read. Run with -race and -count=20 (see Makefile /
// docs): the mutex-guarded map must never race or leak a stale entry
// across PRs.
func TestMergeGateTTLRaceConcurrent(t *testing.T) {
	s := newTestMergeGate(200 * time.Millisecond)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				pr := 100 + (i % 5)
				s.RecordPreflightResult("merge_preflight", `{"pr":`+strconv.Itoa(pr)+`,"head_sha":"x","verdict":"PASS"}`)
				s.preflightFresh(pr, time.Second)
				s.preflightFresh(pr, time.Nanosecond) // force expiry reads
				s.checkCommand(permission.SupervisedMode, "run_command", `{"command":"gh pr merge `+strconv.Itoa(pr)+`"}`)
			}
		}(g)
	}
	wg.Wait()
	// Distinct-PR isolation survived the storm: PR 5356 has no snapshot.
	if fresh, _ := s.preflightFresh(5356, time.Hour); fresh {
		t.Error("no snapshot was recorded for PR 5356; isolation broken")
	}
}
