package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/restart"
)

// zz_issue5753_test.go pins the sharpest incident shape: the restart arm
// reported OK, the armRestartMsg was silently dropped (no live TUI program),
// and the process kept serving the old image with ZERO footprint while the
// resume nudge still fired. The drop must now annotate the durable arm
// record so the nudge-time handshake alarms ARMED-BUT-NOT-EXECUTED.

func TestIssue5753ArmDeliveryFailureIsRecorded(t *testing.T) {
	t.Setenv("GGCODE_RESTART_ARM_DIR", t.TempDir())
	target := filepath.Join(t.TempDir(), "ggcode")
	if err := os.WriteFile(target, []byte("staged binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := restart.WriteArmRecord(target, "delivery test"); err != nil {
		t.Fatal(err)
	}

	// Zero-value REPL: program and programSend are both nil — exactly the
	// silent-drop conditions from the incident.
	rr := &replRestartRequester{repl: &REPL{}}
	rr.RequestRestart("binary updated", false)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rec, ok := restart.LoadArmRecord()
		if ok && rec.ExecStage == "arm-delivery" {
			if rec.ExecError == "" || !strings.Contains(rec.ExecError, "no live TUI program") {
				t.Errorf("delivery failure error shape: %+v", rec)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("silent arm drop left no durable trace — the #5753 defect is still present")
}
