// Package sessionidxd implements the machine-wide shared session-index
// service (#5149) under the SINGLE-BINARY constraint (user order): there is
// no separate daemon binary and no spawned process. The first interactive
// ggcode instance whose Transport.Listen() succeeds IS the host - the
// service runs in its process; every other instance is a client and falls
// back to its local Manager when no host answers. When the host exits, the
// next instance that tries becomes the host (stale endpoints are reclaimed
// by the transport's election semantics).
//
// Memory semantics (v1.3.275, QA-verified):
//   - machine-global decoded-bytes budget with LRU eviction (a single
//     894MB idx is never decoded whole into the heap);
//   - cold per-workspace load: nothing decoded at rest, first query
//     activates that workspace, 30min without a request unloads it;
//   - workspace scoping: results are filtered to the caller's project;
//   - refresh: 30s scan keyed on size+mtime (not mtime alone).
package sessionidxd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/sessionidx"
)

const (
	// DefaultBudgetBytes is the machine-global admission cap for decoded
	// indexes (env override GGCODE_SESSIONIDX_BUDGET).
	DefaultBudgetBytes int64 = 1 << 30
)

// scanEvery: how often the host re-scans the idx dir (size+mtime keyed)
// and tails missing indexes. Package-level var (not const) so tests can
// shrink the tick.
var scanEvery = 30 * time.Second

// Paths returns the discovery artifact paths for a ggcode home (the dir
// holding sessions/, i.e. ~/.ggcode). The addr file is written for
// rolling-upgrade compatibility with older clients that parse it; current
// clients dial through the platform transport instead.
func Paths(home string) (addrFile, sockPath string) {
	return filepath.Join(home, "sessionidxd.addr"),
		filepath.Join(home, "idxd.sock")
}

// Daemon is one shared-index server bound to a sessions dir.
type Daemon struct {
	sessionsDir string
	budget      int64
	mgr         *sessionidx.Manager
	lastUse     atomic.Int64 // unix nano of last search request
	started     time.Time
}

func budgetFromEnv() int64 {
	if v := os.Getenv("GGCODE_SESSIONIDX_BUDGET"); v != "" {
		var n int64
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			return n
		}
	}
	return DefaultBudgetBytes
}

// StartHost attempts to make THIS process the machine's shared-index host.
// Election is the transport bind: success = we are the host; a live host
// already holding the endpoint returns (nil, nil) and this instance stays
// a client; a stale endpoint left by a crashed previous host is reclaimed.
// The returned stop func tears the service down; passing nil stop with nil
// err means "another live host exists".
// GGCODE_SESSIONIDX_DAEMON=0 disables the host role entirely.
func StartHost(sessionsDir, home string) (stop func(), err error) {
	if os.Getenv("GGCODE_SESSIONIDX_DAEMON") == "0" {
		return nil, nil
	}
	budget := budgetFromEnv()

	// Manager with machine budget + cold per-workspace loading: nothing is
	// decoded at rest; the first query activates one workspace; 30min idle
	// unloads it. The total budget is NOT the per-file cap - without it a
	// single 894MB idx would be decoded into the heap (the exact #5149
	// amplification this whole effort removes).
	mgr := sessionidx.NewManager(sessionsDir, "")
	mgr.SetExternalLimit(budget)
	mgr.SetTotalBudget(budget)
	mgr.Start()

	d := &Daemon{sessionsDir: sessionsDir, budget: budget, mgr: mgr, started: time.Now()}
	tr := sessionidx.NewTransport(home)
	ln, err := tr.Listen()
	if err != nil {
		mgr.Stop()
		if errors.Is(err, sessionidx.ErrLeaderAlive) {
			// A live host exists: this instance is a client.
			return nil, nil
		}
		return nil, fmt.Errorf("sessionidxd: bind: %w", err)
	}

	// Rolling-upgrade compat: older clients parse sessionidxd.addr
	// ("unix:<path>"). Current clients dial through the transport.
	addrFile, _ := Paths(home)
	if desc := tr.Descriptor(); desc != "" {
		_ = os.WriteFile(addrFile, []byte(desc), 0o600)
	}

	d.lastUse.Store(time.Now().UnixNano())

	mux := http.NewServeMux()
	mux.HandleFunc("/search", d.handleSearch)
	mux.HandleFunc("/status", d.handleStatus)
	srv := &http.Server{Handler: mux}

	// Background refresh (size+mtime keyed). NO idle exit: the host is a
	// live ggcode instance, not a resident daemon - it disappears when its
	// instance exits, and another instance re-elects on its next start.
	done := make(chan struct{})
	safego.Go("sessionidxd.watch", func() {
		t := time.NewTicker(scanEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				d.mgr.RefreshExternal()
				// ① (#5149 idx-123): host owns indexes for sessions that
				// never got one (dead/foreign writers). Existing files stay
				// writer-owned; oversized sources stay source-scanned.
				d.mgr.TailMissingSources(sessionidx.DefaultTailMaxSourceBytes)
			}
		}
	})
	safego.Go("sessionidxd.serve", func() {
		if serr := srv.Serve(ln); serr != http.ErrServerClosed {
			debug.Log("sessionidxd", "serve: %v", serr)
		}
	})
	debug.Log("sessionidxd", "host up (%s, budget=%dMB)", tr.Name(), budget>>20)

	stop = func() {
		close(done)
		srv.Close()
		ln.Close()
		mgr.Stop()
		_ = os.Remove(addrFile)
	}
	return stop, nil
}

