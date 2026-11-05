package stt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #5095: in ensureAvailable's candidate loop, `probeErr = err` assigned the
// OUTER LookPath error (always nil at that point) instead of probeFlavor's
// error - the accurate probe diagnosis (e.g. whisper-cpp present but no ggml
// model) was silently replaced by the misleading "no whisper binary found in
// PATH". This test drives the real ensureAvailable path with a fake
// whisper-cpp binary on PATH and an empty model search dir: after the fix the
// unavailable reason must carry the ggml diagnosis, not the "binary not
// found" fallback.
func TestIssue5095ProbeErrorNotShadowed(t *testing.T) {
	t.Setenv("GGCODE_WHISPER_CPP_MODEL", "")
	restoreDirs := swapCPPModelSearchDirs(t, []string{t.TempDir()})
	defer restoreDirs()

	fakeBin := t.TempDir()
	script := "#!/bin/sh\nexit 0\n"
	for _, name := range []string{"whisper-cpp"} {
		p := filepath.Join(fakeBin, name)
		if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Replace PATH entirely: this host may carry a real `whisper` (OpenAI
	// flavor) binary that would resolve first and mark available; the probe
	// never executes the binary, so no system dirs are needed (#5095).
	t.Setenv("PATH", fakeBin)

	w := NewLocalWhisper("", "", "")
	err := w.ensureAvailable()
	if err == nil {
		t.Fatal("ensureAvailable must fail without a ggml model")
	}
	if strings.Contains(err.Error(), "no whisper binary found") {
		t.Errorf("#5095 shadowed: got the generic no-binary fallback, want the probe diagnosis; got: %v", err)
	}
	if !strings.Contains(err.Error(), "ggml") {
		t.Errorf("#5095 unavailable reason must carry the probe (ggml model) diagnosis, got: %v", err)
	}
}
