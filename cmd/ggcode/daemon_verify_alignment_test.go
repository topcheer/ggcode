package main

// Regression guard: verify config (verify.auto_after_run,
// verify.claims_supervision, verify.adversarial_review) must be wired on the
// main agent in every agent-constructing entrypoint. root.go and pipe.go had
// agentruntime.ApplyVerifyConfigToAgent from the start; daemon.go never did,
// so the flags silently no-op in daemon (IM/tunnel) sessions. The wiring was
// added to daemon.go next to ApplyResolvedLimitsToAgent, mirroring the
// root.go/pipe.go ordering.

import (
	"os"
	"strings"
	"testing"
)

func TestDaemonWiresVerifyConfigLikeRootAndPipe(t *testing.T) {
	call := "agentruntime.ApplyVerifyConfigToAgent(ag, cfg)"
	for _, f := range []string{"root.go", "pipe.go", "daemon.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Skipf("layout changed: %v", err)
		}
		if !strings.Contains(string(b), call) {
			t.Errorf("%s must wire verify config via %q; verify flags would silently no-op in this entrypoint", f, call)
		}
	}
}
