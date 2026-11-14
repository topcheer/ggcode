package a2a

import (
	"strings"
	"testing"
)

// #seat-addressing: nick resolution, same-name disambiguation, and the
// ordered candidate list for dead-endpoint resilience.

func TestSeatAddressing_NickExactBeatsProjectName(t *testing.T) {
	rt := NewRemoteTool(seedRegistry(t,
		InstanceInfo{ID: "1", Workspace: "/w/ggcode", Endpoint: "x:1", Nick: "dev_ggcode"},
		InstanceInfo{ID: "2", Workspace: "/w/ggcode", Endpoint: "x:2", Nick: "watchdog_dev"}), "k")
	inst, err := rt.FindInstance("dev_ggcode")
	if err != nil || inst == nil || inst.ID != "1" {
		t.Fatalf("nick resolution: %v %+v", err, inst)
	}
}

func TestSeatAddressing_FindInstancesOrdersNewestFirst(t *testing.T) {
	rt := NewRemoteTool(seedRegistry(t,
		InstanceInfo{ID: "old", Workspace: "/w/ggcode", Endpoint: "x:1", StartedAt: "2026-11-14T10:00:00Z"},
		InstanceInfo{ID: "new", Workspace: "/w/ggcode", Endpoint: "x:2", StartedAt: "2026-11-14T12:00:00Z"}), "k")
	cands, err := rt.FindInstances("ggcode")
	if err != nil || len(cands) != 2 {
		t.Fatalf("candidates: %v %d", err, len(cands))
	}
	if cands[0].ID != "new" {
		t.Fatalf("newest-first violated: %s then %s", cands[0].ID, cands[1].ID)
	}
}

func TestSeatAddressing_SameNameAmbiguityMentionsNick(t *testing.T) {
	rt := NewRemoteTool(seedRegistry(t,
		InstanceInfo{ID: "1", Workspace: "/w/ggcode", Endpoint: "x:1", Nick: "dev_ggcode"},
		InstanceInfo{ID: "2", Workspace: "/w/ggcode", Endpoint: "x:2", Nick: "watchdog_dev"}), "k")
	_, err := rt.FindInstance("ggcode")
	if err == nil || !strings.Contains(err.Error(), "nick") || !strings.Contains(err.Error(), "dev_ggcode") {
		t.Fatalf("ambiguous error must offer nicks: %v", err)
	}
}

// seedRegistry returns a Registry whose discovery serves the given list
// from cache (no mDNS in unit tests; same-package access).
func seedRegistry(t *testing.T, list ...InstanceInfo) *Registry {
	t.Helper()
	r, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	r.mdnsSvc = nil // force the cached path; a real mDNS lookup is empty in tests
	r.asyncCache = list
	r.asyncCacheOK = true
	return r
}

func TestSeatAddressing_DisplayNameKeepsPortUnderScheme(t *testing.T) {
	i := InstanceInfo{Workspace: "/w/ggcode", Endpoint: "http://192.168.1.5:5555"}
	if i.DisplayName() != "ggcode:5555" {
		t.Fatalf("scheme must be stripped so name:port survives: %q", i.DisplayName())
	}
}
