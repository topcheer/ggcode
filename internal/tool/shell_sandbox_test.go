package tool

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSandboxPolicyDefaults(t *testing.T) {
	p := NewSandboxPolicy(false, nil, nil)
	if p.Enabled {
		t.Fatal("Enabled=false must stay off")
	}
	enabled := NewSandboxPolicy(true, nil, nil)
	if !enabled.Enabled {
		t.Fatal("Enabled=true must stay on")
	}
	// Platform default: network allowed (agents need fetch/git push).
	if !enabled.AllowNetwork {
		t.Fatal("nil AllowNetwork must default to allowed")
	}
	// Explicit opt-out is honored.
	denied := false
	p = NewSandboxPolicy(true, &denied, nil)
	if p.AllowNetwork {
		t.Fatal("AllowNetwork=false must be honored")
	}
}

func TestWrapShellCommandOS_NoopWhenDisabled(t *testing.T) {
	p := NewSandboxPolicy(false, nil, nil)
	cmd := &exec.Cmd{}
	wrapped, err := wrapShellCommandOS(cmd, t.TempDir(), p)
	if err != nil {
		t.Fatalf("disabled policy must not error: %v", err)
	}
	if wrapped {
		t.Fatal("disabled policy must not wrap")
	}
	if cmd.Path != "" {
		t.Fatalf("disabled policy must not touch cmd, got Path=%q", cmd.Path)
	}
}

func TestSandboxDeniedOutput(t *testing.T) {
	cases := []struct {
		name, out string
		want      bool
	}{
		{"operation not permitted", "touch: /etc/hosts: Operation not permitted", true},
		{"lowercase eperm", "permission denied, errno = 1, eperm", true},
		{"read-only file system", "failed to write: read-only file system", true},
		{"normal failure", "grep: no matches", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		if got := sandboxDeniedOutput(tc.out); got != tc.want {
			t.Errorf("%s: sandboxDeniedOutput(%q) = %v, want %v", tc.name, tc.out, got, tc.want)
		}
	}
}

func TestSandboxEPERMHintContent(t *testing.T) {
	if !strings.Contains(sandboxEPERMHint, "sandbox") {
		t.Fatalf("hint must mention the sandbox, got %q", sandboxEPERMHint)
	}
}

func TestResolveSandboxPathExisting(t *testing.T) {
	dir := t.TempDir()
	resolved, cleaned := resolveSandboxPath(dir)
	if !filepath.IsAbs(resolved) || !filepath.IsAbs(cleaned) {
		t.Fatalf("both forms must be absolute: %q / %q", resolved, cleaned)
	}
	if resolved != filepath.Clean(resolved) {
		t.Fatalf("resolved form must be clean: %q", resolved)
	}
	// On symlinked systems (/tmp -> /private/tmp) the resolved form may
	// differ from cleaned, but EvalSymlinks(cleaned) must equal resolved.
	if real, err := filepath.EvalSymlinks(cleaned); err == nil && real != resolved {
		t.Fatalf("resolved %q != EvalSymlinks(cleaned) %q", resolved, real)
	}
}

func TestResolveSandboxPathNonExisting(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "out", "new.txt")
	resolved, _ := resolveSandboxPath(missing)
	if !filepath.IsAbs(resolved) {
		t.Fatalf("resolved must be absolute: %q", resolved)
	}
	// The missing file's dir must be preserved under an existing root.
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", dir, err)
	}
	if !strings.HasPrefix(resolved, dir) && !strings.HasPrefix(resolved, real) {
		t.Fatalf("resolved %q must stay under %q", resolved, dir)
	}
	if filepath.Base(resolved) != "new.txt" {
		t.Fatalf("basename must survive resolution: %q", resolved)
	}
}

func TestErrSandboxUnavailableSentinel(t *testing.T) {
	if !errors.Is(errSandboxUnavailable, errSandboxUnavailable) {
		t.Fatal("sentinel must be identity-comparable")
	}
}

func TestSandboxLaunchPayloadRoundTrip(t *testing.T) {
	in := sandboxLaunchPayload{
		WritePaths:   []string{"/tmp", "/home/user/workspace"},
		AllowNetwork: true,
	}
	encoded, err := encodeSandboxLaunchPayload(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := decodeSandboxLaunchPayload(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.AllowNetwork != in.AllowNetwork {
		t.Errorf("AllowNetwork = %v, want %v", out.AllowNetwork, in.AllowNetwork)
	}
	if len(out.WritePaths) != len(in.WritePaths) {
		t.Fatalf("WritePaths = %v, want %v", out.WritePaths, in.WritePaths)
	}
	for i := range in.WritePaths {
		if out.WritePaths[i] != in.WritePaths[i] {
			t.Errorf("WritePaths[%d] = %q, want %q", i, out.WritePaths[i], in.WritePaths[i])
		}
	}
}

func TestDecodeSandboxLaunchPayloadRejectsBadInput(t *testing.T) {
	cases := []struct {
		name, payload string
	}{
		{"empty", ""},
		{"invalid json", "{not json"},
		{"oversized", strings.Repeat("a", sandboxLaunchPayloadMaxBytes+1)},
	}
	for _, tc := range cases {
		if _, err := decodeSandboxLaunchPayload(tc.payload); err == nil {
			t.Errorf("%s: decode must reject input of len %d", tc.name, len(tc.payload))
		}
	}
}

// The marker is the argv contract between the run_command rewrite path and
// the cmd/ggcode main hook. Renaming one side without the other silently
// breaks every sandboxed Linux command, so pin both spellings.
func TestSandboxLaunchMarkerExportContract(t *testing.T) {
	if SandboxLaunchMarker != sandboxLaunchMarker {
		t.Fatalf("export mismatch: %q != %q", SandboxLaunchMarker, sandboxLaunchMarker)
	}
	if SandboxLaunchMarker == "" {
		t.Fatal("marker must not be empty")
	}
	if !strings.HasPrefix(SandboxLaunchMarker, "__ggcode_") {
		t.Fatalf("marker must stay ggcode-namespaced, got %q", SandboxLaunchMarker)
	}
}

func TestSandboxWriteAllowPaths(t *testing.T) {
	workspace := t.TempDir()
	extra := t.TempDir()
	p := NewSandboxPolicy(true, nil, []string{extra, extra})
	out := sandboxWriteAllowPaths(workspace, p)
	if len(out) == 0 {
		t.Fatal("allow paths must not be empty")
	}
	contains := func(path string) bool {
		for _, got := range out {
			if got == path {
				return true
			}
		}
		return false
	}
	// Every entry must be absolute and deduplicated; the workspace and the
	// extra root must be present in their resolved or cleaned spelling
	// (symlinked roots like /tmp -> /private/tmp legitimately appear twice).
	seen := map[string]bool{}
	for _, path := range out {
		if !filepath.IsAbs(path) {
			t.Errorf("entry %q must be absolute", path)
		}
		if seen[path] {
			t.Errorf("entry %q duplicated", path)
		}
		seen[path] = true
	}
	realWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", workspace, err)
	}
	if !contains(workspace) && !contains(realWorkspace) {
		t.Errorf("workspace %q (or %q) missing from %v", workspace, realWorkspace, out)
	}
	realExtra, err := filepath.EvalSymlinks(extra)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", extra, err)
	}
	if !contains(extra) && !contains(realExtra) {
		t.Errorf("extra write path %q (or %q) missing from %v", extra, realExtra, out)
	}
}
