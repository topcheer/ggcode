package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/util"
)

// The Chrome profile is derived from the ggcode session on the context - one
// persistent profile per session, never a model-invented name.
func TestResolveProfile_FixedToSession(t *testing.T) {
	b := NewBrowser()

	// No session on the context (pipe mode before the session is confirmed):
	// the ephemeral default, nothing persistent on disk.
	got, err := b.resolveProfile(context.Background(), "", false)
	if err != nil || got != "default" {
		t.Fatalf("no session: got %q, %v; want default", got, err)
	}

	ctx := util.WithSessionID(context.Background(), "20261127-101500-00b389147c828ccc")
	got, err = b.resolveProfile(ctx, "", false)
	if err != nil || got != "sess-20261127-101500-00b389147c828ccc" {
		t.Fatalf("session: got %q, %v", got, err)
	}

	// Same session -> same profile (a resumed session keeps its logins).
	again, _ := b.resolveProfile(util.WithSessionID(context.Background(), "20261127-101500-00b389147c828ccc"), "", false)
	if again != got {
		t.Fatalf("profile must be stable for a session: %q vs %q", got, again)
	}

	// Another session -> another profile.
	other, _ := b.resolveProfile(util.WithSessionID(context.Background(), "20261127-101600-00b389147c828ccc"), "", false)
	if other == got {
		t.Fatalf("different sessions must get different profiles, both %q", got)
	}

	// Explicit opt-in to the one shared profile.
	got, err = b.resolveProfile(ctx, "", true)
	if err != nil || got != "system" {
		t.Fatalf("shared: got %q, %v; want system", got, err)
	}
}

// A stale caller passing "profile" (old prompts, skills, docs) gets a clear
// error instead of a silently ignored argument - the model can retry without it.
func TestResolveProfile_RejectsExplicitProfile(t *testing.T) {
	b := NewBrowser()
	ctx := util.WithSessionID(context.Background(), "20261127-101500-00b389147c828ccc")
	_, err := b.resolveProfile(ctx, "og-check", false)
	if err == nil {
		t.Fatal("passing profile must be rejected")
	}
	for _, want := range []string{"profile", "no longer supported", "sess-20261127-101500-00b389147c828ccc"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q must mention %q", err.Error(), want)
		}
	}
	// "system" by name is rejected like any other name; only shared_profile=true selects it.
	if _, err := b.resolveProfile(ctx, "system", false); err == nil {
		t.Fatal(`profile="system" must be rejected too - use shared_profile=true`)
	}
}

// The tool schema must not advertise a profile argument any more.
func TestBrowserSchema_HasNoProfileArgument(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(NewBrowser().Parameters(), &schema); err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["profile"]; ok {
		t.Fatal(`schema must not expose a "profile" property`)
	}
	if _, ok := props["shared_profile"]; !ok {
		t.Fatal(`schema must expose "shared_profile"`)
	}
}

// Execute itself refuses a call that carries "profile" - before any Chrome is
// started - and the message names the session's fixed profile.
func TestExecute_RejectsProfileArgumentBeforeLaunching(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	b := NewBrowser()
	defer b.Close()

	ctx := util.WithSessionID(context.Background(), "20261127-101500-00b389147c828ccc")
	res, err := b.Execute(ctx, json.RawMessage(`{"action":"navigate","url":"https://example.com","profile":"og-check"}`))
	if err != nil {
		t.Fatalf("Execute returned a Go error, want an IsError result: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "no longer supported") {
		t.Fatalf("want an IsError result explaining the fixed profile, got %+v", res)
	}
	if len(b.profiles) != 0 {
		t.Fatalf("no browser may be started for a rejected call, profiles=%d", len(b.profiles))
	}
}

// The session profile lands under ~/.ggcode/browser-profiles/sess-<id> and is
// reused for the same session.
func TestSessionProfile_PersistentDirPerSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	b := NewBrowser()
	defer b.Close()
	name, err := b.resolveProfile(util.WithSessionID(context.Background(), "20261127-101500-00b389147c828ccc"), "", false)
	if err != nil {
		t.Fatal(err)
	}
	p, err := b.getProfile(name, nil)
	if err != nil {
		t.Skipf("Chrome not available here: %v", err)
	}
	wantDir := filepath.Join(homeDir(), ".ggcode", "browser-profiles", "sess-20261127-101500-00b389147c828ccc")
	if info, err := os.Stat(wantDir); err != nil || !info.IsDir() {
		t.Fatalf("session profile must use %s (err=%v)", wantDir, err)
	}
	p2, err := b.getProfile(name, nil)
	if err != nil || p2 != p {
		t.Fatalf("session profile must be stable across calls: err=%v same=%v", err, p2 == p)
	}
}
