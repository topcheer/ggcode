package permission

import (
	"testing"
)

var adversarialPrefixes = []string{
	"",
	"timeout 5 ",
	"stdbuf -o0 ",
	"nohup ",
	"env X=1 ",
	"nice -n 5 ",
	"sudo ",
	"xargs -I{} ",
}

var criticalTargets = []string{
	"rm -rf $HOME",
	`rm -rf "$HOME"`,
	"rm -rf $HOME/",
	"rm -rf $HOME/projects",
	"rm -rf ~",
	"rm -r ~",
	"rm -rf ~/projects",
	"rm -rf /*",
	"rm -rf --no-preserve-root /",
	"mkfs /dev/sda",
}

func TestAdversarial_Invariants_HomeAndRootCriticalInEveryWrapper(t *testing.T) {
	d := NewDangerousDetector()
	for _, prefix := range adversarialPrefixes {
		for _, target := range criticalTargets {
			cmd := prefix + target
			if chk := d.Check(cmd); chk.Level < DangerCritical {
				t.Errorf("adversarial: %q = level %v, want >= Critical (wrapper must not erode the #1804/#3061 contract)", cmd, chk.Level)
			}

			quoted := prefix + "sh -c '" + target + "'"
			if chk := d.Check(quoted); chk.Level < DangerCritical {
				t.Errorf("adversarial: %q = level %v, want >= Critical (quoted sh -c shape)", quoted, chk.Level)
			}
		}
	}
}

func TestAdversarial_KnownMisses_MediumLayerColdWrappers(t *testing.T) {
	d := NewDangerousDetector()
	known := map[string]bool{
		"timeout 5 rm -f config.yaml": true,
		"stdbuf -o0 rm -r build/*":    true,
		"nohup rm -f config.yaml":     false,
	}
	for cmd, missing := range known {
		chk := d.Check(cmd)
		if missing {
			if chk.Level >= DangerCritical {
				t.Errorf("known-miss pin drifted (became Critical - update this pin): %q = %v", cmd, chk.Level)
			}
		} else if chk.Level < DangerMedium {
			t.Errorf("anchored wrapper lost Medium coverage: %q = %v, want >= Medium", cmd, chk.Level)
		}
	}
}
