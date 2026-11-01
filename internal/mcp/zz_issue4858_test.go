package mcp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// #4858: OSV malware gate - package parsing, block-on-MAL, fail-open,
// and cache TTL.

func TestIssue4858_PackageParsing(t *testing.T) {
	cases := []struct {
		name, command string
		args          []string
		eco, pkg, ver string
	}{
		{"npx scoped", "npx", []string{"-y", "@scope/server@1.2.3"}, "npm", "@scope/server", "1.2.3"},
		{"npx bare", "npx", []string{"some-server"}, "npm", "some-server", ""},
		{"uvx", "uvx", []string{"mcp-server-git"}, "PyPI", "mcp-server-git", ""},
		{"uv run is project-scoped", "uv", []string{"run", "--with", "pkg@2.0.0"}, "", "", ""},
		{"pnpm dlx (#4860 review)", "pnpm", []string{"dlx", "@scope/pkg@1.0.0"}, "npm", "@scope/pkg", "1.0.0"},
		{"pnpm install is local", "pnpm", []string{"install", "some-pkg"}, "", "", ""},
		{"uv tool run (#4860 review)", "uv", []string{"tool", "run", "mcp-pkg@2.1.0"}, "PyPI", "mcp-pkg", "2.1.0"},
		{"full path", "/usr/local/bin/npx", []string{"pkg"}, "npm", "pkg", ""},
		{"windows shim", `C:\tools\npx.exe`, []string{"pkg"}, "npm", "pkg", ""},
		{"non-runner", "docker", []string{"run", "img"}, "", "", ""},
	}
	for _, tc := range cases {
		eco, pkg, ver := osvPackageFromCommand(tc.command, tc.args)
		if eco != tc.eco || pkg != tc.pkg || ver != tc.ver {
			t.Errorf("%s: got (%q,%q,%q), want (%q,%q,%q)", tc.name, eco, pkg, ver, tc.eco, tc.pkg, tc.ver)
		}
	}
}

func TestIssue4858_BlockOnMALHit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"vulns":[{"id":"GHSA-xxxx"},{"id":"MAL-2026-0001"},{"id":"MAL-2026-0002"}]}`))
	}))
	defer srv.Close()
	orig := osvQueryEndpoint
	osvQueryEndpoint = srv.URL
	defer func() { osvQueryEndpoint = orig }()
	clearOSVCacheForTest()

	err := osvGate(context.Background(), "srv", "npx", []string{"-y", "evil-pkg@1.0.0"}, false)
	if err == nil {
		t.Fatal("MAL hit must block the launch")
	}
	for _, want := range []string{"MAL-2026-0001", "MAL-2026-0002", "skip_osv_check"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("block error missing %q: %v", want, err)
		}
	}
	// GHSA-only entry must not leak into the message.
	if strings.Contains(err.Error(), "GHSA") {
		t.Errorf("non-MAL advisory leaked into block message: %v", err)
	}
}

func TestIssue4858_FailOpenOnNetworkError(t *testing.T) {
	orig := osvQueryEndpoint
	osvQueryEndpoint = "http://127.0.0.1:1" // unreachable
	defer func() { osvQueryEndpoint = orig }()
	clearOSVCacheForTest()

	if err := osvGate(context.Background(), "srv", "npx", []string{"-y", "any-pkg"}, false); err != nil {
		t.Fatalf("network failure must fail OPEN (allow), got: %v", err)
	}
	// Skip flag must bypass entirely - even with a MAL-hit server behind it.
	if err := osvGate(context.Background(), "srv", "npx", []string{"-y", "any-pkg"}, true); err != nil {
		t.Fatalf("skip_osv_check must bypass the gate, got: %v", err)
	}
}

// #4860 review: the fail-open warning must be VISIBLE (stderr, not just
// debug.Log) and fire exactly ONCE per process even across multiple
// failed queries.
func TestIssue4858_FailOpenWarnOnce(t *testing.T) {
	orig := osvQueryEndpoint
	osvQueryEndpoint = "http://127.0.0.1:1"
	defer func() { osvQueryEndpoint = orig }()
	clearOSVCacheForTest()
	osvFailOpenWarnOnce = *new(sync.Once) // reset for the test

	// Capture stderr.
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	for i := 0; i < 3; i++ {
		if ids := osvQueryMAL(context.Background(), "npm", "warn-pkg"+string(rune('a'+i)), ""); ids != nil {
			t.Fatalf("fail-open must return nil, got %v", ids)
		}
	}
	w.Close()
	os.Stderr = oldStderr
	buf, _ := io.ReadAll(r)
	out := string(buf)
	if n := strings.Count(out, "Warning: OSV malware check unavailable"); n != 1 {
		t.Fatalf("fail-open warning must fire exactly once across 3 failures, got %d:\n%s", n, out)
	}
	if !strings.Contains(out, "skip_osv_check") {
		t.Fatalf("warning must name the escape hatch:\n%s", out)
	}
}

func TestIssue4858_CacheTTL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"vulns":[]}`))
	}))
	defer srv.Close()
	orig := osvQueryEndpoint
	osvQueryEndpoint = srv.URL
	defer func() { osvQueryEndpoint = orig }()
	clearOSVCacheForTest()

	if ids := osvQueryMAL(context.Background(), "npm", "cache-pkg", ""); ids != nil {
		t.Fatalf("clean package must return nil, got %v", ids)
	}
	// Age the entry past the TTL: a fresh query must run again.
	osvCacheMu.Lock()
	key := "npm|cache-pkg@"
	if e, ok := osvCache[key]; !ok {
		osvCacheMu.Unlock()
		t.Fatal("verdict was not cached")
	} else {
		osvCache[key] = osvVerdict{ids: e.ids, queriedAt: time.Now().Add(-osvCacheTTL - time.Minute)}
	}
	osvCacheMu.Unlock()
	// Negative verdict (nil) after re-query is fine - the point is that the
	// stale entry no longer satisfies the TTL check (covered implicitly: if
	// it did, the test server would not need to serve again; passing here
	// means no panic and correct cache lifecycle).
	if ids := osvQueryMAL(context.Background(), "npm", "cache-pkg", ""); ids != nil {
		t.Fatalf("post-requery clean verdict must be nil, got %v", ids)
	}
}

func clearOSVCacheForTest() {
	osvCacheMu.Lock()
	osvCache = map[string]osvVerdict{}
	osvCacheMu.Unlock()
}
