package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	qrcode "github.com/skip2/go-qrcode"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/session"
)

func TestRenderQQShareQRCodeMatchesTerminalSmallStyle(t *testing.T) {
	link := "https://qun.qq.com/qunpro/robot/qunshare?robot_appid=1&sceneData=test"
	code, err := qrcode.New(link, qrcode.Low)
	if err != nil {
		t.Fatalf("qrcode.New returned error: %v", err)
	}

	rendered, err := renderQQShareQRCode(link)
	if err != nil {
		t.Fatalf("renderQQShareQRCode returned error: %v", err)
	}
	lines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
	if len(lines) == 0 {
		t.Fatal("expected rendered QR lines")
	}
	if !strings.Contains(rendered, "▀") || !strings.Contains(rendered, "▄") || !strings.Contains(rendered, "█") {
		t.Fatalf("expected half-block terminal glyphs, got %q", rendered)
	}
	if len(lines) == 0 || len(lines[0]) == 0 || !strings.Contains(rendered, " ") {
		t.Fatalf("expected non-empty QR output, got %q", rendered)
	}
	if len(lines) >= len(code.Bitmap()) {
		t.Fatalf("expected height-compressed QR output, got %d lines for %d bitmap rows", len(lines), len(code.Bitmap()))
	}
	if !strings.HasPrefix(lines[0], "▄▄▄") {
		t.Fatalf("expected top border to use lower-half blocks, got %q", lines[0])
	}
	if !strings.HasPrefix(lines[len(lines)-1], "▀▀▀") {
		t.Fatalf("expected bottom border to use upper-half blocks, got %q", lines[len(lines)-1])
	}
}

func TestQQPanelEscClosesPanel(t *testing.T) {
	m := NewModel(nil, nil)
	m.openQQPanel()
	m.qqPanel.shareLink = "https://example.com/qq-share"
	m.qqPanel.shareQRCode = "qr"

	updated, cmd := m.handleQQPanelKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("expected esc panel close without command")
	}
	m = updated
	if m.qqPanel != nil {
		t.Fatal("expected esc to close the qq panel")
	}
}

func TestQQPanelViewShowsInlineShareQRCode(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.openQQPanel()
	m.qqPanel.shareAdapter = "ggcodetest"
	m.qqPanel.shareLink = "https://example.com/qq-share"
	m.qqPanel.shareQRCode = "QR\nQR"

	view := m.View().Content
	if !strings.Contains(view, "QR") {
		t.Fatalf("expected inline QR code in qq panel, got:\n%s", view)
	}
	if !strings.Contains(view, "Bind Channel") || !strings.Contains(view, "Share Link:") {
		t.Fatalf("expected bind channel section in qq panel, got:\n%s", view)
	}
	if !strings.Contains(view, "https://example.com/qq-share") {
		t.Fatalf("expected share link in qq panel, got:\n%s", view)
	}
}

func TestQQPanelRenderLocalizesToChinese(t *testing.T) {
	m := NewModel(nil, nil)
	m.SetConfig(&config.Config{
		Language: "zh-CN",
		IM: config.IMConfig{
			Adapters: map[string]config.IMAdapterConfig{
				"qq-a": {Enabled: true, Platform: "qq"},
			},
		},
	})
	m.openQQPanel()

	rendered := m.renderQQPanel()
	if !strings.Contains(rendered, "目录") || !strings.Contains(rendered, "QQ 机器人") || !strings.Contains(rendered, "当前绑定") {
		t.Fatalf("expected localized qq panel, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "j/k 移动") || !strings.Contains(rendered, "绑定渠道") {
		t.Fatalf("expected localized qq panel actions, got:\n%s", rendered)
	}
}

func TestQQPanelCreateAdapterPersistsCredentials(t *testing.T) {
	m := NewModel(nil, nil)
	cfg := config.DefaultConfig()
	path := t.TempDir() + "/ggcode.yaml"
	cfg.FilePath = path
	m.SetConfig(cfg)
	if err := m.config.Save(); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	m.openQQPanel()
	updated, _ := m.handleQQPanelKey(tea.KeyPressMsg{Text: "i"})
	m = updated
	m.qqPanel.createInput = "qq-main 123456 secret-abc"
	_, cmd := m.handleQQPanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected create adapter command")
	}
	msg := cmd()
	result, ok := msg.(qqBindResultMsg)
	if !ok {
		t.Fatalf("expected qqBindResultMsg, got %#v", msg)
	}
	if result.err != nil {
		t.Fatal(result.err)
	}
	adapter, ok := m.config.IM.Adapters["qq-main"]
	if !ok {
		t.Fatalf("expected qq-main adapter, got %#v", m.config.IM.Adapters)
	}
	if adapter.Extra["appid"] != "123456" || adapter.Extra["appsecret"] != "secret-abc" {
		t.Fatalf("unexpected adapter credentials: %#v", adapter.Extra)
	}
	if !m.config.IM.Enabled {
		t.Fatal("expected QQ bot creation to enable IM config")
	}
}

