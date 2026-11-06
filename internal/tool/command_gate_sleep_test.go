package tool

import (
	"strings"
	"testing"
)

// Inline-sleep abuse guard: "sleep 300 && ./script" inside run_command is
// the same abuse the sleep tool's 3-minute hard cap exists for. Sum of
// literal sleeps >180s blocks with an actionable alternative; >60s warns;
// short polling sleeps and quoted/variable sleeps stay untouched.

func TestInlineSleepGate_BlocksLongWaits(t *testing.T) {
	g := NewCommandGate()
	for name, cmd := range map[string]string{
		"plain":       "sleep 300 && ./build.sh",
		"suffix min":  "sleep 4m && deploy.sh",
		"suffix hour": "sleep 1h; run.sh",
		"sum":         "sleep 100 && sleep 100",
		"powershell":  "Start-Sleep -Seconds 240",
	} {
		res := g.Check(cmd)
		if !res.IsBlocked() {
			t.Errorf("%s: expected Block, got %v (%s)", name, res.Behavior, res.Reason)
		}
		if !strings.Contains(res.Reason, "start_command") {
			t.Errorf("%s: block reason must point at start_command alternative, got: %s", name, res.Reason)
		}
	}
}

func TestInlineSleepGate_WarnsMediumWaits(t *testing.T) {
	g := NewCommandGate()
	for name, cmd := range map[string]string{
		"90s":    "sleep 90 && echo ok",
		"2m":     "sleep 2m && retry.sh",
		"summed": "sleep 40; fetch; sleep 40",
	} {
		res := g.Check(cmd)
		if res.IsBlocked() || res.NeedsConfirmation() {
			t.Errorf("%s: expected Allow, got %v (%s)", name, res.Behavior, res.Reason)
		}
		if !hasWarningContaining(res.Warnings, "Inline sleep") {
			t.Errorf("%s: expected inline-sleep warning, got %v", name, res.Warnings)
		}
	}
}

func TestInlineSleepGate_LeavesShortAndOpaqueSleepsAlone(t *testing.T) {
	g := NewCommandGate()
	for name, cmd := range map[string]string{
		"polling loop":    "until curl -sf localhost:8080/health; do sleep 5; done",
		"short single":    "sleep 30 && tail log",
		"variable":        "sleep $WAIT && go test ./...",
		"quoted":          `echo "sleep 999" && true`,
		"unrelated word":  "mysleep 400",
		"substitution":    "sleep $(compute) || true",
		"ps milliseconds": "Start-Sleep -Milliseconds 500; echo hi",
	} {
		res := g.Check(cmd)
		if res.IsBlocked() {
			t.Errorf("%s: must not block, got Block (%s)", name, res.Reason)
		}
		if hasWarningContaining(res.Warnings, "Inline sleep") {
			t.Errorf("%s: must not warn for a legal short sleep, got %v", name, res.Warnings)
		}
	}
}

func TestInlineSleepSeconds_Units(t *testing.T) {
	cases := map[string]float64{
		"sleep 5":              5,
		"sleep 2m":             120,
		"sleep 1.5h":           5400,
		"sleep 30 && sleep 30": 60,
		"a; sleep 3m":          180,
	}
	for cmd, want := range cases {
		if got := inlineSleepSeconds(cmd); got != want {
			t.Errorf("inlineSleepSeconds(%q)=%v, want %v", cmd, got, want)
		}
	}
}

func hasWarningContaining(ws []string, sub string) bool {
	for _, w := range ws {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}
