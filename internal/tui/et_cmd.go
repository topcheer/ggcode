package tui

import (
	"net/url"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/debug"
)

// /et 命令族(顶级命令,与 /webui 解耦):etunnel 隧道生命周期管理。
//   /et on            激活隧道(使用 /et setup 的子域名/token,未配置走免费计划)
//   /et off           关闭隧道
//   /et show          当前状态(URL + 配置摘要,token 打码)
//   /et setup <域名> <token>   持久化付费计划配置(写 config etunnel 节)
//
// 控制器由 root 注入(webuiSrv 侧 TunnelCtl 单例);WebUI 本地地址仍从
// webuiURL 提取——隧道发布的就是 WebUI 服务。

type etTunnelReadyMsg struct {
	url     string
	suffix  string // "#token=…"（公网访问鉴权必须随 URL；本地 webuiURL 中提取）
	err     error
	started bool // false=已激活直接复用
}

func (m Model) handleEtTunnelReadyMsg(msg etTunnelReadyMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, func() tea.Msg { return streamMsg("⚠ et tunnel failed: " + msg.err.Error()) }
	}
	prefix := "⬢ et tunnel active"
	if !msg.started {
		prefix = "⬢ et tunnel (already active)"
	}
	m.chatWriteSystem(nextSystemID(), prefix+": "+msg.url+msg.suffix+"  (free plan: 4h lifetime, 1 Mbps)")
	return m, nil
}

// maskToken 打码展示:仅留尾部 4 位
func maskToken(tok string) string {
	if tok == "" {
		return "(未设置)"
	}
	if len(tok) <= 4 {
		return "****"
	}
	return "****" + tok[len(tok)-4:]
}

// handleEtCommand /et [on|off|show|setup <subdomain> <token>]
func (m Model) handleEtCommand(parts []string) tea.Cmd {
	sub := ""
	if len(parts) > 1 {
		sub = strings.ToLower(parts[1])
	}

	// setup:持久化付费计划配置
	if sub == "setup" {
		if len(parts) < 4 {
			cfg := m.config.ETunnel
			m.chatWriteSystem(nextSystemID(),
				"用法: /et setup <子域名> <token>\n当前配置: 子域名="+maskToken(cfg.Subdomain)+"(域名本身非敏感,直接展示:"+cfg.Subdomain+") token="+maskToken(cfg.Token))
			return nil
		}
		m.config.ETunnel.Subdomain = strings.Trim(parts[2], ". ")
		m.config.ETunnel.Token = parts[3]
		if err := m.config.SaveScoped(m.configSaveScope); err != nil {
			m.chatWriteSystem(nextSystemID(), "⚠ et setup 保存失败: "+err.Error())
			return nil
		}
		m.chatWriteSystem(nextSystemID(),
			"⬢ et 配置已保存: 子域名="+m.config.ETunnel.Subdomain+" token="+maskToken(m.config.ETunnel.Token)+
				"\n下次 /et on 将请求该自定义域名(需付费计划)。")
		return nil
	}

	if m.webuiTunnel == nil {
		m.chatWriteSystem(nextSystemID(), "ⓘ et tunnel is not available in this session.")
		return nil
	}

	switch sub {
	case "off":
		if m.webuiTunnel.TunnelURL() == "" {
			m.chatWriteSystem(nextSystemID(), "ⓘ et tunnel is not active.")
			return nil
		}
		m.webuiTunnel.StopTunnel()
		m.chatWriteSystem(nextSystemID(), "⬢ et tunnel closed.")
		return nil
	case "show", "":
		suffix := ""
		if m.webuiURL != "" {
			if idx := strings.Index(m.webuiURL, "#token="); idx >= 0 {
				suffix = m.webuiURL[idx:]
			}
		}
		cfg := m.config.ETunnel
		plan := "free"
		if cfg.Subdomain != "" || cfg.Token != "" {
			plan = "paid(子域名=" + cfg.Subdomain + " token=" + maskToken(cfg.Token) + ")"
		}
		if u := m.webuiTunnel.TunnelURL(); u != "" {
			m.chatWriteSystem(nextSystemID(), "⬢ et tunnel active: "+u+suffix+"\n计划: "+plan)
		} else {
			m.chatWriteSystem(nextSystemID(), "ⓘ et tunnel 未激活。/et on 激活;计划: "+plan)
		}
		return nil
	case "on":
		return m.etTunnelOn()
	default:
		m.chatWriteSystem(nextSystemID(), "用法: /et [on|off|show|setup <子域名> <token>]")
		return nil
	}
}

// etTunnelOn 激活隧道:本地地址取自 webuiURL,付费配置取自 config.ETunnel
func (m Model) etTunnelOn() tea.Cmd {
	cfg := m.config.ETunnel
	suffix := ""
	if m.webuiURL != "" {
		if idx := strings.Index(m.webuiURL, "#token="); idx >= 0 {
			suffix = m.webuiURL[idx:]
		}
	}
	if u := m.webuiTunnel.TunnelURL(); u != "" {
		m.chatWriteSystem(nextSystemID(), "⬢ et tunnel (already active): "+u+suffix)
		return nil
	}
	if m.webuiURL == "" {
		m.chatWriteSystem(nextSystemID(), "ⓘ WebUI server is not running in this session (e.g. --no-webui or headless mode).")
		return nil
	}
	u, err := url.Parse(m.webuiURL)
	if err != nil || u.Host == "" {
		m.chatWriteSystem(nextSystemID(), "⚠ et tunnel failed: cannot parse local address from "+m.webuiURL)
		return nil
	}
	localAddr := u.Host
	if cfg.Subdomain != "" && cfg.Token != "" {
		m.chatWriteSystem(nextSystemID(), "⬢ Opening et tunnel via custom subdomain \""+cfg.Subdomain+"\" …")
	} else {
		m.chatWriteSystem(nextSystemID(), "⬢ Opening et tunnel via free plan (random subdomain, ~seconds)。付费定制域名: /et setup <子域名> <token>")
	}
	return func() tea.Msg {
		tunnelURL, started, terr := m.webuiTunnel.StartTunnelOpts(localAddr, cfg.Subdomain, cfg.Token)
		debug.Log("tui", "et tunnel ready: %s started=%v err=%v", tunnelURL, started, terr)
		return etTunnelReadyMsg{url: tunnelURL, suffix: suffix, err: terr, started: started}
	}
}
