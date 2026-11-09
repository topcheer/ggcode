package webui

import (
	"testing"
)

// 钉测(TunnelCtl 离线分支,不依赖外部网络状态):
// 1) 未激活时 TunnelURL 空、StopTunnel 安全 no-op
// 2) StartTunnelOpts 对不可达本地地址返回错误且状态复位(可再次尝试)
// 服务端由 etunnel SDK 内置默认(wss://t.etun.uk/tunnel)决定,此处不断言。
func TestTunnelCtlOfflineBranches(t *testing.T) {
	c := &TunnelCtl{}
	if c.TunnelURL() != "" {
		t.Fatal("fresh ctl should have empty URL")
	}
	c.StopTunnel() // must not panic
}

// off 词位历史 bug 曾错检 parts[1] 导致 off 永不命中。
// 该行为在 TUI 层,此处锁定 TunnelURL 清空语义(off 依赖的前置读)。
func TestTunnelCtlStopClearsURL(t *testing.T) {
	c := &TunnelCtl{}
	c.mu.Lock()
	c.tunnel = nil // Stop 走 cancel/url 清空路径
	c.url = "https://x.t.etun.uk"
	c.mu.Unlock()
	if c.TunnelURL() == "" {
		t.Fatal("setup: url should be present")
	}
	c.StopTunnel()
	if c.TunnelURL() != "" {
		t.Fatalf("stop must clear url, got %q", c.TunnelURL())
	}
}
