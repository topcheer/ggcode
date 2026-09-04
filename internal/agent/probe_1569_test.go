package agent

import (
	"fmt"
	"strings"
	"testing"
)

// TestProbe1569 pins #1569: substring -f in branch names must not fire,
// dry-run clean must not fire, +refspec force must fire.
func TestProbe1569(t *testing.T) {
	cases := []struct {
		cmd      string
		wantName string // "" = expect no fire
	}{
		{"git push origin hot-fix", ""},
		{"git push origin bug-fix", ""},
		{"git push --force origin main", "force_push"},
		{"git push -f origin main", "force_push"},
		{"git push origin +main", "force_push"},
		{"git clean -fdn", ""},
		{"git clean -n -fd", ""},
		{"git clean -fd", "clean_force"},
	}
	for _, c := range cases {
		pats := detectDestructiveInShellCommand(c.cmd)
		got := ""
		for _, p := range pats {
			if p.name == "force_push" || p.name == "clean_force" {
				got = p.name
			}
		}
		status := "OK"
		if got != c.wantName {
			status = "MISMATCH"
			t.Errorf("%-28s got=%q want=%q", c.cmd, got, c.wantName)
		}
		fmt.Printf("%-28s got=%-12q want=%-12q %s\n", c.cmd, got, c.wantName, status)
	}
}

// TestProbe1600 pins #1600: separator-bounded scanning, long-form
// dry-run/force, and lease-not-masking.
func TestProbe1600(t *testing.T) {
	cases := []struct {
		cmd      string
		wantFire bool
	}{
		{"git push origin main; make -f Makefile.build", false},
		{"git push origin main && make -fx", false},
		{"git clean --dry-run -fd", false},
		{"git clean --force -d", true},
		{"git push --force-with-lease origin main -f", true},
		{"git push --force-with-lease origin main", false},
	}
	for _, c := range cases {
		fired := isForcePushCommand(c.cmd)
		if strings.Contains(c.cmd, "clean") {
			fired = isCleanForceCommand(c.cmd)
		}
		if fired != c.wantFire {
			t.Errorf("%-45q fired=%v want=%v", c.cmd, fired, c.wantFire)
		}
	}
}
