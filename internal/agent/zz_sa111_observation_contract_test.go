package agent

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const (
	obsSHA     = "973360f2c0d1a3b4e5f60718293a4b5c6d7e8f90"
	obsSHATypo = "973360f2c0d1a3b4e5f60718293a4b5c6d7e8f91"
	obsPresign = "https://bucket.s3.amazonaws.com/key?X-Amz-Signature=abCd12Ef34&X-Amz-Expires=5"
	obsPrRef   = "#3970"
	obsPrTypo  = "#3971"
)

func TestSA111SHAMistranscriptionWarns(t *testing.T) {
	tr := newObservationContractTracker()
	tr.recordResult("run_command", "commit "+obsSHA+"\nAuthor: x", 1)

	hint := tr.checkArgs("git_revert", []byte(`{"commit":"`+obsSHATypo+`"}`), 2)
	if hint == "" {
		t.Fatal("expected integrity warning for 1-char-off SHA, got none")
	}
	if !strings.Contains(hint, "[observation-contract]") || !strings.Contains(hint, "mistranscribed") {
		t.Fatalf("hint malformed: %s", hint)
	}

	if h2 := tr.checkArgs("git_revert", []byte(`{"commit":"`+obsSHATypo+`"}`), 3); h2 != "" {
		t.Fatalf("expected fire-once suppression, got: %s", h2)
	}
}

func TestSA111ExactMatchSilent(t *testing.T) {
	tr := newObservationContractTracker()
	tr.recordResult("run_command", "commit "+obsSHA+" merged", 1)

	if h := tr.checkArgs("git_show", []byte(`{"revision":"`+obsSHA+`"}`), 2); h != "" {
		t.Fatalf("exact match must be silent, got: %s", h)
	}

	if h := tr.checkArgs("git_show", []byte(`{"revision":"0000000000000000000000000000000000000000"}`), 3); h != "" {
		t.Fatalf("unrelated SHA must be silent, got: %s", h)
	}

	tr.recordResult("run_command", "PR "+obsPrRef+" closed", 1)
	if h := tr.checkArgs("im", []byte(`{"message":"see "+obsPrTypo+" for context"}`), 4); h != "" {
		t.Fatalf("short artifact near-match must be silent, got: %s", h)
	}
}

func TestSA111PresignedURLExpiryWarns(t *testing.T) {
	tr := newObservationContractTracker()
	tr.recordResult("run_command", "upload ok: "+obsPresign, 1)
	h1 := tr.checkArgs("run_command", []byte(`{"command":"curl '`+obsPresign+`'"}`), 2)
	if h1 != "" {
		t.Fatalf("in-window exact reuse must be silent, got: %s", h1)
	}
	for i := range tr.artifacts {
		if tr.artifacts[i].ttlSecs == 5 {
			tr.artifacts[i].at = tr.artifacts[i].at.Add(-time.Minute)
			tr.artifacts[i].expiresAt = tr.artifacts[i].expiresAt.Add(-time.Minute)
		}
	}
	if len(tr.artifacts) == 0 || tr.artifacts[0].ttlSecs == 0 {
		t.Fatalf("artifact missing or TTL-less: %+v", tr.artifacts)
	}
	if !time.Now().After(tr.artifacts[0].expiresAt) {
		t.Fatalf("backdate failed: expiresAt=%v", tr.artifacts[0].expiresAt)
	}
	h2 := tr.checkArgs("run_command", []byte(`{"command":"curl '`+obsPresign+`'"}`), 3)
	if h2 == "" {
		t.Fatal("expected validity warning for expired presigned URL, got none")
	}
	if !strings.Contains(h2, "EXPIRED") {
		t.Fatalf("hint malformed: %s", h2)
	}
}

func TestSA111RingBufferAndReset(t *testing.T) {
	tr := newObservationContractTracker()
	for i := 0; i < obsMaxArtifacts+10; i++ {

		sha := fmt.Sprintf("%040x", i+1)
		tr.recordResult("run_command", sha, i+1)
	}
	if len(tr.artifacts) != obsMaxArtifacts {
		t.Fatalf("ring buffer should be exactly full, got %d", len(tr.artifacts))
	}
	tr.reset()
	if len(tr.artifacts) != 0 || len(tr.hintsGiven) != 0 {
		t.Fatal("reset must clear artifacts and hints")
	}
}

func TestSA111MalformedArgsNoPanic(t *testing.T) {
	tr := newObservationContractTracker()
	tr.recordResult("run_command", "sha "+obsSHA, 1)

	for _, args := range []string{`{"broken`, "\x00\xff\x01", `{}`, ""} {
		_ = tr.checkArgs("any", []byte(args), 2)
	}
}
