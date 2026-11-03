package tui

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

		bound := strings.Index(src, "entry.OccupiedBy != \"\"")
		if bound >= 0 && i > bound {
			t.Errorf("%s: Muted branch must precede the bound branch", c.file)
		}
	}
}
