package permission

// Adversarial invariant probes (r371, inspired by agentic red-teaming:
// arXiv 2605.04019 / GPT-Red - systematically probe your own safety
// boundary with attack-shaped variants instead of waiting for a human
// reviewer to find each one, as happened for #1804/#778/#3059-#3062).
//
// The pinned contract (danger.go "Critical rm-root patterns stay
// \b-anchored anywhere in the command"): whole-home and whole-filesystem
// deletions are DangerCritical in EVERY wrapper shape, because
// bypass/autopilot only block >= Critical (config_policy.go). The
// Medium-layer rmCmdAnchor wrapper set is deliberately narrower; known
// Medium-layer misses under cold wrappers are pinned BELOW as documented
// known-misses, not accidentally as invariants.

import (
	"testing"
)

// adversarialPrefixes are command wrappers a real shell history shows.
var adversarialPrefixes = []string{
	"",            // bare
	"timeout 5 ",  // NOT in rmCmdAnchor's set
	"stdbuf -o0 ", // NOT in rmCmdAnchor's set
	"nohup ",      // in anchor set
	"env X=1 ",    // in anchor set
	"nice -n 5 ",  // in anchor set
	"sudo ",       // in anchor set
	"xargs -I{} ", // chained form
}

// criticalTargets: every shape must stay >= DangerCritical under every
// prefix, bare and sh -c quoted.
var criticalTargets = []string{
	"rm -rf $HOME",
	`rm -rf "$HOME"`,
	"rm -rf $HOME/",
	"rm -rf $HOME/projects",
	"rm -rf ~",
	"rm -r ~", // no -f at all
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
			// sh -c quoted form: the single quotes a user (or attacker
			// composing a nested command) would actually type.
			quoted := prefix + "sh -c '" + target + "'"
			if chk := d.Check(quoted); chk.Level < DangerCritical {
				t.Errorf("adversarial: %q = level %v, want >= Critical (quoted sh -c shape)", quoted, chk.Level)
			}
		}
	}
}

// TestAdversarial_KnownMisses_MediumLayerColdWrappers documents the
// Medium-layer gap from #3061's "attached doubt" note: rmCmdAnchor's
// wrapper set lacks timeout/stdbuf, so Medium workflow patterns (force
// rm, recursive wildcard rm) miss under those prefixes. Medium still
// requires confirmation in auto mode, so this is NOT a zero-confirm
// bypass - but when the anchor set grows, flip these pins to >= Medium.
func TestAdversarial_KnownMisses_MediumLayerColdWrappers(t *testing.T) {
	d := NewDangerousDetector()
	known := map[string]bool{
		"timeout 5 rm -f config.yaml": true,  // force-rm Medium misses "timeout 5 rm"
		"stdbuf -o0 rm -r build/*":    true,  // recursive-wildcard Medium misses too
		"nohup rm -f config.yaml":     false, // nohup IS in the anchor set
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