func TestQQPanelBindUsesCurrentDirectoryTargetByDefault(t *testing.T) {
	m := NewModel(nil, nil)
	cfg := config.DefaultConfig()
	cfg.IM = config.IMConfig{
		Enabled: true,
		Adapters: map[string]config.IMAdapterConfig{
			"qq-b": {Enabled: true, Platform: "qq"},
		},
	}
	m.SetConfig(cfg)
	m.session = &session.Session{Workspace: filepath.Join(t.TempDir(), "workspace-alpha")}
	imMgr := im.NewManager()
	store := im.NewMemoryBindingStore()
	if err := imMgr.SetBindingStore(store); err != nil {
		t.Fatalf("SetBindingStore returned error: %v", err)
	}
	imMgr.BindSession(im.SessionBinding{SessionID: "session-1", Workspace: m.currentWorkspacePath()})
	imMgr.PublishAdapterState(im.AdapterState{Name: "qq-b", Platform: im.PlatformQQ, Healthy: true, Status: "connected"})
	m.SetIMManager(imMgr)

	msg := m.bindQQEntry(qqBindingEntry{Adapter: "qq-b"})()
	result, ok := msg.(qqBindResultMsg)
	if !ok {
		t.Fatalf("expected qqBindResultMsg, got %#v", msg)
	}
	if result.err != nil {
		t.Fatal(result.err)
	}
	binding := imMgr.CurrentBinding()
	if binding == nil {
		t.Fatal("expected current binding")
	}
	if got := binding.TargetID; got != filepath.Base(m.currentWorkspacePath()) {
		t.Fatalf("unexpected bound target id: %q", got)
	}
	if binding.ChannelID != "" {
		t.Fatalf("expected channel to stay empty before pairing, got %q", binding.ChannelID)
	}
}

func TestQQPanelRenderCreateHint(t *testing.T) {
	m := NewModel(nil, nil)
	m.SetConfig(&config.Config{
		IM: config.IMConfig{
			Adapters: map[string]config.IMAdapterConfig{
				"qq-a": {Enabled: true, Platform: "qq"},
			},
		},
	})
	m.openQQPanel()
	rendered := m.renderQQPanel()
	if !strings.Contains(rendered, "c bind channel") || !strings.Contains(rendered, "x unbind channel") || !strings.Contains(rendered, "bind bot") {
		t.Fatalf("expected create hint in /qq panel, got:\n%s", rendered)
	}
	if strings.Contains(rendered, "full-screen QR") || strings.Contains(rendered, "Press v") {
		t.Fatalf("expected no fullscreen QR hint in /qq panel, got:\n%s", rendered)
	}
}

