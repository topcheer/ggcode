package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/topcheer/ggcode/internal/permission"
)

// #5553: a restart/binary swap must prove the NEW binary's agent-core
// plumbing works before the restart arms. A --version check cannot catch
// the #5549 class regression (zero tool definitions -> every restarted
// seat silently paralyzed). SelfCheck boots the REAL builtin tool
// registry through the same RegisterBuiltinTools path the interactive
// runtime uses, then executes one trivial run_command round-trip and
// asserts the observed output.
const (
	selfCheckEchoMarker = "ggcode-selfcheck-roundtrip-ok"

	// selfCheckMinTools is a deliberately LOW floor: it exists to catch
	// the zero/near-zero-toolDefs failure class (#5549), not to pin the
	// exact builtin count (which changes every release).
	selfCheckMinTools = 20
)

// SelfCheck boots a real builtin registry and probes it. workingDir
// scopes the bypass policy the probe runs under ("" = cwd).
func SelfCheck(ctx context.Context, workingDir string) error {
	if workingDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("selfcheck: resolve working dir: %w", err)
		}
		workingDir = wd
	}
	registry := NewRegistry()
	policy := permission.NewConfigPolicyWithMode(nil, []string{workingDir}, permission.BypassMode)
	if err := RegisterBuiltinTools(registry, policy, workingDir, nil, nil); err != nil {
		return fmt.Errorf("selfcheck: builtin registry boot FAILED (toolDefs plumbing broken, cf. #5549): %w", err)
	}
	defer registry.CloseAll()
	return selfCheckProbe(ctx, registry)
}

// selfCheckProbe asserts the observable invariants of a booted registry:
// a non-trivial tool count and a live run_command echo round-trip. Split
// from SelfCheck so tests can pin the failure classifications against
// crafted registries without booting the full builtin set.
func selfCheckProbe(ctx context.Context, registry *Registry) error {
	if registry == nil {
		return fmt.Errorf("selfcheck: nil registry")
	}
	if n := len(registry.List()); n < selfCheckMinTools {
		return fmt.Errorf("selfcheck: registry exposes only %d tools (< %d) - zero-toolDefs class regression (cf. #5549)", n, selfCheckMinTools)
	}
	rc, ok := registry.Get("run_command")
	if !ok {
		return fmt.Errorf("selfcheck: run_command tool missing from registry")
	}
	res, err := rc.Execute(ctx, json.RawMessage(`{"command":"echo `+selfCheckEchoMarker+`"}`))
	if err != nil {
		return fmt.Errorf("selfcheck: run_command round-trip errored: %w", err)
	}
	if res.IsError {
		return fmt.Errorf("selfcheck: run_command round-trip returned tool error: %s", firstLine(res.Content))
	}
	if !strings.Contains(res.Content, selfCheckEchoMarker) {
		return fmt.Errorf("selfcheck: run_command round-trip output mismatch: marker %q not observed in result: %s", selfCheckEchoMarker, firstLine(res.Content))
	}
	return nil
}
