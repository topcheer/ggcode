package tui

// #2224: pcConnectionStatus had three more hardcoded/raw sites beyond the
// four keys #1731 case 3 wired - error (the key diagnostic surface),
// the default branch (idle/connecting/reconnecting raw English), and the
// tail fallback (bypassing the existing starting key).

import (
	"os"
	"strings"
	"testing"
)

func TestIssue2224PCStatusFullyInternationalized(t *testing.T) {
	b, err := os.ReadFile("pc_panel.go")
	if err != nil {
		t.Skipf("layout changed: %v", err)
	}
	src := string(b)
	i := strings.Index(src, "func (m Model) pcConnectionStatus")
	if i < 0 {
		t.Fatal("pcConnectionStatus not found")
	}
	body := src[i:]
	for _, bad := range []string{
		`fmt.Sprintf("error: %s"`, // raw error
		`return a.Status`,         // raw default
		`return "starting..."`,    // hardcoded tail
	} {
		if strings.Contains(body, bad) {
			t.Errorf("pcConnectionStatus still contains %q", bad)
		}
	}
	for _, key := range []string{
		"panel.pc.status.error", "panel.pc.status.raw", "panel.pc.status.starting",
	} {
		if !strings.Contains(body, key) {
			t.Errorf("pcConnectionStatus must use %s", key)
		}
	}
	// Both locales carry the new keys.
	ib, _ := os.ReadFile("i18n_pc.go")
	cat := string(ib)
	for _, k := range []string{"panel.pc.status.error", "panel.pc.status.raw"} {
		if strings.Count(cat, `"`+k+`"`) != 2 {
			t.Errorf("%s must exist in BOTH locales", k)
		}
	}
}
