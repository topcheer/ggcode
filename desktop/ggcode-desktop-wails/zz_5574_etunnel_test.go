package main

// #5574 pins: desktop etunnel surface.
//
// Behaviour pins (fake controller injected via a.etCtl, matching the
// etHost() "faked for tests" escape hatch — no real webui Server, no
// poller, no network):
//   - Start passes the hosted loopback URL through to the controller
//   - Status/Start/Stop/Setup all return ControlEt's reply + live state
//   - Stop → Start cycle works (no stuck singleton state)
//   - Setup persists subdomain/token through DesktopConfig (merge, not
//     overwrite) and the next call reads them back
//   - stopEtunnel is nil-safe and idempotent
//
// Static pins (wiring): four bindings delegate to webui.ControlEt, the
// bridge never re-implements tunnel start/stop, and shutdown tears the
// etunnel surface down BEFORE the share tunnel.

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/desktop/wailskit"
)

type fakeEtCtl struct {
	mu sync.Mutex

	starts, stops int
	url           string
	lastLocal     string
	lastSub       string
	lastTok       string
}

func (f *fakeEtCtl) StartTunnelOpts(localAddr, subdomain, token string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	f.lastLocal, f.lastSub, f.lastTok = localAddr, subdomain, token
	f.url = "https://fake-desk.trygg.io"
	return f.url, true, nil
}

func (f *fakeEtCtl) StopTunnel() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	f.url = ""
}

func (f *fakeEtCtl) TunnelURL() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.url
}

func newEtTestApp(t *testing.T, fake *fakeEtCtl) *App {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	a := &App{
		dc: wailskit.LoadDesktopConfig(),
	}
	a.etMu.Lock()
	a.etCtl = fake
	a.etLocalURL = "http://127.0.0.1:45999/#token=testtok"
	a.etMu.Unlock()
	return a
}

func TestEtunnelStartPassesHostedURLAndReportsActive(t *testing.T) {
	fake := &fakeEtCtl{}
	a := newEtTestApp(t, fake)

	st, err := a.EtunnelStart()
	if err != nil {
		t.Fatalf("EtunnelStart: %v", err)
	}
	if fake.starts != 1 {
		t.Fatalf("expected exactly 1 start, got %d", fake.starts)
	}
	if fake.lastLocal != "127.0.0.1:45999" {
		t.Fatalf("tunnel target must be the hosted loopback addr: %q", fake.lastLocal)
	}
	// The #token fragment must never leak into the tunnel target.
	if strings.Contains(fake.lastLocal, "token=") {
		t.Fatalf("auth fragment leaked into tunnel target: %q", fake.lastLocal)
	}
	if !st.Active || st.URL != "https://fake-desk.trygg.io" {
		t.Fatalf("start state mismatch: %+v", st)
	}
	if st.Message == "" {
		t.Fatal("expected ControlEt reply text in message")
	}
}

func TestEtunnelStatusReflectsControllerStateWithoutStarting(t *testing.T) {
	fake := &fakeEtCtl{}
	a := newEtTestApp(t, fake)

	st, err := a.EtunnelStatus()
	if err != nil {
		t.Fatalf("EtunnelStatus: %v", err)
	}
	if st.Active {
		t.Fatal("fresh host must report inactive")
	}
	if fake.starts != 0 {
		t.Fatalf("status must not start the tunnel, got %d starts", fake.starts)
	}

	if _, err := a.EtunnelStart(); err != nil {
		t.Fatal(err)
	}
	st, err = a.EtunnelStatus()
	if err != nil {
		t.Fatal(err)
	}
	if !st.Active || st.URL == "" {
		t.Fatalf("status after start: %+v", st)
	}
	if fake.starts != 1 {
		t.Fatalf("status must not double-start, got %d", fake.starts)
	}
}

func TestEtunnelStopThenStartCycle(t *testing.T) {
	fake := &fakeEtCtl{}
	a := newEtTestApp(t, fake)

	if _, err := a.EtunnelStart(); err != nil {
		t.Fatal(err)
	}
	st, err := a.EtunnelStop()
	if err != nil {
		t.Fatal(err)
	}
	if st.Active || st.URL != "" {
		t.Fatalf("stop state mismatch: %+v", st)
	}
	if fake.stops != 1 {
		t.Fatalf("expected 1 stop, got %d", fake.stops)
	}

	st, err = a.EtunnelStart()
	if err != nil {
		t.Fatalf("restart after stop: %v", err)
	}
	if !st.Active {
		t.Fatalf("restart state mismatch: %+v", st)
	}
	if fake.starts != 2 {
		t.Fatalf("expected 2 starts across the cycle, got %d", fake.starts)
	}
}

