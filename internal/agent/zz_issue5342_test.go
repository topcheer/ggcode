package agent

import "testing"

// Pins #5342: the #5315 command-position tightening regressed two FN
// classes on the rm_rf critical gate -
//
//	A. wrapper prefixes: sudo/nohup/xargs/command (and env/time/nice/
//	   builtin/doas, path forms) ahead of rm left rm outside command
//	   position, so the most dangerous privileged recursive force-deletes
//	   were silently unflagged.
//	B. chain short-circuit: only the FIRST rm segment was checked, so
//	   `rm file && rm -rf dir` hid its recursive force-delete behind the
//	   plain leading segment.
//
// Fix: rmCommandSegments returns EVERY rm-at-command-position segment,
// skipping env-prefix assignments and wrapper words (reusing this file's
// heredocPrefixWords lexicon + xargs/doas from the permission layer's
// rmCmdAnchor), with path forms normalized.

func hasRmRfCritical(cmd string) bool {
	for _, p := range detectDestructiveInShellCommand(cmd) {
		if p.name == "rm_rf" {
			return true
		}
	}
	return false
}

func TestIssue5342WrapperPrefixedRmRfFires(t *testing.T) {
	positives := []struct{ name, cmd string }{
		{"sudo", "sudo rm -rf /tmp/x"},
		{"sudo path rm", "sudo /bin/rm -rf /tmp/x"},
		{"nohup", "nohup rm -rf logs/"},
		{"xargs", "xargs rm -rf < list.txt"},
		{"command", "command rm -rf /tmp/x"},
		{"env", "env rm -rf /tmp/x"},
		{"nice", "nice rm -rf /tmp/x"},
		{"builtin-adjacent wrapper env word", "time rm -rf /tmp/x"},
		{"doas", "doas rm -rf /tmp/x"},
		{"path sudo", "/usr/bin/sudo rm -rf /tmp/x"},
		{"env then sudo", "FOO=1 sudo rm -rf /tmp/x"},
		{"sudo then env", "sudo FOO=1 rm -rf /tmp/x"},
		{"chain: plain rm then wrapped rm -rf", "rm a.txt && sudo rm -rf /tmp/x"},
	}
	for _, tc := range positives {
		if !hasRmRfCritical(tc.cmd) {
			t.Errorf("%s: wrapper-prefixed rm -rf must fire rm_rf (regression #5342): %q", tc.name, tc.cmd)
		}
	}
}

func TestIssue5342ChainedNonFirstSegmentFires(t *testing.T) {
	positives := []string{
		"rm file.txt && rm -rf /tmp/x",
		"rm file.txt; rm -rf /tmp/x",
		"rm file.txt | rm -rf /tmp/x",
		"rm file.txt\nrm -rf /tmp/x",
		"cd /tmp && rm old && rm -rf build",
		"rm plain && sudo rm -rf /tmp/x",
	}
	for i, cmd := range positives {
		if !hasRmRfCritical(cmd) {
			t.Errorf("positive %d: non-first chain segment rm -rf must fire rm_rf (regression #5342): %q", i, cmd)
		}
	}
}

func TestIssue5342SegmentExtraction(t *testing.T) {
	// Direct API pin: both rm segments are returned, wrappers stripped
	// from the command-position walk.
	segs := rmCommandSegments("rm a.txt && sudo rm -rf /tmp/x")
	if len(segs) != 2 {
		t.Fatalf("want 2 rm segments, got %d: %v", len(segs), segs)
	}
	if segs[0] != "rm a.txt" {
		t.Errorf("segment 0 = %q, want %q", segs[0], "rm a.txt")
	}
	if segs[1] != "rm -rf /tmp/x" {
		t.Errorf("segment 1 = %q, want %q", segs[1], "rm -rf /tmp/x")
	}

	// Plain rm only mentions of rm never yield segments (issue's `echo`
	// FP-fix row stays fixed).
	if segs := rmCommandSegments(`echo rm -rf /tmp/x`); len(segs) != 0 {
		t.Errorf("echo'd rm must yield no segments, got %v", segs)
	}
}

func TestIssue5342FalsePositiveContractHeld(t *testing.T) {
	// #5315's FP fixes must survive this change.
	negatives := []string{
		`echo "rm -rf /tmp/x"`,
		"grep 'rm -rf' build.log",
		"echo hi && echo 'never run rm -rf /' && ls",
		"cat >> note.md <<'EOF'\nrm -rf /\nEOF",
	}
	for i, cmd := range negatives {
		if hasRmRfCritical(cmd) {
			t.Errorf("negative %d fired on text-only mention (FP regression): %q", i, cmd)
		}
	}
	// A wrapper word appearing as the receiver itself (rm never reached)
	// must not fire: sudo with no rm behind it.
	if hasRmRfCritical("sudo ls -la /tmp") {
		t.Error("sudo without rm must not fire rm_rf")
	}
	// Wrapper stop-word discipline: an rm that is an ARGUMENT of a
	// non-wrapper command (xargs consumes stdin; but `git rm` is a
	// different binary) stays out of scope here by design - `git rm -rf`
	// is a git operation, not /bin/rm.
	if segs := rmCommandSegments("git rm -rf --cached ."); len(segs) != 0 {
		t.Errorf("git rm must not yield a /bin/rm segment, got %v", segs)
	}
}
