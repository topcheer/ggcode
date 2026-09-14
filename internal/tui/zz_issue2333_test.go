package tui

// #2333: the WeCom CREATE flow intercepted errWecomEnableNeeded and ran
// the auto-enable mutation (#1792 case 3), but the BIND flow passed the
// internal sentinel through raw - the user saw the bare error string and
// the bind never executed. Source pin: the bind tail must intercept the
// sentinel and chain the enable mutation, mirroring the create flow.

import (
	"os"
	"strings"
	"testing"
)

func TestIssue2333BindFlowInterceptsEnableSentinel(t *testing.T) {
	b, err := os.ReadFile("wecom_panel.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	start := strings.Index(src, "func (m *Model) wecomBindTail")
	if start < 0 {
		t.Fatal("bind tail must exist as its own method")
	}
	end := strings.Index(src[start+1:], "\nfunc ")
	tail := src[start : start+1+end]
	if !strings.Contains(tail, "errors.Is(err, errWecomEnableNeeded)") {
		t.Fatal("bind tail must intercept the enable sentinel")
	}
	if !strings.Contains(tail, "wecomEnableMutation") {
		t.Fatal("interception must chain the enable mutation")
	}
	if !strings.Contains(tail, "wecomBindTail") {
		t.Fatal("post-enable continuation must retry the bind tail")
	}
	// The raw passthrough is gone: the old shape returned the sentinel
	// directly from the bind entry.
	entryRaw := src[strings.Index(src, "func (m *Model) bindWeComEntry"):start]
	// strip doc comments - they legitimately NAME the sentinel while
	// explaining the fix; only executable lines carry the contract.
	var entryCode []string
	for _, l := range strings.Split(entryRaw, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "//") {
			entryCode = append(entryCode, l)
		}
	}
	entry := strings.Join(entryCode, "\n")
	if strings.Contains(entry, "errWecomEnableNeeded") || strings.Contains(entry, "err: err}") {
		t.Fatal("bind entry must not pass start errors through raw")
	}
}
