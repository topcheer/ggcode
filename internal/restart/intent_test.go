package restart

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestEncodeDecodeIntentRoundTrip(t *testing.T) {
	in := Intent{Path: "/opt/ggcode/bin/ggcode", Dev: 12, Inode: 204720108, Revision: "1b8e2f481a2b", ArmedAt: time.Unix(1773000000, 123456789)}
	decoded, ok := DecodeIntent(EncodeIntent(in))
	if !ok {
		t.Fatal("round-trip decode failed")
	}
	if decoded.Path != in.Path || decoded.Dev != in.Dev || decoded.Inode != in.Inode || decoded.Revision != in.Revision {
		t.Fatalf("round-trip mismatch: got %+v want %+v", decoded, in)
	}
	if !decoded.ArmedAt.Equal(in.ArmedAt) {
		t.Fatalf("ArmedAt mismatch: got %v want %v", decoded.ArmedAt, in.ArmedAt)
	}
}

func TestDecodeIntentMalformed(t *testing.T) {
	for _, bad := range []string{"", "|1|2|rev|3", "/a/b|notanum|2|rev|3", "/a/b|1|notanum|rev|3", "/a/b|1|2|rev|notanum", "garbage", "/a/b|1|2|rev"} {
		if _, ok := DecodeIntent(bad); ok {
			t.Fatalf("DecodeIntent(%q) unexpectedly ok", bad)
		}
	}
}

func TestStartupVerifyNoticeNoIntent(t *testing.T) {
	t.Setenv(IntentEnvKey, "")
	if notice, _ := StartupVerifyNotice(); notice != "" {
		t.Fatalf("expected empty notice without intent env, got %q", notice)
	}
}

func TestStartupVerifyNoticeMalformedIntent(t *testing.T) {
	t.Setenv(IntentEnvKey, "garbage")
	if notice, _ := StartupVerifyNotice(); notice != "" {
		t.Fatalf("expected empty notice for malformed intent, got %q", notice)
	}
}

func TestStartupVerifyNoticeMatchSameInode(t *testing.T) {
	id, ok := CurrentIdentity()
	if !ok {
		t.Skip("current executable identity unavailable")
	}
	t.Setenv(IntentEnvKey, EncodeIntent(Intent{Path: id.Path, Dev: id.Dev, Inode: id.Inode, Revision: SelfRevision(), ArmedAt: time.Now().Add(-2 * time.Second)}))
	notice, matched := StartupVerifyNotice()
	if !matched {
		t.Fatalf("expected match, got notice: %s", notice)
	}
	if !strings.Contains(notice, "Restart verified") || !strings.Contains(notice, id.Path) {
		t.Fatalf("notice missing expected content: %s", notice)
	}
}

func TestStartupVerifyNoticeMatchByRevisionOnInodeChurn(t *testing.T) {
	rev := SelfRevision()
	if rev == "" {
		t.Skip("test binary built without vcs stamping")
	}
	id, _ := CurrentIdentity()
	t.Setenv(IntentEnvKey, EncodeIntent(Intent{Path: id.Path, Dev: id.Dev, Inode: id.Inode + 999, Revision: rev, ArmedAt: time.Now()}))
	notice, matched := StartupVerifyNotice()
	if !matched {
		t.Fatalf("expected revision-based match on rebuild-in-place, got notice: %s", notice)
	}
	if !strings.Contains(notice, "Restart verified") {
		t.Fatalf("notice missing expected content: %s", notice)
	}
}

func TestStartupVerifyNoticeMismatchWrongPath(t *testing.T) {
	id, ok := CurrentIdentity()
	if !ok {
		t.Skip("current executable identity unavailable")
	}
	t.Setenv(IntentEnvKey, EncodeIntent(Intent{Path: "/definitely/other/ggcode-rebuilt", Dev: id.Dev + 1, Inode: id.Inode + 1, Revision: "deadbeef0000", ArmedAt: time.Now().Add(-5 * time.Second)}))
	notice, matched := StartupVerifyNotice()
	if matched {
		t.Fatal("expected mismatch verdict for wrong-path exec")
	}
	for _, want := range []string{"restart-warn", "RESTART DID NOT SWAP", "/definitely/other/ggcode-rebuilt", id.Path} {
		if !strings.Contains(notice, want) {
			t.Fatalf("mismatch notice missing %q: %s", want, notice)
		}
	}
}

