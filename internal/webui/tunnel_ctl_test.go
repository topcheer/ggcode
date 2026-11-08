package webui

import (
	"strings"
	"testing"
)

// 钉测(TunnelCtl 离线分支,不联真实隧道服务):
// 1) TunnelServer 默认值 + ETUNNEL_SERVER 覆盖
// 2) 未激活时 TunnelURL 空、StopTunnel 安全 no-op
// 3) StartTunnel 对不可达 server 返回错误且状态复位(可再次尝试)
func TestTunnelCtlOfflineBranches(t *testing.T) {
	if got := TunnelServer(); got != "wss://t.ggcode.uk/tunnel" {
		t.Fatalf("default server = %q", got)
	}
	t.Setenv("ETUNNEL_SERVER", "wss://custom.example.com/tunnel")
	if got := TunnelServer(); !strings.Contains(got, "custom.example.com") {
		t.Fatalf("env override ignored: %q", got)
	}

	c := &TunnelCtl{}
	if c.TunnelURL() != "" {
		t.Fatal("fresh ctl should have empty URL")
	}
	c.StopTunnel() // must not panic

	if _, _, err := c.StartTunnel("127.0.0.1:1"); err == nil {
		t.Skip("tunnel server unexpectedly reachable; skipping offline assertion")
	}
	if c.TunnelURL() != "" {
		t.Fatal("failed start must not leave a URL behind")
	}
	c.StopTunnel() // failed-start state must be safe to stop again
}
