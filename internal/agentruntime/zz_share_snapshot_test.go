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
