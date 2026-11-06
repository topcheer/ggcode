package sessionidxd

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/sessionidx"
)

// dialTimeout bounds every daemon interaction; a slow or wedged daemon must
// never stall a recall (fail → caller falls back to local Manager).
const dialTimeout = 500 * time.Millisecond

// Query asks the shared daemon for results across NON-live sessions.
// ok=false means "no daemon / daemon slow / daemon errored" — the caller
// falls back to its in-process Manager (graceful degradation, never data
// loss). liveSelf is excluded from results (the caller serves its own live
// session locally — it owns those writes).
func Query(home, sessionsDir string, terms []string, session, liveSelf string, limit int) ([]sessionidx.Result, bool) {
	if os.Getenv("GGCODE_SESSIONIDX_DAEMON") == "0" {
		return nil, false
	}
	client, okClient := dialClient(home)
	if !okClient {
		maybeSelfLaunch(home, sessionsDir)
		return nil, false
	}
	body, _ := json.Marshal(searchRequest{Terms: terms, Session: session, LiveSelf: liveSelf, Limit: limit})
	resp, err := client.Post("http://ggcode-sessionidxd/search", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	var sr searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil || !sr.OK {
		return nil, false
	}
	return sr.Results, true
}

func dialClient(home string) (*http.Client, bool) {
	addrFile, _, _ := Paths(home)
	raw, err := os.ReadFile(addrFile)
	if err != nil {
		return nil, false
	}
	addr := strings.TrimSpace(string(raw))
	network, target, ok := strings.Cut(addr, ":")
	if !ok || network != "unix" {
		return nil, false
	}
	// Reachability probe rides on the first real request (dialTimeout on
	// every socket op, so no extra ping needed).
	return &http.Client{
		Timeout: dialTimeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.DialTimeout(network, target, dialTimeout)
			},
		},
	}, true
}

// maybeSelfLaunch spawns the daemon detached when no discovery file exists.
// The daemon exits on its own after 30min idle, so a quiet machine keeps no
// resident process. Spawn failures are logged and ignored (client falls
// back).
func maybeSelfLaunch(home, sessionsDir string) {
	if _, _, pid := Paths(home); pid != "" {
		if raw, err := os.ReadFile(pid); err == nil {
			_ = raw // pidfile exists: daemon may be starting up; don't dogpile
			debug.Log("sessionidxd", "client: discovery missing but pidfile present — assuming daemon startup")
			return
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "sessionidxd-serve",
		"--sessions", sessionsDir,
		"--home", home)
	cmd.SysProcAttr = detachAttr()
	if err := cmd.Start(); err != nil {
		debug.Log("sessionidxd", "client: self-launch failed: %v", err)
		return
	}
	// Reap so we never leave a zombie.
	go func() { _ = cmd.Wait() }()
	debug.Log("sessionidxd", "client: spawned sessionidxd (pid %d)", cmd.Process.Pid)
}
