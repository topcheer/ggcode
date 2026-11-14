package a2a

import (
	"strings"
	"testing"
	"time"
)

// #5620 gap: listInstances enriches discovery results with the live
// lanchat identity lookup so a published nick renders in the list
// surface without a process rebuild (FindInstance already enriches).

func TestIssue5616_NilLookupKeepsRowsRenderable(t *testing.T) {
	in := []InstanceInfo{{ID: "i1", Workspace: "/w", Endpoint: "http://h:1"}}
	out := enrichInstances(in, nil)
	if len(out) != 1 || out[0].Nick != "" {
		t.Fatalf("nil lookup mutated rows: %+v", out)
	}
}

func TestIssue5616_EnrichFillsNickForSeatLine(t *testing.T) {
	in := []InstanceInfo{{ID: "i1", Workspace: "/w", Endpoint: "http://192.168.31.105:54911"}}
	lookup := func(endpoint string) (Identity, bool) {
		if normEndpoint(endpoint) == "192.168.31.105:54911" {
			return Identity{Nick: "main_fullstack", Role: "fullstack", Team: "ggcode", LastSeen: time.Now().Unix(), Online: true}, true
		}
		return Identity{}, false
	}
	enriched := enrichInstances(in, lookup)
	if enriched[0].Nick != "main_fullstack" || enriched[0].Role != "fullstack" {
		t.Fatalf("enrich missed nick: %+v", enriched[0])
	}
	var sb strings.Builder
	for _, inst := range enriched {
		if inst.Nick != "" {
			sb.WriteString("  Seat: " + inst.Nick + " (role=" + inst.Role + ")\n")
		}
	}
	if got := sb.String(); !strings.Contains(got, "Seat: main_fullstack (role=fullstack)") {
		t.Fatalf("seat line missing: %q", got)
	}
}
