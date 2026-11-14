package a2a

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// seatAddrTestEnv stands up a registry-backed RemoteTool with a fixed
// instance set; identity is derived from the instances themselves.
type seatAddrTestEnv struct {
	t         *testing.T
	rt        *RemoteTool
	instances []InstanceInfo
}

func (e *seatAddrTestEnv) addLiveInstance(nick, ws, started string) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		e.t.Fatalf("listen: %v", err)
	}
	e.t.Cleanup(func() { _ = l.Close() })
	return e.addInstance(nick, ws, started, l.Addr().String(), true)
}

func (e *seatAddrTestEnv) addDeadInstance(nick, ws, started string) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		e.t.Fatalf("listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close() // free the port: nothing listens → dead
	return e.addInstance(nick, ws, started, "127.0.0.1:"+strconv.Itoa(port), false)
}

func (e *seatAddrTestEnv) addInstance(nick, ws, started, endpoint string, online bool) string {
	id := "inst-" + nick + "-" + endpoint
	inst := InstanceInfo{
		ID:        id,
		PID:       os.Getpid(),
		Workspace: ws,
		StartedAt: started,
		Endpoint:  "http://" + endpoint,
		Status:    "ready",
		Nick:      nick,
		Role:      "dev",
		Team:      "ggcode",
	}
	if online {
		inst.LastSeen = time.Now().Unix()
	}
	e.instances = append(e.instances, inst)
	return id
}

// rebuild constructs the RemoteTool over the current instance set.
func (e *seatAddrTestEnv) rebuild() {
	e.t.Helper()
	reg := &Registry{}
	reg.SetCachedInstances(e.instances)
	e.rt = NewRemoteTool(reg, "key")
	idmap := make(map[string]Identity, len(e.instances))
	for _, inst := range e.instances {
		idmap[normEndpoint(inst.Endpoint)] = Identity{
			Nick: inst.Nick, Role: inst.Role, Team: inst.Team,
			LastSeen: inst.LastSeen, Online: inst.LastSeen > 0,
		}
	}
	e.rt.SetIdentityLookup(func(endpoint string) (Identity, bool) {
		id, ok := idmap[normEndpoint(endpoint)]
		return id, ok
	})
}

func find(t *testing.T, rt *RemoteTool, target string) (*InstanceInfo, error) {
	t.Helper()
	return rt.FindInstance(target)
}

// Ruler 1: nick target hits exactly the named seat.
func TestSeatNickUniqueHit(t *testing.T) {
	e := &seatAddrTestEnv{t: t}
	ws := filepath.Join(os.TempDir(), "ggcode")
	e.addLiveInstance("dev_ggcode", ws, "2026-11-14T10:00:00Z")
	e.addLiveInstance("ggcxf_reviewer", ws, "2026-11-14T10:01:00Z")
	e.rebuild()

	inst, err := find(t, e.rt, "dev_ggcode")
	if err != nil {
		t.Fatalf("nick resolve: %v", err)
	}
	if inst.Nick != "dev_ggcode" {
		t.Fatalf("got %q want dev_ggcode", inst.Nick)
	}
	// composite nick_role form
	if inst, err = find(t, e.rt, "ggcxf_reviewer_dev"); err != nil || inst.Nick != "ggcxf_reviewer" {
		t.Fatalf("composite nick: inst=%v err=%v", inst, err)
	}
}

// Ruler 2: same-name collision (12 seats named ggcode) resolves healthy-first.
func TestSeatSameNameHealthyFirst(t *testing.T) {
	e := &seatAddrTestEnv{t: t}
	ws := filepath.Join(os.TempDir(), "ggcode")
	dead := e.addDeadInstance("old_seat", ws, "2026-11-14T08:00:00Z")
	live := e.addLiveInstance("new_seat", ws, "2026-11-14T10:00:00Z")
	e.rebuild()

	inst, err := find(t, e.rt, "ggcode")
	if err != nil {
		t.Fatalf("same-name resolve: %v", err)
	}
	if inst.ID != live {
		t.Fatalf("resolved %s want live %s (dead %s must never answer)", inst.ID, live, dead)
	}
}

// Ruler 3: all candidates dead → explicit offline verdict, never a seat.
func TestSeatOfflineExplicit(t *testing.T) {
	e := &seatAddrTestEnv{t: t}
	ws := filepath.Join(os.TempDir(), "ggcode")
	e.addDeadInstance("seat_a", ws, "2026-11-14T08:00:00Z")
	e.addDeadInstance("seat_b", ws, "2026-11-14T07:30:00Z")
	e.rebuild()

	_, err := find(t, e.rt, "ggcode")
	if err == nil {
		t.Fatal("want explicit offline error, got nil")
	}
	if !strings.Contains(err.Error(), "unreachable") || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline verdict not explicit: %v", err)
	}
}

// Ruler 3b: offline nick → explicit verdict naming the seat.
func TestSeatNickOfflineExplicit(t *testing.T) {
	e := &seatAddrTestEnv{t: t}
	ws := filepath.Join(os.TempDir(), "ggcode")
	e.addDeadInstance("dev_ggcode", ws, "2026-11-14T08:00:00Z")
	e.rebuild()

	_, err := find(t, e.rt, "dev_ggcode")
	if err == nil {
		t.Fatal("want offline error for dead nick target")
	}
	if !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("verdict: %v", err)
	}
}

// Ruler 4: name:port pins the exact instance, skipping nick tier.
func TestSeatNamePortExact(t *testing.T) {
	e := &seatAddrTestEnv{t: t}
	ws := filepath.Join(os.TempDir(), "ggcode")
	e.addLiveInstance("dev_ggcode", ws, "2026-11-14T10:00:00Z")
	e.addLiveInstance("other_seat", ws, "2026-11-14T10:01:00Z")
	e.rebuild()

	inst, err := find(t, e.rt, "ggcode:"+strings.Split(normEndpoint(e.instances[1].Endpoint), ":")[1])
	if err != nil {
		t.Fatalf("name:port resolve: %v", err)
	}
	if inst.Nick != "other_seat" {
		t.Fatalf("pinned wrong seat: %q", inst.Nick)
	}
}

// Roster/mDNS entries carry nick through discovery.
func TestSeatNickTXTAndRoster(t *testing.T) {
	fields := []string{"id=x", "workspace=/w", "nick=dev_ggcode", "role=dev", "team=ggcode"}
	got := parseTXTFields(fields)
	if got["nick"] != "dev_ggcode" || got["role"] != "dev" {
		t.Fatalf("txt parse: %v", got)
	}
	// roster file round-trip of identity fields
	dir := t.TempDir()
	info := InstanceInfo{ID: "i1", Workspace: "/w", Endpoint: "127.0.0.1:1", Nick: "dev_ggcode", Role: "dev", Team: "ggcode", LastSeen: 1700000000}
	writeRosterEntry(dir, info)
	got2 := ReadRoster(dir, "self")
	if len(got2) != 1 || got2[0].Nick != "dev_ggcode" || got2[0].LastSeen != 1700000000 {
		t.Fatalf("roster identity round-trip: %+v", got2)
	}
}
