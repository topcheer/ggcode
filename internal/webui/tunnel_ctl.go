package webui

import (
	"context"
	"os"
	"sync"

	etunnel "etunnel/sdk"

	"github.com/topcheer/ggcode/internal/debug"
)

// TunnelCtl /webui tunnel 的隧道控制器:把 WebUI 本地地址经 etunnel
// (wss://t.ggcode.uk/tunnel,免费匿名计划)映射为公网 https 域名。
// 单例生命周期:激活→打印 URL→显式 stop 或进程退出关闭。
type TunnelCtl struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	tunnel *etunnel.Tunnel
	url    string
}

// TunnelServer 默认隧道服务端(live deployment);ETUNNEL_SERVER 环境变量可覆盖
func TunnelServer() string {
	if s := os.Getenv("ETUNNEL_SERVER"); s != "" {
		return s
	}
	return "wss://t.ggcode.uk/tunnel"
}

// StartTunnel 激活隧道(幂等:已激活直接返回现 URL)。
// localAddr 是 WebUI 实际监听地址(127.0.0.1:port);返回公网 https URL。
func (c *TunnelCtl) StartTunnel(localAddr string) (string, bool, error) {
	c.mu.Lock()
	if c.tunnel != nil {
		u := c.url
		c.mu.Unlock()
		return u, false, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.mu.Unlock()

	t, err := etunnel.Connect(ctx, etunnel.Options{
		Server:    TunnelServer(),
		LocalAddr: localAddr,
		Client:    "ggcode-webui/1.0",
		OnLog:     func(format string, args ...any) { debug.Log("webui", "tunnel: "+format, args...) },
	})
	if err != nil {
		c.mu.Lock()
		c.cancel = nil
		c.mu.Unlock()
		cancel()
		return "", false, err
	}
	c.mu.Lock()
	c.tunnel = t
	c.url = t.URL()
	c.mu.Unlock()

	// 监视关闭(远端过期/断连)后清空状态,允许重新激活
	go func() {
		<-t.Done()
		debug.Log("webui", "tunnel closed: %s", t.CloseReason())
		c.mu.Lock()
		c.tunnel = nil
		c.url = ""
		c.cancel = nil
		c.mu.Unlock()
	}()
	return c.url, true, nil
}

// StopTunnel 关闭隧道(未激活为 no-op)
func (c *TunnelCtl) StopTunnel() {
	c.mu.Lock()
	t, cancel := c.tunnel, c.cancel
	c.tunnel, c.cancel, c.url = nil, nil, ""
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if t != nil {
		t.Close("stopped by /webui tunnel stop")
	}
}

// TunnelURL 当前隧道 URL(未激活为空)
func (c *TunnelCtl) TunnelURL() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.url
}
