package update

// zz_r525_cov_test.go — r525/r519 update-track coverage lift.
// Targets: readCachedCheck/writeCachedCheck (update.go:413/436),
// checkWritable (:560), Prepare error paths (:115), tryGetVersion
// (detect.go:225), scanNpmDir/scanNpmDirWindows (detect.go:193/208),
// DetectDualScopeWindows non-windows branch (detect.go:31).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func newIsolatedService(t *testing.T, current string) *Service {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	svc := NewService(current, filepath.Join(tmp, "bin", "ggcode"), filepath.Join(tmp, "ggcode.yaml"), tmp)
	svc.CheckTTL = time.Hour
	return svc
}

func TestReadCachedCheck(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		svc := newIsolatedService(t, "v1.0.0")
		if _, ok := svc.readCachedCheck(); ok {
			t.Fatal("expected miss for missing cache file")
		}
	})
	t.Run("invalid json", func(t *testing.T) {
		svc := newIsolatedService(t, "v1.0.0")
		// writeCachedCheck creates the .ggcode dir; overwrite with garbage after.
		if err := svc.writeCachedCheck(cachedCheck{CurrentVersion: "v1.0.0", LatestVersion: "v1.1.0", CheckedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(svc.cachePath(), []byte("{not-json"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, ok := svc.readCachedCheck(); ok {
			t.Fatal("expected miss for invalid JSON")
		}
	})
	t.Run("version mismatch", func(t *testing.T) {
		svc := newIsolatedService(t, "v1.0.0")
		if err := svc.writeCachedCheck(cachedCheck{CurrentVersion: "v0.9.0", LatestVersion: "v1.1.0", CheckedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if _, ok := svc.readCachedCheck(); ok {
			t.Fatal("expected miss when cached CurrentVersion differs")
		}
	})
	t.Run("expired ttl", func(t *testing.T) {
		svc := newIsolatedService(t, "v1.0.0")
		if err := svc.writeCachedCheck(cachedCheck{CurrentVersion: "v1.0.0", LatestVersion: "v1.1.0", CheckedAt: time.Now().Add(-2 * time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if _, ok := svc.readCachedCheck(); ok {
			t.Fatal("expected miss past TTL (1h)")
		}
	})
	t.Run("ttl zero defaults to 12h", func(t *testing.T) {
		svc := newIsolatedService(t, "v1.0.0")
		svc.CheckTTL = 0
		if err := svc.writeCachedCheck(cachedCheck{CurrentVersion: "v1.0.0", LatestVersion: "v1.1.0", CheckedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if _, ok := svc.readCachedCheck(); !ok {
			t.Fatal("expected hit with CheckTTL=0 defaulting to 12h")
		}
	})
	t.Run("fresh hit", func(t *testing.T) {
		svc := newIsolatedService(t, "v1.0.0")
		if err := svc.writeCachedCheck(cachedCheck{CurrentVersion: "v1.0.0", LatestVersion: "v1.1.0", CheckedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		got, ok := svc.readCachedCheck()
		if !ok {
			t.Fatal("expected cache hit")
		}
		if got.LatestVersion != "v1.1.0" {
			t.Fatalf("latest = %q, want v1.1.0", got.LatestVersion)
		}
	})
}

func TestWriteCachedCheckRoundTrip(t *testing.T) {
	svc := newIsolatedService(t, "v1.0.0")
	in := cachedCheck{CurrentVersion: "v1.0.0", LatestVersion: "v1.2.0", CheckedAt: time.Now()}
	if err := svc.writeCachedCheck(in); err != nil {
		t.Fatalf("writeCachedCheck: %v", err)
	}
	data, err := os.ReadFile(svc.cachePath())
	if err != nil {
		t.Fatalf("cache file not written: %v", err)
	}
	var out cachedCheck
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("cache file not valid JSON: %v", err)
	}
	if out.LatestVersion != in.LatestVersion {
		t.Fatalf("round-trip latest = %q, want %q", out.LatestVersion, in.LatestVersion)
	}
}

func TestWriteCachedCheckUnwritable(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod 0o444 does not block same-user/root writes here")
	}
	// Directory-level chmod is unreliable under macOS /var/folders ACLs;
	// make the cache FILE itself read-only instead — WriteFile's truncate
	// open then fails deterministically for the same non-root user.
	svc := newIsolatedService(t, "v1.0.0")
	if err := svc.writeCachedCheck(cachedCheck{CurrentVersion: "v1.0.0"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(svc.cachePath(), 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(svc.cachePath(), 0o644) })
	if err := svc.writeCachedCheck(cachedCheck{CurrentVersion: "v1.0.0"}); err == nil {
		t.Fatal("expected write error for read-only cache file")
	}
}

func TestCheckWritableHappy(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	// checkWritable probes Dir(ExecPath); the directory must exist first.
	if err := os.MkdirAll(filepath.Join(tmp, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewService("v1.0.0", filepath.Join(tmp, "bin", "ggcode"), "", tmp)
	needsElev, err := svc.checkWritable()
	if err != nil {
		t.Fatalf("checkWritable: %v", err)
	}
	if needsElev {
		t.Fatal("writable dirs must not request elevation")
	}
}

func TestCheckWritableEmptyExecPathFallsBackToWd(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	svc := NewService("v1.0.0", "", "", tmp)
	if _, err := svc.checkWritable(); err != nil {
		t.Fatalf("empty ExecPath should fall back to cwd (writable): %v", err)
	}
}

func TestCheckWritableTargetDirUnwritable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows returns needsElevation=true here; covered on windows CI")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes through 0o500")
	}
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	execDir := filepath.Join(tmp, "locked-bin")
	if err := os.MkdirAll(execDir, 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewService("v1.0.0", filepath.Join(execDir, "ggcode"), "", tmp)
	if _, err := svc.checkWritable(); err != nil {
		t.Skipf("baseline not writable, cannot test degradation: %v", err)
	}
	if err := os.Chmod(execDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(execDir, 0o755) })
	needsElev, err := svc.checkWritable()
	if err == nil {
		t.Fatal("expected error for unwritable target dir")
	}
	if needsElev {
		t.Fatal("unix must not request elevation")
	}
}

func writeStubScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTryGetVersionStub(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stub; windows covered via PATH stub in a follow-up")
	}
	dir := t.TempDir()
	ok := writeStubScript(t, dir, "ok", "#!/bin/sh\necho v9.9.9\n")
	if got := tryGetVersion(ok); got != "v9.9.9" {
		t.Fatalf("tryGetVersion(ok) = %q, want v9.9.9", got)
	}
	fail := writeStubScript(t, dir, "fail", "#!/bin/sh\nexit 3\n")
	if got := tryGetVersion(fail); got != "" {
		t.Fatalf("tryGetVersion(failing) = %q, want empty", got)
	}
	if got := tryGetVersion(filepath.Join(dir, "missing")); got != "" {
		t.Fatalf("tryGetVersion(missing) = %q, want empty", got)
	}
}

func TestScanNpmDirFindsVersionedBinaries(t *testing.T) {
	npmDir := t.TempDir()
	platform := runtime.GOOS + "_" + runtime.GOARCH
	for _, v := range []string{"v1.0.0", "v1.1.0"} {
		bin := filepath.Join(npmDir, v, platform, "ggcode")
		if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(bin, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// empty version dir: check callback still invoked (existence decided by caller)
	if err := os.MkdirAll(filepath.Join(npmDir, "v2.0.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	// non-dir entry must be skipped
	if err := os.WriteFile(filepath.Join(npmDir, "stray-file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var paths []string
	scanNpmDir(npmDir, func(path, source string, priv bool) {
		paths = append(paths, path)
		if source != "npm" || priv {
			t.Errorf("scanNpmDir callback source=%q priv=%v, want npm/false", source, priv)
		}
	})
	if len(paths) != 3 { // v1.0.0, v1.1.0, v2.0.0 (no binary); stray-file skipped
		t.Fatalf("scanNpmDir reported %d paths (%v), want 3", len(paths), paths)
	}
}

func TestScanNpmDirMissing(t *testing.T) {
	called := false
	scanNpmDir(filepath.Join(t.TempDir(), "does-not-exist"), func(string, string, bool) { called = true })
	if called {
		t.Fatal("scanNpmDir must not call back for a missing dir")
	}
}

func TestScanNpmDirWindowsLayout(t *testing.T) {
	npmDir := t.TempDir()
	bin := filepath.Join(npmDir, "v3.0.0", "windows_"+runtime.GOARCH, "ggcode.exe")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	var got []string
	scanNpmDirWindows(npmDir, func(path, source string, priv bool) {
		got = append(got, path)
		if source != "npm" {
			t.Errorf("source = %q, want npm", source)
		}
	})
	if len(got) != 1 || got[0] != bin {
		t.Fatalf("scanNpmDirWindows = %v, want [%s]", got, bin)
	}
}

func TestDetectDualScopeWindowsNonWindowsNil(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("real windows branch covered by zz_r525_cov_windows_test.go")
	}
	if got := DetectDualScopeWindows(); got != nil {
		t.Fatalf("non-windows must return nil, got %+v", got)
	}
}

// latestRedirectServer serves the ResolveReleaseVersion redirect pattern
// (update_test.go:60-71); asset GETs return 404 — sufficient for the
// Prepare error paths tested below.
func latestRedirectServer(latest string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/topcheer/ggcode/releases/latest" {
			http.Redirect(w, r, "/topcheer/ggcode/releases/tag/"+latest, http.StatusFound)
			return
		}
		if r.URL.Path == "/topcheer/ggcode/releases/tag/"+latest {
			_, _ = w.Write([]byte("ok")) // final hop (update_test.go:69-71 pattern)
			return
		}
		http.NotFound(w, r)
	}))
}

func TestPrepareAlreadyUpToDate(t *testing.T) {
	server := latestRedirectServer("v1.0.0") // same as current
	defer server.Close()
	svc := newIsolatedService(t, "v1.0.0")
	svc.HTTPClient = rewriteClient(server.URL, server.Client())

	_, err := svc.Prepare(context.Background(), "")
	if !errors.Is(err, ErrAlreadyUpToDate) {
		t.Fatalf("Prepare err = %v, want ErrAlreadyUpToDate", err)
	}
}

func TestPrepareCheckFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()
	svc := newIsolatedService(t, "v1.0.0")
	svc.HTTPClient = rewriteClient(server.URL, server.Client())

	_, err := svc.Prepare(context.Background(), "")
	if err == nil || errors.Is(err, ErrAlreadyUpToDate) {
		t.Fatalf("Prepare err = %v, want a check failure", err)
	}
}

func TestPrepareCheckWritableFails(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod-based unwritable dir is unix-non-root only")
	}
	server := latestRedirectServer("v1.2.0")
	defer server.Close()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	execDir := filepath.Join(tmp, "locked-bin")
	if err := os.MkdirAll(execDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(execDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(execDir, 0o755) })
	svc := NewService("v1.0.0", filepath.Join(execDir, "ggcode"), "", tmp)
	svc.CheckTTL = time.Hour
	svc.HTTPClient = rewriteClient(server.URL, server.Client())

	_, err := svc.Prepare(context.Background(), "")
	if err == nil || errors.Is(err, ErrAlreadyUpToDate) {
		t.Fatalf("Prepare err = %v, want pre-flight writable failure", err)
	}
}
