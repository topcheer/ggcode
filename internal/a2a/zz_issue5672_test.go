package a2a

import "testing"

// #5672 F2: on a same-host multi-workspace fleet the roster arm used to
// return on the FIRST host-matching entry, so gate decisions flipped with
// roster order (same peer passed brief, failed full seconds later). The
// arm must scan every host-matching entry and pass when ANY belongs to
// our workspace.
func TestIssue5672GateAnyHostMatchPasses(t *testing.T) {
	roster := []InstanceInfo{
		// Same host, OTHER workspace listed first - must not veto.
		{Workspace: "/ws/other", Endpoint: "http://192.168.31.9:7001"},
		{Workspace: "/ws/self", Endpoint: "http://192.168.31.9:7002"},
	}
	if !WorkspaceGateDecision("192.168.31.9:5555", roster, "/ws/self") {
		t.Fatal("same-host fleet with our workspace present must pass regardless of roster order")
	}
	// Order flipped: deterministic pass both ways.
	if !WorkspaceGateDecision("192.168.31.9:5555", reverseRoster5672(roster), "/ws/self") {
		t.Fatal("gate decision must be order-independent")
	}
}

func TestIssue5672GateAllForeignWorkspacesStillDeny(t *testing.T) {
	roster := []InstanceInfo{
		{Workspace: "/ws/other-a", Endpoint: "http://192.168.31.9:7001"},
		{Workspace: "/ws/other-b", Endpoint: "http://192.168.31.9:7002"},
	}
	if WorkspaceGateDecision("192.168.31.9:5555", roster, "/ws/self") {
		t.Fatal("same-host entries from foreign workspaces only must still deny (-32003)")
	}
}

func reverseRoster5672(in []InstanceInfo) []InstanceInfo {
	out := make([]InstanceInfo, len(in))
	for i, v := range in {
		out[len(in)-1-i] = v
	}
	return out
}
