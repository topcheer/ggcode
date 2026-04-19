package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIsNewerRelease(t *testing.T) {
	if !isNewerRelease("v1.2.4", "v1.2.3") {
		t.Fatal("expected v1.2.4 to be newer")
	}
	if isNewerRelease("v1.2.3", "v1.2.3") {
		t.Fatal("expected equal versions not to be newer")
	}
	if isNewerRelease("dev", "v1.2.3") {
		t.Fatal("expected invalid versions to compare false")
	}
}

func TestWrapperLatestPath(t *testing.T) {
	got, ok := wrapperLatestPath(filepath.FromSlash("/tmp/.cache/ggcode/npm/v1.0.0/linux-x64/ggcode"), "v1.1.0")
	if !ok {
		t.Fatal("expected wrapper path to be detected")
	}
	want := filepath.FromSlash("/tmp/.cache/ggcode/npm/v1.1.0/linux-x64/ggcode")
	if got != want {
		t.Fatalf("unexpected latest path: %q", got)
	}
}

func TestCheckUsesCache(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	svc := NewService("v1.0.0", "/tmp/ggcode", filepath.Join(tmp, "ggcode.yaml"), tmp)
	svc.CheckTTL = time.Hour
	if err := svc.writeCachedCheck(cachedCheck{
		CurrentVersion: "v1.0.0",
		LatestVersion:  "v1.1.0",
		CheckedAt:      time.Now(),
	}); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	check, err := svc.Check(context.Background())
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !check.HasUpdate || check.LatestVersion != "v1.1.0" {
		t.Fatalf("unexpected cached check result: %+v", check)
	}
}

func TestCheckFetchesLatestRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/topcheer/ggcode/releases/latest" {
			http.Redirect(w, r, "/topcheer/ggcode/releases/tag/v1.2.0", http.StatusFound)
			return
		}
		if r.URL.Path == "/topcheer/ggcode/releases/tag/v1.2.0" {
			_, _ = w.Write([]byte("ok"))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	client := server.Client()
	orig := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if orig != nil {
			return orig(req, via)
		}
		return nil
	}

	svc := NewService("v1.0.0", "/tmp/ggcode", filepath.Join(tmp, "ggcode.yaml"), tmp)
	svc.HTTPClient = rewriteClient(server.URL, client)

	check, err := svc.Check(context.Background())
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !check.HasUpdate || check.LatestVersion != "v1.2.0" {
		t.Fatalf("unexpected fetched check result: %+v", check)
	}
}

func rewriteClient(base string, client *http.Client) *http.Client {
	client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(base, "http://")
		return http.DefaultTransport.RoundTrip(req)
	})
	return client
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