func TestQQPanelRenderShowsBotCountsAndBindingStatus(t *testing.T) {
	m := NewModel(nil, nil)
	m.SetConfig(&config.Config{
		IM: config.IMConfig{
			Adapters: map[string]config.IMAdapterConfig{
				"qq-a": {Enabled: true, Platform: "qq"},
				"qq-b": {Enabled: true, Platform: "qq"},
			},
		},
	})
	m.openQQPanel()
	rendered := m.renderQQPanel()
	if !strings.Contains(rendered, "Created: 2") || !strings.Contains(rendered, "Available: 2") {
		t.Fatalf("expected bot counts in /qq panel, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Bound Directory: (none)") {
		t.Fatalf("expected binding status in /qq panel, got:\n%s", rendered)
	}
}

func TestQQPanelBindAutoStartsRuntime(t *testing.T) {
	m := NewModel(nil, nil)
	cfg := config.DefaultConfig()
	adapterName := "qq-bind-auto-" + filepath.Base(t.TempDir())
	cfg.FilePath = filepath.Join(t.TempDir(), "ggcode.yaml")
	cfg.IM = config.IMConfig{
		Enabled: false,
		Adapters: map[string]config.IMAdapterConfig{
			adapterName: {
				Enabled:  true,
				Platform: "qq",
				Extra: map[string]any{
					"appid":     "123456",
					"appsecret": "secret-abc",
				},
			},
		},
	}
	m.SetConfig(cfg)
	t.Setenv("HOME", t.TempDir())
	m.session = &session.Session{Workspace: filepath.Join(t.TempDir(), "workspace-alpha")}
	if err := m.config.Save(); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	m.openQQPanel()
	_, cmd := m.handleQQPanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected bind command")
	}
	msg := cmd()
	result, ok := msg.(qqBindResultMsg)
	if !ok {
		t.Fatalf("expected qqBindResultMsg, got %#v", msg)
	}
	if result.err != nil {
		t.Fatal(result.err)
	}
	if m.imManager == nil {
		t.Fatal("expected bind to auto-start IM runtime")
	}
	if !m.config.IM.Enabled {
		t.Fatal("expected bind to enable IM config")
	}
}

func TestQQPanelClearChannelKeepsBotBinding(t *testing.T) {
	m := NewModel(nil, nil)
	m.SetConfig(config.DefaultConfig())
	m.session = &session.Session{Workspace: "/tmp/project"}
	imMgr := im.NewManager()
	store := im.NewMemoryBindingStore()
	if err := imMgr.SetBindingStore(store); err != nil {
		t.Fatalf("SetBindingStore returned error: %v", err)
	}
	imMgr.BindSession(im.SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := imMgr.BindChannel(im.ChannelBinding{
		Platform:  im.PlatformQQ,
		Adapter:   "hermes",
		TargetID:  "ops",
		ChannelID: "group-1",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}
	m.SetIMManager(imMgr)
	m.openQQPanel()

	_, cmd := m.handleQQPanelKey(tea.KeyPressMsg{Text: "x"})
	if cmd == nil {
		t.Fatal("expected clear channel command")
	}
	msg := cmd()
	result, ok := msg.(qqBindResultMsg)
	if !ok {
		t.Fatalf("expected qqBindResultMsg, got %#v", msg)
	}
	if result.err != nil {
		t.Fatal(result.err)
	}

	binding := imMgr.CurrentBinding()
	if binding == nil || binding.Adapter != "hermes" || binding.ChannelID != "" {
		t.Fatalf("expected binding to remain and channel cleared, got %#v", binding)
	}
}

func TestStartQQAdapterIfNeededStartsConfiguredAdapterInRuntime(t *testing.T) {
	m := NewModel(nil, nil)
	m.SetConfig(&config.Config{
		IM: config.IMConfig{
			Enabled: true,
			Adapters: map[string]config.IMAdapterConfig{
				"ggcodetest": {Enabled: true, Platform: "qq"},
			},
		},
	})
	imMgr := im.NewManager()
	m.SetIMManager(imMgr)

	if err := m.startQQAdapterIfNeeded("ggcodetest"); err != nil {
		t.Fatalf("startQQAdapterIfNeeded returned error: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := imMgr.Snapshot()
		for _, state := range snapshot.Adapters {
			if state.Name == "ggcodetest" {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected QQ adapter state to be published after dynamic start")
}

func TestQQPanelCtrlCClosesPanel(t *testing.T) {
	m := NewModel(nil, nil)
	m.openQQPanel()

	next, cmd := m.Update(tea.KeyPressMsg{Text: "ctrl+c"})
	if cmd != nil {
		t.Fatal("expected ctrl-c QQ panel close to be synchronous")
	}
	m2 := next.(Model)
	if m2.qqPanel != nil {
		t.Fatal("expected QQ panel to close on ctrl-c")
	}
}
