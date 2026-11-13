package webui

import (
	"strings"
	"testing"
)

type fakeEtCtl struct {
	url     string
	stopped bool
	// #5395: record what StartTunnelOpts actually received so the pin
	// tests can also catch a pass-through regression.
	gotSub, gotTok string
}

func (f *fakeEtCtl) StartTunnelOpts(localAddr, subdomain, token string) (string, bool, error) {
	f.gotSub, f.gotTok = subdomain, token
	f.url = "https://demo.example.com"
	return f.url, true, nil
}
func (f *fakeEtCtl) StopTunnel()       { f.stopped = true; f.url = "" }
func (f *fakeEtCtl) TunnelURL() string { return f.url }

// TestControlEtOnPlanDistinction pins #5395: the /et on success message
// must state the plan that actually started. A paid config (subdomain/token
// non-empty) runs the paid custom-domain path in StartTunnelOpts and must
// NOT claim the free-plan limits; a free config keeps the free note.
func TestControlEtOnPlanDistinction(t *testing.T) {
	// paid: /et setup saved a subdomain + token, then /et on
	f := &fakeEtCtl{}
	out, err := ControlEt(f, "http://127.0.0.1:1#token=x", "mydom", "sekret123", nil, []string{"on"})
	if err != nil {
		t.Fatalf("paid on: %v", err)
	}
	if !strings.Contains(out, "plan: paid (subdomain=mydom token=****t123)") {
		t.Fatalf("paid on must carry the paid plan note: %q", out)
	}
	if strings.Contains(out, "free plan") {
		t.Fatalf("paid on must NOT claim free-plan limits: %q", out)
	}
	if f.gotSub != "mydom" || f.gotTok != "sekret123" {
		t.Fatalf("paid on must pass the paid config through, got sub=%q tok=%q", f.gotSub, f.gotTok)
	}
	// free: no config, /et on keeps the free note
	g := &fakeEtCtl{}
	out, err = ControlEt(g, "http://127.0.0.1:1#token=x", "", "", nil, []string{"on"})
	if err != nil || !strings.Contains(out, "free plan: 4h lifetime, 1 Mbps") {
		t.Fatalf("free on must keep the free note: %q %v", out, err)
	}
	if g.gotSub != "" || g.gotTok != "" {
		t.Fatalf("free on must not pass paid config, got sub=%q tok=%q", g.gotSub, g.gotTok)
	}
}

// TestControlEtLoginGuidance pins the IM/daemon /et login surface: the
// browser loopback flow cannot run headless (the SDK's fallback URL goes to
// stderr, undeliverable over IM), so login must guide back to the TUI and
// must NOT start any flow here. The usage line must mention login.
func TestControlEtLoginGuidance(t *testing.T) {
	f := &fakeEtCtl{}
	out, err := ControlEt(f, "http://127.0.0.1:1#token=x", "", "", nil, []string{"login"})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.Contains(out, "TUI") {
		t.Fatalf("login must guide to the interactive TUI: %q", out)
	}
	if f.url != "" || f.stopped {
		t.Fatalf("login must not touch the tunnel controller")
	}
	// usage line (unknown sub) mentions login
	out, _ = ControlEt(f, "", "", "", nil, []string{"bogus"})
	if !strings.Contains(out, "login") {
		t.Fatalf("usage line must mention login: %q", out)
	}
}

func TestControlEtShowOffSetup(t *testing.T) {
	f := &fakeEtCtl{}
	// show, inactive
	out, err := ControlEt(f, "http://127.0.0.1:1#token=x", "", "", nil, nil)
	if err != nil || !strings.Contains(out, "not active") {
		t.Fatalf("show inactive: %q %v", out, err)
	}
	// on
	out, err = ControlEt(f, "http://127.0.0.1:1#token=x", "", "", nil, []string{"on"})
	if err != nil || !strings.Contains(out, "demo.example.com") || !strings.Contains(out, "#token=x") {
		t.Fatalf("on: %q %v", out, err)
	}
	// off
	out, err = ControlEt(f, "", "", "", nil, []string{"off"})
	if err != nil || !strings.Contains(out, "closed") || !f.stopped {
		t.Fatalf("off: %q %v", out, err)
	}
	// setup persists via saveCfg
	var gotSub, gotTok string
	out, err = ControlEt(f, "", "", "", func(sub, tok string) error { gotSub, gotTok = sub, tok; return nil }, []string{"setup", "mydom", "sekret123"})
	if err != nil || gotSub != "mydom" || gotTok != "sekret123" || !strings.Contains(out, "****t123") {
		t.Fatalf("setup: %q %v %s/%s", out, err, gotSub, gotTok)
	}
	// nil ctl degrades gracefully
	if out, _ := ControlEt(nil, "", "", "", nil, []string{"show"}); !strings.Contains(out, "not available") {
		t.Fatalf("nil ctl: %q", out)
	}
	// usage for unknown sub
	if out, _ := ControlEt(f, "", "", "", nil, []string{"bogus"}); !strings.Contains(out, "usage") {
		t.Fatalf("usage: %q", out)
	}
}
