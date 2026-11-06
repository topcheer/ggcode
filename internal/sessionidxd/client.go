package sessionidxd

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/topcheer/ggcode/internal/sessionidx"
)

// dialTimeout bounds every host interaction; a slow or wedged host must
// never stall a recall (fail -> caller falls back to local Manager).
const dialTimeout = 500 * time.Millisecond

// Query asks the shared host for results across NON-live sessions.
// ok=false means "no host / host slow / host errored" — the caller falls
// back to its in-process Manager (graceful degradation, never data loss).
// liveSelf is excluded from results (the caller serves its own live
// session locally — it owns those writes).
//
// Single-binary constraint (user order): there is NO spawn path here. If
// no host is reachable, this instance simply uses its local Manager; the
// interactive runtime decides hosthood at startup via StartHost's
// transport election. GGCODE_SESSIONIDX_DAEMON=0 disables the client role.
func Query(home, sessionsDir string, terms []string, session, liveSelf string, limit int, workspace string) ([]sessionidx.Result, bool) {
	if os.Getenv("GGCODE_SESSIONIDX_DAEMON") == "0" {
		return nil, false
	}
	client, okClient := dialClient(home)
	if !okClient {
		return nil, false
	}
	body, _ := json.Marshal(searchRequest{Terms: terms, Session: session, LiveSelf: liveSelf, Limit: limit, Workspace: workspace})
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

// dialClient builds an HTTP client whose connections ride the platform
// transport (unix socket on darwin/linux, loopback+discovery-file on
// Windows) — the same election endpoint StartHost binds. Reachability is
// probed by the first real request (dialTimeout on every socket op), so a
// dead host costs one failed dial and the caller degrades locally.
func dialClient(home string) (*http.Client, bool) {
	tr := sessionidx.NewTransport(home)
	if _, err := tr.Dial(dialTimeout); err != nil {
		return nil, false
	}
	return &http.Client{
		Timeout: dialTimeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return tr.Dial(dialTimeout)
			},
		},
	}, true
}
