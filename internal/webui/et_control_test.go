package webui

import (
	"strings"
	"testing"
)

type fakeEtCtl struct {
	url     string
	stopped bool
}

func (f *fakeEtCtl) StartTunnelOpts(localAddr, subdomain, token string) (string, bool, error) {
	f.url = "https://demo.example.com"
	return f.url, true, nil
}
func (f *fakeEtCtl) StopTunnel()       { f.stopped = true; f.url = "" }
func (f *fakeEtCtl) TunnelURL() string { return f.url }

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

// Pin for issue #5395: the "on" branch must reply with the plan-aware
// suffix (same plan() helper the "show" branch uses), not a hardcoded
// free-plan string - a paid (custom-subdomain) tunnel must not be
// announced as "free plan".
func TestControlEtOnPlanAware(t *testing.T) {
	f := &fakeEtCtl{}
	out, err := ControlEt(f, "http://127.0.0.1:1", "mysub", "token123", nil, []string{"on"})
	if err != nil {
		t.Fatalf("on paid: %v", err)
	}
	if strings.Contains(out, "free plan") {
		t.Fatalf("on paid config must not say free plan (issue #5395): %q", out)
	}
	if !strings.Contains(out, "paid (subdomain=mysub") || !strings.Contains(out, "****n123") {
		t.Fatalf("on paid config must carry plan() suffix, got %q", out)
	}
	// free config keeps the plan: free line
	f2 := &fakeEtCtl{}
	out, err = ControlEt(f2, "http://127.0.0.1:1", "", "", nil, []string{"on"})
	if err != nil || !strings.Contains(out, "plan: free") {
		t.Fatalf("on free: %q %v", out, err)
	}
}
