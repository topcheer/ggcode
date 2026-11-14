package a2a

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"time"
)

// #5616 seat addressing. A same-workspace fleet registers every instance
// under the same project name, so name-based a2a target resolution cannot
// address a concrete seat. IdentityLookup bridges the lanchat seat domain
// (AgentNick/HumanNick per endpoint) into the a2a domain without importing
// internal/lanchat (no cycle): cmd wiring supplies the closure, the a2a
// package only consumes it.
type Identity struct {
	Nick     string
	Role     string
	Team     string
	LastSeen int64 // unix seconds; 0 = unknown
	Online   bool
}

// IdentityLookup maps an instance endpoint (http://host:port or host:port)
// to its seat identity. ok=false when the endpoint is unknown to the
// identity domain.
type IdentityLookup func(endpoint string) (Identity, bool)

// normEndpoint strips scheme and trailing slash so lanchat endpoints
// (http://host:port) and InstanceInfo endpoints compare equal.
func normEndpoint(endpoint string) string {
	e := strings.TrimSpace(endpoint)
	e = strings.TrimPrefix(e, "http://")
	e = strings.TrimPrefix(e, "https://")
	return strings.TrimSuffix(e, "/")
}

// enrichInstances copies seat identity into each instance (best effort:
// unknown endpoints keep zero-value identity fields).
func enrichInstances(instances []InstanceInfo, lookup IdentityLookup) []InstanceInfo {
	if lookup == nil {
		return instances
	}
	seen := make(map[string]Identity, len(instances))
	for i := range instances {
		ep := normEndpoint(instances[i].Endpoint)
		id, ok := seen[ep]
		if !ok {
			id, _ = lookup(instances[i].Endpoint)
			seen[ep] = id
		}
		if id.Nick != "" {
			instances[i].Nick = id.Nick
		}
		if id.Role != "" {
			instances[i].Role = id.Role
		}
		if id.Team != "" {
			instances[i].Team = id.Team
		}
		if id.LastSeen != 0 {
			instances[i].LastSeen = id.LastSeen
		}
	}
	return instances
}

const (
	probeTimeout    = 1500 * time.Millisecond
	freshLastSeenWL = 90 * time.Second // identity LastSeen freshness window
)

// probeAlive reports whether the instance endpoint currently accepts TCP
// connections. Used only when resolution must disambiguate same-name
// candidates or confirm a lone candidate is not a stale roster entry.
func probeAlive(endpoint string) bool {
	hostPort := normEndpoint(endpoint)
	if hostPort == "" {
		return false
	}
	conn, err := net.DialTimeout("tcp", hostPort, probeTimeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// identityFresh reports whether an identity last-seen is recent enough to
// stand in for a live probe (lanchat heartbeats are seconds-grained).
func identityFresh(id Identity, now time.Time) bool {
	if id.LastSeen <= 0 {
		return false
	}
	return now.Sub(time.Unix(id.LastSeen, 0)) <= freshLastSeenWL
}

// aliveByEvidence ranks candidates: identity-fresh beats probe beats stale,
// without probing when identity already proves liveness (cheap path).
// Returns (alive list, dead list).
func partitionAlive(cands []InstanceInfo, lookup IdentityLookup, now time.Time) (alive, dead []InstanceInfo) {
	for _, c := range cands {
		live := false
		if lookup != nil {
			if id, ok := lookup(c.Endpoint); ok && (id.Online || identityFresh(id, now)) {
				live = true
			}
		}
		if !live && probeAlive(c.Endpoint) {
			live = true
		}
		if live {
			alive = append(alive, c)
		} else {
			dead = append(dead, c)
		}
	}
	return alive, dead
}

// sortByRecency orders candidates newest-first by LastSeen then StartedAt
// (deterministic tie-break on ID).
func sortByRecency(cands []InstanceInfo) []InstanceInfo {
	out := append([]InstanceInfo(nil), cands...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastSeen != out[j].LastSeen {
			return out[i].LastSeen > out[j].LastSeen
		}
		si, sj := parseStarted(out[i].StartedAt), parseStarted(out[j].StartedAt)
		if !si.Equal(sj) {
			return si.After(sj)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func parseStarted(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// offlineErr renders the explicit "target is offline" verdict (#5616
// acceptance: an offline seat must never resolve to a wrong instance).
func offlineErr(target string, dead []InstanceInfo) error {
	var parts []string
	for _, d := range sortByRecency(dead) {
		parts = append(parts, fmt.Sprintf("%s (started %s)", d.DisplayName(), d.StartedAt))
	}
	return fmt.Errorf("target %q resolved to %d instance(s) but all are unreachable (offline): %s",
		target, len(dead), strings.Join(parts, "; "))
}

// pickHealthy resolves a candidate tier: single candidate passes a liveness
// check (a stale roster/mDNS entry must not be returned silently); multiple
// candidates collapse to the one live instance when the rest are dead
// (same-name collision), error otherwise.
func pickHealthy(target string, cands []InstanceInfo, lookup IdentityLookup, now time.Time) (*InstanceInfo, error) {
	if len(cands) == 1 {
		alive, _ := partitionAlive(cands, lookup, now)
		if len(alive) == 1 {
			return &alive[0], nil
		}
		return nil, offlineErr(target, cands)
	}
	alive, dead := partitionAlive(cands, lookup, now)
	switch {
	case len(alive) == 1:
		return &alive[0], nil
	case len(alive) == 0:
		return nil, offlineErr(target, dead)
	default:
		// Multiple live same-name peers: newest-Seen wins only when it is
		// strictly separable; otherwise surface the ambiguity.
		ordered := sortByRecency(alive)
		if len(ordered) > 1 && ordered[0].LastSeen != ordered[1].LastSeen {
			top := ordered[0]
			return &top, nil
		}
		var names []string
		for _, inst := range ordered {
			names = append(names, inst.DisplayName())
		}
		return nil, fmt.Errorf("ambiguous match %q: multiple live instances (%s). Use name:port to disambiguate.",
			target, strings.Join(names, ", "))
	}
}

// matchNick returns instances whose seat nick matches the target exactly
// (case-insensitive). HumanNick and the composite agent nick
// (<nick>_<role>) both resolve to the bare-nick seat.
func matchNick(instances []InstanceInfo, target string) []InstanceInfo {
	var out []InstanceInfo
	for i := range instances {
		n := strings.ToLower(instances[i].Nick)
		if n == "" {
			continue
		}
		if n == target || n+"_"+strings.ToLower(instances[i].Role) == target {
			out = append(out, instances[i])
		}
	}
	return out
}