// TestExecRestartStampsIntent proves the intent env actually crosses a real
// exec: a child re-execs the test binary with ExecRestart and the helper
// below asserts the child saw GGCODE_RESTART_INTENT matching the exec'ed
// image identity. Skipped when the child-side env requests are absent.
func TestExecRestartStampsIntent(t *testing.T) {
	// Stage-2 child: ExecRestart brought us here. Report the stamped intent
	// and the running image identity, then exit.
	if os.Getenv("GGCODE_TEST_RESTART_STAGE") == "2" {
		raw := os.Getenv(IntentEnvKey)
		execPath, err := os.Executable()
		if err != nil {
			panic(err)
		}
		id, _ := StatIdentity(execPath)
		os.Stdout.WriteString("INTENT=" + raw + "\n")
		os.Stdout.WriteString("RESOLVED=" + id.Path + "\n")
		os.Stdout.WriteString("DEV=" + uitoa(id.Dev) + "\n")
		os.Stdout.WriteString("INODE=" + uitoa(id.Inode) + "\n")
		os.Exit(0)
	}

	// Stage-1 child: call ExecRestart on our own test binary with the stage
	// marker advanced to 2 so the exec'ed image lands in the stage-2 branch
	// (ExecRestart passes our env through, so we must flip the marker here).
	if os.Getenv("GGCODE_TEST_RESTART_STAGE") == "1" {
		execPath, err := os.Executable()
		if err != nil {
			panic(err)
		}
		env := os.Environ()
		for i, kv := range env {
			if strings.HasPrefix(kv, "GGCODE_TEST_RESTART_STAGE=") {
				env[i] = "GGCODE_TEST_RESTART_STAGE=2"
			}
		}
		if err := ExecRestart(execPath, []string{"-test.run", "TestExecRestartStampsIntent", "-test.v"}, env); err != nil {
			panic(err)
		}
		return
	}

	execPath, err := os.Executable()
	if err != nil {
		t.Skip("executable path unavailable")
	}

	// Parent: spawn child with stage marker; child calls ExecRestart which
	// execs the same test binary (stage 2 reports the stamped intent).
	cmd := exec.Command(execPath, "-test.run", "TestExecRestartStampsIntent", "-test.v")
	cmd.Env = append(os.Environ(), "GGCODE_TEST_RESTART_STAGE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child run failed: %v\n%s", err, out)
	}
	// The stage-2 output carries INTENT= after exec; find it.
	var intentRaw, devStr, inodeStr, resolved string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "INTENT=") {
			intentRaw = strings.TrimPrefix(line, "INTENT=")
		}
		if strings.HasPrefix(line, "DEV=") {
			devStr = strings.TrimPrefix(line, "DEV=")
		}
		if strings.HasPrefix(line, "INODE=") {
			inodeStr = strings.TrimPrefix(line, "INODE=")
		}
		if strings.HasPrefix(line, "RESOLVED=") {
			resolved = strings.TrimPrefix(line, "RESOLVED=")
		}
	}
	if intentRaw == "" {
		t.Fatalf("no INTENT reported after exec; child output:\n%s", out)
	}
	in, ok := DecodeIntent(intentRaw)
	if !ok {
		t.Fatalf("post-exec intent does not decode: %q", intentRaw)
	}
	if in.Dev != parseUint(t, devStr) || in.Inode != parseUint(t, inodeStr) {
		t.Fatalf("intent identity %+v does not match running image dev=%s inode=%s", in, devStr, inodeStr)
	}
	if in.Path != resolved {
		t.Fatalf("intent path %q != resolved running path %q", in.Path, resolved)
	}
}

func uitoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

func parseUint(t *testing.T, s string) uint64 {
	t.Helper()
	var v uint64
	if s == "" {
		t.Fatal("empty numeric field")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			t.Fatalf("bad numeric field %q", s)
		}
		v = v*10 + uint64(c-'0')
	}
	return v
}
