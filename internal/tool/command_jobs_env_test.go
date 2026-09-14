package tool

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/util"
)

// TestCommandJobStartStripsSecretEnv verifies #2350: Manager.Start builds
// its own exec.Cmd, and a nil cmd.Env inherits the FULL parent environment -
// including config-managed secrets. `start_command printenv` must not be
// able to dump them into job output. The run_command main path strips
// (normalizedCommandEnv); Manager.Start must apply the same.
func TestCommandJobStartStripsSecretEnv(t *testing.T) {
	const secretName = "GGCODE_PROBE_SECRET_2350"
	t.Setenv(secretName, "leak-me-if-you-can")
	t.Setenv("GGCODE_PROBE_PLAIN_2350", "must-pass-through")
	util.RegisterSecretEnv(secretName)

	m := NewCommandJobManager("")

	runJob := func(command string) string {
		t.Helper()
		snap, err := m.Start(context.Background(), command, false, 30*time.Second)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		out, err := m.Wait(ctx, snap.ID, 15*time.Second, 0, 0)
		if err != nil {
			t.Fatalf("Wait %s: %v", snap.ID, err)
		}
		return strings.Join(out.Lines, "\n")
	}

	// Positive control: a non-secret variable survives the job env.
	if lines := runJob(`echo "[$GGCODE_PROBE_PLAIN_2350]"`); !strings.Contains(lines, "[must-pass-through]") {
		t.Fatalf("plain var missing from job env (stripping too aggressive?):\n%s", lines)
	}

	// The probe: the registered secret must NOT reach the child process.
	if lines := runJob(`echo "[$GGCODE_PROBE_SECRET_2350]"`); strings.Contains(lines, "leak-me-if-you-can") {
		t.Fatalf("secret leaked into child env via Manager.Start:\n%s", lines)
	}

	// Terminal normalization rides along (spot-check TERM).
	if lines := runJob(`echo "TERM=$TERM"`); !strings.Contains(lines, "TERM=dumb") {
		t.Fatalf("TERM not normalized in job env:\n%s", lines)
	}
}
