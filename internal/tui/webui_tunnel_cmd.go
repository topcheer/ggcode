package tui

import (
	"net/url"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/webui"
)

// webui tunnel 子命令(/webui tunnel [stop]):经 etunnel 把 WebUI 本地地址
// 映射为公网 https 域名并打印完整 URL(含 #token=,鉴权随 URL 走)。
// 控制器由 root 注入(webuiSrv 侧 TunnelCtl 单例)。

type webuiTunnelReadyMsg struct {
	url     string
	suffix  string // "#token=…"（公网访问鉴权必须随 URL；本地 webuiURL 中提取）
	err     error
	started bool // false=已激活直接复用
}

func (m Model) handleWebuiTunnelReadyMsg(msg webuiTunnelReadyMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, func() tea.Msg { return streamMsg("⚠ WebUI tunnel failed: " + msg.err.Error()) }
	}
	prefix := "⬢ WebUI tunnel active"
	if !msg.started {
		prefix = "⬢ WebUI tunnel (already active)"
	}
	m.chatWriteSystem(nextSystemID(), prefix+": "+msg.url+msg.suffix+"  (free plan: 4h lifetime, 1 Mbps)")
	return m, nil
}

// handleWebuiTunnelCommand /webui tunnel [off]
func (m Model) handleWebuiTunnelCommand(parts []string) tea.Cmd {
	if m.webuiTunnel == nil {
		m.chatWriteSystem(nextSystemID(), "ⓘ WebUI tunnel is not available in this session.")
		return nil
	}
	// 入口分流：parts = [/webui, tunnel, off]（也容忍直接 /webui off）
	offWord := ""
	if len(parts) > 2 && strings.EqualFold(parts[1], "tunnel") {
		offWord = parts[2]
	} else if len(parts) > 1 {
		offWord = parts[1]
	}
	if strings.EqualFold(offWord, "off") {
		if m.webuiTunnel.TunnelURL() == "" {
			m.chatWriteSystem(nextSystemID(), "ⓘ WebUI tunnel is not active.")
			return nil
		}
		m.webuiTunnel.StopTunnel()
		m.chatWriteSystem(nextSystemID(), "⬢ WebUI tunnel closed.")
		return nil
	}
	// 从 webuiURL 提取 #token= 片段（公网 URL 必须携带，否则 WS 握手 401）
	suffix := ""
	if m.webuiURL != "" {
		if idx := strings.Index(m.webuiURL, "#token="); idx >= 0 {
			suffix = m.webuiURL[idx:]
		}
	}
	if u := m.webuiTunnel.TunnelURL(); u != "" {
		m.chatWriteSystem(nextSystemID(), "⬢ WebUI tunnel (already active): "+u+suffix)
		return nil
	}
	if m.webuiURL == "" {
		m.chatWriteSystem(nextSystemID(), "ⓘ WebUI server is not running in this session (e.g. --no-webui or headless mode).")
		return nil
	}
	// 从 webuiURL(http://127.0.0.1:PORT/#token=…)解析本地映射地址
	u, err := url.Parse(m.webuiURL)
	if err != nil || u.Host == "" {
		m.chatWriteSystem(nextSystemID(), "⚠ WebUI tunnel failed: cannot parse local address from "+m.webuiURL)
		return nil
	}
	localAddr := u.Host
	m.chatWriteSystem(nextSystemID(), "⬢ Opening WebUI tunnel via "+webui.TunnelServer()+" … (free plan: random subdomain, ~seconds)")
	return func() tea.Msg {
		tunnelURL, started, terr := m.webuiTunnel.StartTunnel(localAddr)
		debug.Log("tui", "webui tunnel ready: %s started=%v err=%v", tunnelURL, started, terr)
		return webuiTunnelReadyMsg{url: tunnelURL, suffix: suffix, err: terr, started: started}
	}
}
