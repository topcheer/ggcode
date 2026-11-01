package tui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
)

// #4869 follow-up (round 5, "create = verify"): the create flow must verify
// credentials against the QQ token endpoint BEFORE persisting, 'o' from the
// /im cockpit must land directly in create mode, and the bots.qq.com
// deep-link renders beside the input.

func TestIssue4869R5_VerifyGateTwoBranches(t *testing.T) {
	// Branch ok: HTTP 200 -> nil error.
	srvOK := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"tok"}`))
	}))
	defer srvOK.Close()
	oldURL, oldClient := qqCreateVerifyURL, qqCreateVerifyClient
	qqCreateVerifyURL, qqCreateVerifyClient = srvOK.URL, srvOK.Client()
	defer func() { qqCreateVerifyURL, qqCreateVerifyClient = oldURL, oldClient }()

	if err := validateQQCreateSpec("bot1", "appid", "secret"); err != nil {
		t.Fatalf("valid credentials must pass the gate: %v", err)
	}
	if err := validateQQCreateSpec("", "appid", "secret"); err == nil {
		t.Fatal("incomplete spec must fail the gate")
	}

	// Branch rejected: HTTP 401 -> error naming the rejection.
	srvBad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srvBad.Close()
	qqCreateVerifyURL = srvBad.URL
	err := validateQQCreateSpec("bot1", "badid", "badsecret")
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("invalid credentials must fail with a rejection error, got: %v", err)
	}
}

func TestIssue4869R5_CreateLinkAndModeRouting(t *testing.T) {
	if l := qqCreateLink("open bots.qq.com"); !strings.Contains(l, "\x1b]8;;https://bots.qq.com") || !strings.Contains(l, "open bots.qq.com") {
		t.Fatalf("OSC8 deep-link malformed: %q", l)
	}
	// 'o' from the cockpit lands in CREATE MODE on the vendor panel.
	m := NewModel(nil, nil)
	m.SetConfig(&config.Config{Language: "en"})
	m.openIMPanel()
	m.imPanel.selected = 0
	entries := m.imChannelEntries()
	_ = entries // empty list -> 'o' no-ops safely; assert via configured path below
	updated, _ := m.handleIMPanelKey(tea.KeyPressMsg{Text: "o"})
	if updated.imPanel == nil {
		t.Fatal("empty-list 'o' must keep the cockpit open (no crash)")
	}

	// Configured qq channel (runtime manager bound): 'o' opens /qq IN
	// create mode (im_panel_test.go BindChannel fixture precedent).
	cfg := &config.Config{Language: "en", IM: config.IMConfig{
		Adapters: map[string]config.IMAdapterConfig{
			"qq-a": {Enabled: true, Platform: "qq"},
		},
	}}
	mgr := im.NewManager()
	mgr.BindSession(im.SessionBinding{SessionID: "s", Workspace: "/w"})
	_, _ = mgr.BindChannel(im.ChannelBinding{Workspace: "/w", Platform: im.PlatformQQ, Adapter: "qq-a", ChannelID: "c1"})
	m2 := Model{config: cfg, imManager: mgr}
	m2.openIMPanel()
	updated2, _ := m2.handleIMPanelKey(tea.KeyPressMsg{Text: "o"})
	if updated2.imPanel != nil {
		t.Fatal("'o' must close the cockpit after routing")
	}
	if updated2.qqPanel == nil || !updated2.qqPanel.createMode {
		t.Fatal("'o' must open the qq panel directly in create mode")
	}
}
