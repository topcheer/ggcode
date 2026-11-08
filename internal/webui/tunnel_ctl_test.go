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

	// 确定性错误路径:server 指向本机端口 1(无监听必拒连),StartTunnel 必返错,
	// 断言失败后状态复位可重试——不依赖外部网络状态,无 skip 分支
	t.Setenv("ETUNNEL_SERVER", "wss://127.0.0.1:1/tunnel")
	if _, started, err := c.StartTunnel("127.0.0.1:1"); err == nil {
		t.Fatalf("dial to port 1 unexpectedly succeeded (started=%v)", started)
	}
	if c.TunnelURL() != "" {
		t.Fatal("failed start must not leave a URL behind")
	}
	c.StopTunnel() // failed-start state must be safe to stop again
}

// 钉测(/webui tunnel off 词位):入口分流 parts=[/webui,tunnel,off] 时
// off 在 parts[2]——历史 bug 曾错检 parts[1] 导致 off 永不命中。
// 该行为在 TUI 层,此处锁定 TunnelURL 清空语义(off 依赖的前置读)。
func TestTunnelCtlStopClearsURL(t *testing.T) {
	c := &TunnelCtl{}
	c.mu.Lock()
	c.tunnel = nil // Stop 走 cancel/url 清空路径
	c.url = "https://x.t.ggcode.uk"
	c.mu.Unlock()
	if c.TunnelURL() == "" {
		t.Fatal("setup: url should be present")
	}
	c.StopTunnel()
	if c.TunnelURL() != "" {
		t.Fatalf("stop must clear url, got %q", c.TunnelURL())
	}
}
