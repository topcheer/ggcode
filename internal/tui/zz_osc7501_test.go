package tui

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// OSC 7501 Program Status Protocol emission (mitchellh.com/writing/program-status-osc7501).
// Sequence shape: ESC ] 7501 ; state=<s>[:kind=<k>]:app=ggcode[:msg=<base64>] ESC \

func oscTestHarness(t *testing.T, enabled bool, tty bool) (*Model, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	origOut, origTTY := oscOut, oscIsTTY
	oscOut = &buf
	oscIsTTY = func() bool { return tty }
	t.Cleanup(func() { oscOut, oscIsTTY = origOut, origTTY })
	m := &Model{config: &config.Config{}}
	m.config.StatusLine.OSC7501 = enabled
	return m, &buf
}

func TestOSCStatus_DisabledByDefaultNoEmission(t *testing.T) {
	m, buf := oscTestHarness(t, false, true)
	m.oscWorking("secret")
	if buf.Len() != 0 {
		t.Fatalf("expected no emission when statusline.osc_7501 is off, got %q", buf.String())
	}
}

func TestOSCStatus_NonTTYNoEmission(t *testing.T) {
	m, buf := oscTestHarness(t, true, false)
	m.oscWorking("x")
	if buf.Len() != 0 {
		t.Fatalf("expected no emission on non-TTY stdout, got %q", buf.String())
	}
}

func TestOSCStatus_WorkingFormat(t *testing.T) {
	m, buf := oscTestHarness(t, true, true)
	m.oscWorking("")
	got := buf.String()
	want := "\x1b]7501;state=working:app=ggcode\x1b\\"
	if got != want {
		t.Fatalf("working = %q, want %q", got, want)
	}
}

func TestOSCStatus_BlockedIncludesKindAndBase64Msg(t *testing.T) {
	m, buf := oscTestHarness(t, true, true)
	m.oscBlocked(oscKindPermission, "Approval needed: run_command")
	got := buf.String()
	wantMsg := base64.StdEncoding.EncodeToString([]byte("Approval needed: run_command"))
	want := "\x1b]7501;state=blocked:kind=permission:app=ggcode:msg=" + wantMsg + "\x1b\\"
	if got != want {
		t.Fatalf("blocked = %q, want %q", got, want)
	}
	if !strings.HasPrefix(got, "\x1b]7501;") || !strings.HasSuffix(got, "\x1b\\") {
		t.Fatalf("sequence must be OSC-introduced and ST-terminated: %q", got)
	}
}

func TestOSCStatus_QuestionKind(t *testing.T) {
	m, buf := oscTestHarness(t, true, true)
	m.oscBlocked(oscKindQuestion, "Question: pick a plan")
	got := buf.String()
	if !strings.Contains(got, "state=blocked:kind=question:app=ggcode:msg=") {
		t.Fatalf("missing blocked/question fields: %q", got)
	}
}

func TestOSCStatus_RunOutcomeStates(t *testing.T) {
	cases := []struct {
		failed, canceled bool
		wantState        string
	}{
		{false, false, "state=done"},
		{true, false, "state=error"},
		{false, true, "state=idle"},
	}
	for _, c := range cases {
		m, buf := oscTestHarness(t, true, true)
		m.oscRunOutcome(c.failed, c.canceled)
		if !strings.Contains(buf.String(), c.wantState) {
			t.Fatalf("outcome(failed=%v,canceled=%v) = %q, want %q", c.failed, c.canceled, buf.String(), c.wantState)
		}
	}
}

func TestOSCStatus_TruncatesLongMsgOnRuneBoundary(t *testing.T) {
	long := strings.Repeat("你", oscMsgMax+20)
	if got := oscTruncateUTF8(long, oscMsgMax); len([]rune(got)) != oscMsgMax {
		t.Fatalf("truncate kept %d runes, want %d", len([]rune(got)), oscMsgMax)
	}
	m, buf := oscTestHarness(t, true, true)
	m.oscDone(long)
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(
		strings.Split(buf.String(), ":msg=")[1], ""), "\x1b\\"))
	if err != nil {
		t.Fatalf("msg not valid base64: %v", err)
	}
	if n := len([]rune(string(decoded))); n != oscMsgMax {
		t.Fatalf("emitted msg has %d runes, want %d", n, oscMsgMax)
	}
}

func TestOSCStatus_EmptyMsgOmitted(t *testing.T) {
	m, buf := oscTestHarness(t, true, true)
	m.oscDone("   ")
	if strings.Contains(buf.String(), ":msg=") {
		t.Fatalf("blank msg must be omitted: %q", buf.String())
	}
}
