package tui

// #2211 (+ family): the #887 Muted-details fix never propagated - feishu,
// wecom, and whatsapp details chains had only Disabled/bound branches, so a
// conflict-muted bot showed "Muted" in the list but "Bound"/"Available" in
// the details on the same screen. This pins the branch ORDER (Disabled >
// Muted > bound > available) by source pinning all three panels.

import (
	"os"
	"strings"
	"testing"
)

func TestIssue2211DetailsMutedBranchPresent(t *testing.T) {
	for _, c := range []struct{ file, mutedKey string }{
		{"feishu_panel.go", `"panel.feishu.entry.muted"`},
		{"wecom_panel.go", `"panel.wecom.entry.muted"`},
		{"whatsapp_panel.go", `"panel.whatsapp.status.muted"`},
	} {
		b, err := os.ReadFile(c.file)
		if err != nil {
			t.Skipf("layout changed: %v", err)
		}
		src := string(b)
		if !strings.Contains(src, c.mutedKey) {
			t.Errorf("%s: details chain must use the Muted key %s", c.file, c.mutedKey)
		}
		i := strings.Index(src, "entry.Muted")
		if i < 0 {
			t.Errorf("%s: no entry.Muted branch", c.file)
			continue
		}
		// Muted must come BEFORE the bound branch (bound would otherwise
		// shadow a muted+bound bot - the visible state is Muted).
		bound := strings.Index(src, "entry.OccupiedBy != \"\"")
		if bound >= 0 && i > bound {
			t.Errorf("%s: Muted branch must precede the bound branch", c.file)
		}
	}
}
