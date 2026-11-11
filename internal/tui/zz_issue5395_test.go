package tui

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/webui"
)

// TestIssue5395EtTunnelReadyMsgPlanNote pins the TUI half of #5395: the
// final et-tunnel success message must state the plan that actually
// started. A paid etTunnelReadyMsg (plan non-empty, set by etTunnelOn
// whenever subdomain/token are configured) must carry the paid plan note
// and must NOT claim the free-plan limits; a free ready msg (plan=="",
// StartTunnelOpts ran the anonymous free path) keeps the free note.
func TestIssue5395EtTunnelReadyMsgPlanNote(t *testing.T) {
	// paid: etTunnelOn classified the config as paid and carried the plan
	m := newTestModel()
	m2, _ := m.handleEtTunnelReadyMsg(etTunnelReadyMsg{
		url:     "https://mydom.example.com",
		suffix:  "#token=x",
		started: true,
		plan:    "paid(子域名=mydom token=****t123)",
	})
	mm, ok := m2.(Model)
	if !ok {
		t.Fatalf("ready msg returned %T, want Model", m2)
	}
	if mm.chatList == nil || mm.chatList.Len() == 0 {
		t.Fatal("ready msg must write a chat item")
	}
	paid := systemTextAt(t, &mm, mm.chatList.Len()-1)
	if !strings.Contains(paid, "paid(子域名=mydom token=****t123)") {
		t.Fatalf("paid ready must carry the paid plan note: %q", paid)
	}
	if strings.Contains(paid, "free plan") {
		t.Fatalf("paid ready must NOT claim free-plan limits: %q", paid)
	}
	if !strings.Contains(paid, "https://mydom.example.com#token=x") {
		t.Fatalf("paid ready must keep the tunnel URL + suffix: %q", paid)
	}

	// free: no config, StartTunnelOpts ran the free anonymous path
	f := newTestModel()
	f2, _ := f.handleEtTunnelReadyMsg(etTunnelReadyMsg{
		url:     "https://demo.example.com",
		suffix:  "#token=x",
		started: true,
		plan:    "",
	})
	ff, ok := f2.(Model)
	if !ok {
		t.Fatalf("free ready returned %T, want Model", f2)
	}
	free := systemTextAt(t, &ff, ff.chatList.Len()-1)
	if !strings.Contains(free, "(free plan: 4h lifetime, 1 Mbps)") {
		t.Fatalf("free ready must keep the free note: %q", free)
	}
	if strings.Contains(free, "paid(子域名=") {
		t.Fatalf("free ready must not claim a paid plan: %q", free)
	}
}

// TestIssue5395EtTunnelOnClassifiesPlan pins the etTunnelOn half: the
// pre-start hint and the plan carried into etTunnelReadyMsg must follow
// the pass-through semantics of StartTunnelOpts (subdomain/token ANY
// non-empty = paid path), not a stricter AND.
func TestIssue5395EtTunnelOnClassifiesPlan(t *testing.T) {
	// token-only config (subdomain empty): StartTunnelOpts still receives
	// a non-empty token = paid path; etTunnelOn must classify as paid.
	m := newTestModel()
	m.config = config.DefaultConfig()
	m.config.ETunnel.Token = "sekret123"
	m.webuiURL = "http://127.0.0.1:1#token=x"
	m.webuiTunnel = &webui.TunnelCtl{} // never started: only TunnelURL()=="" consulted pre-start

	cmd := m.etTunnelOn()
	if cmd == nil {
		t.Fatal("etTunnelOn must return a command that starts the tunnel")
	}
	last := systemTextAt(t, &m, m.chatList.Len()-1)
	if !strings.Contains(last, "custom subdomain") {
		t.Fatalf("token-only config must get the paid pre-start hint: %q", last)
	}
}

// systemTextAt returns the text of the chat item at idx as a system item.
func systemTextAt(t *testing.T, m *Model, idx int) string {
	t.Helper()
	item := m.chatList.ItemAt(idx)
	s, ok := item.(*chat.SystemItem)
	if !ok {
		t.Fatalf("chat item %d is %T, want *chat.SystemItem", idx, item)
	}
	return s.Text()
}
