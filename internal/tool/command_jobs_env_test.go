package tool

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/util"
)

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

	if lines := runJob(`echo "[$GGCODE_PROBE_PLAIN_2350]"`); !strings.Contains(lines, "[must-pass-through]") {
		t.Fatalf("plain var missing from job env (stripping too aggressive?):\n%s", lines)
	}

	if lines := runJob(`echo "[$GGCODE_PROBE_SECRET_2350]"`); strings.Contains(lines, "leak-me-if-you-can") {
		t.Fatalf("secret leaked into child env via Manager.Start:\n%s", lines)
	}

	if lines := runJob(`echo "TERM=$TERM"`); !strings.Contains(lines, "TERM=dumb") {
		t.Fatalf("TERM not normalized in job env:\n%s", lines)
	}
}