func (d *Daemon) touch() { d.lastUse.Store(time.Now().UnixNano()) }

type searchRequest struct {
	Terms    []string `json:"terms"`
	Session  string   `json:"session"`
	Limit    int      `json:"limit"`
	LiveSelf string   `json:"liveSelf"` // caller's live session id (excluded from daemon results)
	// Workspace scopes results to the caller's project (#5149): the
	// machine-global index holds every workspace's sessions.
	Workspace string `json:"workspace"`
}

type searchResponse struct {
	OK      bool                `json:"ok"`
	Results []sessionidx.Result `json:"results,omitempty"`
	Error   string              `json:"error,omitempty"`
}

func (d *Daemon) handleSearch(w http.ResponseWriter, r *http.Request) {
	d.touch()
	var req searchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, searchResponse{OK: false, Error: "bad request: " + err.Error()})
		return
	}
	// The caller handles its OWN live session locally (it owns those
	// writes); the host serves everything else.
	res := d.mgr.QueryScoped(req.Terms, req.Session, req.Limit, req.Workspace)
	out := make([]sessionidx.Result, 0, len(res))
	for _, x := range res {
		if x.SessionID == req.LiveSelf {
			continue
		}
		out = append(out, x)
	}
	writeJSON(w, searchResponse{OK: true, Results: out})
}

type statusResponse struct {
	OK        bool      `json:"ok"`
	StartedAt time.Time `json:"startedAt"`
	BudgetMB  int64     `json:"budgetMb"`
	Indexes   int       `json:"indexes"`
	// R1: budget pressure visibility (QA requirement).
	DecodedMB        int64 `json:"decodedMb"`
	ActiveWorkspaces int   `json:"activeWorkspaces"`
	EvictedBytes     int64 `json:"evictedBytes"`
	EvictedIndexes   int64 `json:"evictedIndexes"`
}

func (d *Daemon) handleStatus(w http.ResponseWriter, r *http.Request) {
	// R3: status is a health probe - it must NOT reset the idle clock.
	// (The cold-load idle window tracks search requests only.)
	evictedBytes, evictedCount := d.mgr.Evictions()
	writeJSON(w, statusResponse{OK: true, StartedAt: d.started, BudgetMB: d.budget >> 20, Indexes: d.mgr.IndexCount(),
		DecodedMB: d.mgr.TotalDecodedBytes() >> 20, ActiveWorkspaces: d.mgr.ActiveWorkspaces(), EvictedBytes: evictedBytes, EvictedIndexes: evictedCount})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
