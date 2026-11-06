// Package sessionidxd implements the machine-wide shared session-index
// daemon (#5149). One daemon process serves all ggcode instances on the
// machine so sibling .idx decoding happens once instead of per-process
// (measured 2026-11-06: 3.0GB of idx across 23 files vs N instances each
// loading a share — the fleet was shut down over this).
//
// v1 decisions (locked in the issue thread, QA-verdict informed):
//   - the daemon REUSES sessionidx.Manager for query semantics, so
//     daemon-vs-local result parity holds by construction;
//   - admission budget is machine-global (default 1GB, env override)
//     instead of Manager's 200MB per-file external cap — over-cap files
//     (894MB top file today) become searchable for the first time;
//   - transport: unix socket (darwin/linux). Windows v1: not wired here;
//     the client falls back to in-process Manager (status quo) — the
//     localhost+discovery-file transport is a follow-up slice;
//   - self-launch: the first client that fails to connect spawns
//     "<argv0> sessionidxd-serve" detached; idle-exit after 30min;
//   - refresh: 30s scan keyed on size+mtime (not mtime alone).
package sessionidxd

import (
	"encoding/json"
	"fmt"
	"net"
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
	// indexes. Must be >= the loadable set (742MB measured 2026-11-06);
	// 1GB default with env override GGCODE_SESSIONIDX_BUDGET.
	DefaultBudgetBytes int64 = 1 << 30
	// DefaultIdleExit: daemon exits after this long without any request.
	DefaultIdleExit = 30 * time.Minute
	// scanEvery: how often the daemon re-scans the idx dir (size+mtime keyed).
	scanEvery = 30 * time.Second
)

// Paths returns the daemon's discovery file and unix socket paths for a
// ggcode home (the dir holding sessions/, i.e. ~/.ggcode).
func Paths(home string) (addrFile, sockPath, pidFile string) {
	return filepath.Join(home, "sessionidxd.addr"),
		filepath.Join(home, "sessionidxd.sock"),
		filepath.Join(home, "sessionidxd.pid")
}

// Daemon is one shared-index server bound to a sessions dir.
type Daemon struct {
	sessionsDir string
	budget      int64
	mgr         *sessionidx.Manager
	lastUse     atomic.Int64 // unix nano of last request
	started     time.Time
}

// Serve runs the daemon foreground (used by the hidden `sessionidxd-serve`
// subcommand and tests). It blocks until the listener fails or the idle
// deadline passes.
func Serve(sessionsDir, home string) error {
	budget := DefaultBudgetBytes
	if v := os.Getenv("GGCODE_SESSIONIDX_BUDGET"); v != "" {
		var n int64
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			budget = n
		}
	}
	// Manager with machine budget: over-cap externals become loadable.
	mgr := sessionidx.NewManager(sessionsDir, "")
	mgr.SetExternalLimit(budget)
	// R1: machine-global decoded-bytes admission with LRU eviction - the
	// total budget is NOT the per-file cap; without this a single 894MB idx
	// would be decoded into the heap (the exact #5149 amplification).
	mgr.SetTotalBudget(budget)
	mgr.Start()

	d := &Daemon{sessionsDir: sessionsDir, budget: budget, mgr: mgr, started: time.Now()}
	_ = home

	addrFile, sockPath, pidFile := Paths(home)
	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("sessionidxd: listen %s: %w", sockPath, err)
	}

	// Discovery + pidfile (fixed machine-global paths — Correction C).
	if err := os.WriteFile(addrFile, []byte("unix:"+sockPath), 0o600); err != nil {
		ln.Close()
		return err
	}
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
		// non-fatal: addr discovery is what clients need
		debug.Log("sessionidxd", "pidfile write: %v", err)
	}
	defer os.Remove(addrFile)
	defer os.Remove(pidFile)
	defer ln.Close()

	d.lastUse.Store(time.Now().UnixNano())

	mux := http.NewServeMux()
	mux.HandleFunc("/search", d.handleSearch)
	mux.HandleFunc("/status", d.handleStatus)
	srv := &http.Server{Handler: mux}

	// Idle-exit + periodic rescan loop.
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
				idle := time.Since(time.Unix(0, d.lastUse.Load()))
				if idle > DefaultIdleExit {
					debug.Log("sessionidxd", "idle %.0fmin — exiting", idle.Minutes())
					srv.Close()
					return
				}
			}
		}
	})
	defer close(done)

	debug.Log("sessionidxd", "serving %s (budget=%dMB)", sockPath, budget>>20)
	err = srv.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
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
	// writes); the daemon serves everything else.
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
	DecodedMB      int64 `json:"decodedMb"`
	EvictedBytes   int64 `json:"evictedBytes"`
	EvictedIndexes int64 `json:"evictedIndexes"`
}

func (d *Daemon) handleStatus(w http.ResponseWriter, r *http.Request) {
	// R3: status is a health probe - it must NOT reset the idle clock,
	// otherwise a periodic liveness check keeps the daemon resident
	// forever (defeating idle-exit).
	evictedBytes, evictedCount := d.mgr.Evictions()
	writeJSON(w, statusResponse{OK: true, StartedAt: d.started, BudgetMB: d.budget >> 20, Indexes: d.mgr.IndexCount(),
		DecodedMB: d.mgr.TotalDecodedBytes() >> 20, EvictedBytes: evictedBytes, EvictedIndexes: evictedCount})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
