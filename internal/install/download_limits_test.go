package install

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestDefaultHTTPClientHasTimeout guards #976: the fallback client must not
// be http.DefaultClient (Timeout == 0), otherwise a half-open mirror hangs
// the installer and the /update path forever.
func TestDefaultHTTPClientHasTimeout(t *testing.T) {
	client := DefaultHTTPClient()
	if client == nil {
		t.Fatal("DefaultHTTPClient returned nil")
	}
	if client == http.DefaultClient {
		t.Fatal("DefaultHTTPClient must not return http.DefaultClient (Timeout == 0)")
	}
	if client.Timeout != defaultDownloadTimeout {
		t.Fatalf("DefaultHTTPClient timeout = %v, want %v", client.Timeout, defaultDownloadTimeout)
	}
	if client.Timeout <= 0 {
		t.Fatal("DefaultHTTPClient must carry a positive Timeout")
	}
	if defaultDownloadTimeout != 5*time.Minute {
		t.Fatalf("defaultDownloadTimeout = %v, want 5m", defaultDownloadTimeout)
	}
}

// TestHTTPClientOrDefaultKeepsExplicitClient ensures injected clients pass
// through unchanged while nil swaps in the bounded default (#976).
func TestHTTPClientOrDefaultKeepsExplicitClient(t *testing.T) {
	if got := httpClientOrDefault(http.DefaultClient); got != http.DefaultClient {
		t.Fatal("explicitly injected clients must be passed through unchanged")
	}
	custom := &http.Client{Timeout: time.Second}
	if got := httpClientOrDefault(custom); got != custom {
		t.Fatal("custom client must be returned as-is")
	}
	if got := httpClientOrDefault(nil); got.Timeout != defaultDownloadTimeout {
		t.Fatalf("nil client must resolve to the bounded default, got timeout %v", got.Timeout)
	}
}

// TestDownloadWithNilClientSucceeds proves download() no longer panics or
// blocks forever when no client is injected: the nil guard substitutes the
// bounded default (#976).
func TestDownloadWithNilClientSucceeds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("payload"))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	data, err := download(ctx, nil, server.URL)
	if err != nil {
		t.Fatalf("download with nil client returned error: %v", err)
	}
	if string(data) != "payload" {
		t.Fatalf("unexpected payload: %q", string(data))
	}
}

// TestDownloadEnforcesSizeLimit guards the #976 memory cap: bodies larger
// than maxDownloadBytes must fail loudly instead of buffering unbounded
// data, and a body exactly at the cap must pass through intact.
func TestDownloadEnforcesSizeLimit(t *testing.T) {
	prev := maxDownloadBytes
	maxDownloadBytes = 8
	defer func() { maxDownloadBytes = prev }()

	atCap := strings.Repeat("a", 8)
	overCap := strings.Repeat("b", 9)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/at-cap":
			_, _ = w.Write([]byte(atCap))
		case "/over-cap":
			_, _ = w.Write([]byte(overCap))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	data, err := download(ctx, server.Client(), server.URL+"/at-cap")
	if err != nil {
		t.Fatalf("body exactly at the cap must download: %v", err)
	}
	if len(data) != 8 {
		t.Fatalf("at-cap body truncated: got %d bytes, want 8", len(data))
	}

	if _, err := download(ctx, server.Client(), server.URL+"/over-cap"); err == nil {
		t.Fatal("body over the cap must fail")
	} else if !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("over-cap error should mention the size limit, got: %v", err)
	}
}

// TestParseChecksumsNormalizesAssetNames guards the #976 fail-safe gap:
// checksum files regenerated with `sha256sum -b` prefix names with "*" and
// relative paths appear as "./name"; both must normalize to the bare asset
// name that verifyArchive looks up.
func TestParseChecksumsNormalizesAssetNames(t *testing.T) {
	body := strings.Join([]string{
		"abc123 *ggcode_linux_x86_64.tar.gz",   // sha256sum -b binary-mode marker
		"def456  ./ggcode_windows_x86_64.zip",  // relative-path prefix
		"xyz789 *./ggcode_darwin_arm64.tar.gz", // both prefixes
		"0f1e2d  ggcode_darwin_x86_64.tar.gz",  // goreleaser bare name
		"",
	}, "\n")

	got := parseChecksums(body)
	want := map[string]string{
		"ggcode_linux_x86_64.tar.gz":  "abc123",
		"ggcode_windows_x86_64.zip":   "def456",
		"ggcode_darwin_arm64.tar.gz":  "xyz789",
		"ggcode_darwin_x86_64.tar.gz": "0f1e2d",
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d entries (%v), want %d", len(got), got, len(want))
	}
	for name, sum := range want {
		if got[name] != sum {
			t.Fatalf("checksum for %s = %q, want %q (raw map: %v)", name, got[name], sum, got)
		}
	}
}

// TestWriteExecutableReplacesExisting proves the fsync'd write path still
// atomically replaces an existing binary (#976 regression guard).
func TestWriteExecutableReplacesExisting(t *testing.T) {
	path := t.TempDir() + "/ggcode"
	if err := WriteExecutable(path, []byte("first")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteExecutable(path, []byte("second")); err != nil {
		t.Fatalf("second write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "second" {
		t.Fatalf("unexpected content after replace: %q", string(data))
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("tmp file should not survive, stat err = %v", err)
	}
}
