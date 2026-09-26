package main

// Regression guard for entry-point assembly drift. The project memory (and
// cmd/ggcode conventions) require root.go (interactive) and pipe.go (headless)
// to keep their agent wiring behaviorally aligned. r127 found a real drift:
// pipe.go set ag.SetProbeKey(...) but root.go did not, so the REPL's reactive
// context-window inference after a prompt-too-long error was dead code
// (InferContextWindowFromError short-circuits on an empty key), while pipe
// mode recovered correctly.

import (
	"os"
	"strings"
	"testing"
)

const probeKeyLine = "ag.SetProbeKey(provider.MakeProbeKey(resolved.VendorID, resolved.BaseURL, resolved.Model))"

// sharedAgentWiring lists the agent calls that BOTH entry points must make so
// interactive and pipe mode stay behaviorally aligned.
var sharedAgentWiring = []string{
	"ag.SetPermissionPolicy(policy)",
	"ag.SetHookConfig(cfg.Hooks)",
	"ag.SetWorkingDir(workingDir)",
	"ag.SetCheckpointManager(checkpoint.NewManager(50))",
	"ag.SetSupportsVision(resolved.SupportsVision)",
	probeKeyLine,
}

func readAssemblySource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Skipf("layout changed: %v", err)
	}
	return string(b)
}

func TestAssemblyAgentWiringAlignedInBothEntryPoints(t *testing.T) {
	rootSrc := readAssemblySource(t, "root.go")
	pipeSrc := readAssemblySource(t, "pipe.go")
	for _, call := range sharedAgentWiring {
		if !strings.Contains(rootSrc, call) {
			t.Errorf("root.go missing agent wiring %q (pipe.go has it); interactive/pipe assembly drifted", call)
		}
		if !strings.Contains(pipeSrc, call) {
			t.Errorf("pipe.go missing agent wiring %q (root.go has it); interactive/pipe assembly drifted", call)
		}
	}
}