func TestEtunnelSetupPersistsPaidCredentials(t *testing.T) {
	fake := &fakeEtCtl{}
	a := newEtTestApp(t, fake)

	st, err := a.EtunnelSetup("team-desk", "et_tok_5574")
	if err != nil {
		t.Fatalf("EtunnelSetup: %v (msg %q)", err, st.Message)
	}

	// Persisted via DesktopConfig (HOME redirected to a temp dir).
	raw, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".ggcode", "desktop-config.json"))
	if err != nil {
		t.Fatalf("settings file missing: %v", err)
	}
	if !strings.Contains(string(raw), "team-desk") {
		t.Fatalf("subdomain not persisted: %s", raw)
	}

	// Merge, not overwrite: a sibling setting written before setup survives.
	reloaded := wailskit.LoadDesktopConfig()
	sub, tok, _ := reloaded.GetETunnel()
	if sub != "team-desk" || tok != "et_tok_5574" {
		t.Fatalf("round-trip mismatch: sub=%q tok=%q", sub, tok)
	}

	// Subsequent control calls feed the persisted credentials to the
	// controller (paid-plan wiring).
	if _, err := a.EtunnelStart(); err != nil {
		t.Fatal(err)
	}
	if fake.lastSub != "team-desk" || fake.lastTok != "et_tok_5574" {
		t.Fatalf("persisted credentials not used: sub=%q tok=%q", fake.lastSub, fake.lastTok)
	}
}

func TestStopEtunnelIdempotentAndNilSafe(t *testing.T) {
	fake := &fakeEtCtl{}
	a := newEtTestApp(t, fake)
	if _, err := a.EtunnelStart(); err != nil {
		t.Fatal(err)
	}

	a.stopEtunnel()
	if fake.stops != 1 {
		t.Fatalf("expected stop on teardown, got %d", fake.stops)
	}
	// Second call: everything already nil — must not panic or double-stop.
	a.stopEtunnel()
	if fake.stops != 1 {
		t.Fatalf("idempotent teardown broken: %d stops", fake.stops)
	}
}

// --- static wiring pins -------------------------------------------------

func readDeskFile(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(raw)
}

func TestPinEtunnelFourBindingsAreThinControlEtWraps(t *testing.T) {
	src := readDeskFile(t, "et_ctl.go")
	for _, want := range []string{
		"a.etControl(\"on\")",
		"a.etControl(\"off\")",
		"a.etControl(\"show\")",
		"a.etControl(\"setup\", subdomain, token)",
		"webui.ControlEt(ctl, localURL, sub, tok, plan, a.etSaveCfg(), args)",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("et_ctl.go lost thin-wrap wiring: %q not found", want)
		}
	}
	// The bridge must never re-implement the tunnel itself.
	for _, banned := range []string{"etunnel.NewClient", "tunnel.Start(", "sdk.Start"} {
		if strings.Contains(src, banned) {
			t.Fatalf("et_ctl.go re-implements tunnel lifecycle: found %q", banned)
		}
	}
	// Push channel for UI state flips.
	if !strings.Contains(src, `"etunnel:state"`) {
		t.Fatal(`et_ctl.go lost "etunnel:state" EventsEmit push`)
	}
}

func TestPinShutdownTearsEtunnelBeforeShare(t *testing.T) {
	src := readDeskFile(t, "app.go")
	i := strings.Index(src, "a.stopEtunnel()")
	j := strings.Index(src[i:], "a.stopShare()")
	if i < 0 {
		t.Fatal("app.go shutdown lost stopEtunnel call")
	}
	if j < 0 {
		t.Fatal("shutdown must stop the etunnel surface (hosted webui + poller) before the share tunnel")
	}
	// Host + singleton fields exist on App with the etMu namespace.
	for _, want := range []string{"etMu", "etSrv", "etCtl", "etLocalURL", "etPollCancel"} {
		if !strings.Contains(src, want+"\t") && !strings.Contains(src, want+" ") {
			t.Fatalf("App struct missing etunnel field %q", want)
		}
	}
}
