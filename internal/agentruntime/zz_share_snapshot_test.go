package agentruntime

import (
	"testing"

	"github.com/topcheer/ggcode/internal/tunnel"
)

// 回归:daemon 模式此前未注入 SetTunnelStatusFn,/api/tunnel/status 恒全 false,
// webui 共享弹窗卡"等待二维码"。ShareStatusSnapshot 是 daemon 侧 share 面数据源。
func TestShareStatusSnapshotNoShare(t *testing.T) {
	h := &TunnelHost{}
	out := h.ShareStatusSnapshot()
	if out["active"] != false || out["starting"] != false || out["connected"] != false {
		t.Fatalf("expected all-false defaults, got %v", out)
	}
	if _, ok := out["qrcode_png_b64"]; ok {
		t.Fatalf("no active share: qrcode_png_b64 must be absent")
	}
}

func TestShareStatusSnapshotActiveSessionNoInfo(t *testing.T) {
	// 未 Start 的 session:Info()==nil → active=true 但无 url/qr/connected。
	h := &TunnelHost{}
	h.mu.Lock()
	h.activeShare = &tunnelSessionRef{session: tunnel.NewSession("wss://invalid.example", tunnel.WithClientMetadata("t", "v"))}
	h.mu.Unlock()
	out := h.ShareStatusSnapshot()
	if out["active"] != true {
		t.Fatalf("expected active=true, got %v", out)
	}
	if _, ok := out["connect_url"]; ok {
		t.Fatalf("info==nil: connect_url must be absent")
	}
	if out["connected"] == true {
		t.Fatalf("info==nil: connected must stay false, got %v", out)
	}
}

// 回归:TUI 关闭分享(closeTunnelGracefully → detachTunnelLifecycle)只清
// Model 侧 tunnelSession 并 DetachOnlineBroker,从不清理 TunnelHost.activeShare,
// 于是 ShareActive() 恒为 true,start_share 整个会话都被拒绝"already active"。
// ReleaseShare 由 detachTunnelLifecycle 调用:标记分享结束(不停 session/broker,
// 它们由调用方优雅关闭),并释放 P2P upgrade manager。
func TestReleaseShareClearsActiveRefWithoutStoppingSession(t *testing.T) {
	h := NewTunnelHost()
	sess := tunnel.NewSession("wss://invalid.example", tunnel.WithClientMetadata("t", "v"))
	broker := tunnel.NewBroker(sess)
	h.mu.Lock()
	h.activeShare = &tunnelSessionRef{session: sess, broker: broker}
	h.onlineBroker = broker
	h.mu.Unlock()
	h.AttachOnlineBroker(broker)
	if h.ShareStatusSnapshot()["active"] != true {
		t.Fatal("precondition: share must be active")
	}

	h.ReleaseShare()

	h.mu.Lock()
	activeRef, mgr, online := h.activeShare, h.upgradeMgr, h.onlineBroker
	h.mu.Unlock()
	if activeRef != nil || mgr != nil || online != nil {
		t.Fatalf("ReleaseShare must clear activeShare/upgradeMgr/onlineBroker, got ref=%v mgr=%v online=%v", activeRef != nil, mgr != nil, online != nil)
	}
	if out := h.ShareStatusSnapshot(); out["active"] != false {
		t.Fatalf("after ReleaseShare the share must be inactive, got %v", out)
	}
	// The session was NOT touched by ReleaseShare (the caller drains it).
	if sess.Info() != nil {
		t.Fatal("ReleaseShare must not mutate the session")
	}
	// Idempotent.
	h.ReleaseShare()
	if h.ShareStatusSnapshot()["active"] != false {
		t.Fatal("second ReleaseShare must be a no-op")
	}
}
