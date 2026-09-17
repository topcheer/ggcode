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
