package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sa-159: pre_compact / on_compaction lifecycle tests.
//
// Design contract being pinned:
//   - pre_compact runs SYNCHRONOUSLY (state must be persisted before the
//     summarizer condenses older turns) but is NON-BLOCKING (a hook can never
//     veto compaction - refusing to compact strands the session at PTL).
//   - on_compaction stays async fire-and-forget.
//   - The payload carries a trigger field: "auto", "reactive", or "manual".

// writeMarkerHook returns a command hook that appends a marker line to path.
func writeMarkerHook(path string) Hook {
	return Hook{
		Match:   "*",
		Command: "echo fired >> " + shellQuote(path),
	}
}

func readMarker(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read marker file: %v", err)
	}
	return strings.TrimSpace(string(b))
}

func TestDispatchPreCompactRunsSynchronously(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker.txt")
	cfg := HookConfig{PreCompact: []Hook{writeMarkerHook(marker)}}

	res := Dispatch(cfg, HookEnv{Event: EventPreCompact, TokenBefore: 100, CompactTrigger: "auto"})

	if !res.Allowed {
		t.Fatalf("pre_compact must never block, Allowed=false")
	}
	if got := readMarker(t, marker); got != "fired" {
		t.Fatalf("pre_compact hook did not run synchronously before Dispatch returned (marker=%q)", got)
	}
}

func TestDispatchPreCompactCannotBlock(t *testing.T) {
	blocking := Hook{
		Match:   "*",
		Command: "echo 'no compaction' >&2; exit 2",
	}
	cfg := HookConfig{PreCompact: []Hook{blocking}}

	res := Dispatch(cfg, HookEnv{Event: EventPreCompact, TokenBefore: 100, CompactTrigger: "auto"})

	if !res.Allowed {
		t.Fatalf("pre_compact exit 2 must NOT block compaction; got Allowed=false output=%q", res.Output)
	}
}

func TestDispatchPreCompactRoutesConfigNotOtherEvents(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker.txt")
	cfg := HookConfig{PreCompact: []Hook{writeMarkerHook(marker)}}

	// A tool event must not fire pre_compact hooks.
	Dispatch(cfg, HookEnv{Event: EventPreToolUse, ToolName: "write_file"})
	if got := readMarker(t, marker); got != "" {
		t.Fatalf("pre_compact hooks fired on pre_tool_use event (marker=%q)", got)
	}

	// And the configured pre_compact hooks do fire on the compact event.
	Dispatch(cfg, HookEnv{Event: EventPreCompact, TokenBefore: 5, CompactTrigger: "manual"})
	if got := readMarker(t, marker); got != "fired" {
		t.Fatalf("pre_compact hooks did not fire on pre_compact event (marker=%q)", got)
	}
}

func TestBuildPayloadCompactionTrigger(t *testing.T) {
	post := BuildPayload(HookEnv{
		Event:          EventOnCompaction,
		TokenBefore:    1000,
		TokenAfter:     400,
		CompactTrigger: "manual",
	})
	if post.Compaction == nil {
		t.Fatal("on_compaction payload missing compaction object")
	}
	if post.Compaction.Trigger != "manual" || post.Compaction.Reclaimed != 600 {
		t.Fatalf("on_compaction payload wrong: %+v", post.Compaction)
	}
	if s := string(post.JSON()); !strings.Contains(s, `"trigger":"manual"`) {
		t.Fatalf("serialized payload missing trigger: %s", s)
	}

	pre := BuildPayload(HookEnv{
		Event:          EventPreCompact,
		TokenBefore:    1000,
		CompactTrigger: "reactive",
	})
	if pre.Compaction == nil {
		t.Fatal("pre_compact payload missing compaction object")
	}
	if pre.Compaction.Trigger != "reactive" || pre.Compaction.TokenAfter != 0 || pre.Compaction.Reclaimed != 0 {
		t.Fatalf("pre_compact payload must carry only pre-state: %+v", pre.Compaction)
	}
	if s := string(pre.JSON()); strings.Contains(s, `"reclaimed":1000`) {
		t.Fatalf("pre_compact payload must not fabricate reclaimed tokens: %s", s)
	}
}

func TestValidateHooksPreCompact(t *testing.T) {
	valid := HookConfig{PreCompact: []Hook{{Match: "*", Command: "true"}}}
	if errs := ValidateHooks(valid); len(errs) != 0 {
		t.Fatalf("valid pre_compact config rejected: %v", errs)
	}

	missingCmd := HookConfig{PreCompact: []Hook{{Match: "*"}}}
	errs := ValidateHooks(missingCmd)
	if len(errs) != 1 || !strings.Contains(errs[0], "pre_compact[0]") {
		t.Fatalf("missing command not reported for pre_compact: %v", errs)
	}
}

func TestRunPreCompactHooksSetsEvent(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker.txt")
	cfg := HookConfig{PreCompact: []Hook{writeMarkerHook(marker)}}

	res := RunPreCompactHooks(cfg, HookEnv{TokenBefore: 7, CompactTrigger: "auto"})
	if res.Err != nil {
		t.Fatalf("unexpected hook error: %v", res.Err)
	}
	if got := readMarker(t, marker); got != "fired" {
		t.Fatalf("RunPreCompactHooks did not run configured hooks (marker=%q)", got)
	}
}
