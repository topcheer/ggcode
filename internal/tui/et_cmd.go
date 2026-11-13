package tui

import (
	"context"
	"net/url"
	"os/exec"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"

	etunnel "github.com/topcheer/etunnel/sdk"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// /et 命令族(顶级命令,与 /webui 解耦):etunnel 隧道生命周期管理。
//   /et on            激活隧道(使用 /et setup|login 的配置,未配置走免费计划)
//   /et off           关闭隧道
//   /et show          当前状态(URL + 配置摘要,token 打码)
//   /et setup <域名> <token>   持久化付费计划配置(写 config etunnel 节)
//   /et login         浏览器授权登录(SDK loopback 流程,回执持久化)
//
// 控制器由 root 注入(webuiSrv 侧 TunnelCtl 单例);WebUI 本地地址仍从
// webuiURL 提取——隧道发布的就是 WebUI 服务。

type etTunnelReadyMsg struct {
	url     string
	suffix  string // "#token=…"（公网访问鉴权必须随 URL；本地 webuiURL 中提取）
	err     error
	started bool   // false=已激活直接复用
	plan    string // ""=free（随 URL 打 free 限制）；非空=paid 标注（#5395，etTunnelOn 按配置判定）
}

// et login 两段式消息：授权 URL 先于完成回执上屏（浏览器流程可长达
// LoginTimeout=10min，用户必须先看到去哪授权），凭据完成后再持久化。
type etLoginAuthMsg struct {
	authURL string
}

type etLoginDoneMsg struct {
	creds *etunnel.Credentials
	err   error
}

func (m Model) handleEtTunnelReadyMsg(msg etTunnelReadyMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, func() tea.Msg { return streamMsg("⚠ et tunnel failed: " + msg.err.Error()) }
	}
	prefix := "⬢ et tunnel active"
	if !msg.started {
		prefix = "⬢ et tunnel (already active)"
	}
	// #5395: 最终成功消息按实际计划标注——付费隧道不得声称 free 限制。
	planNote := "  (free plan: 4h lifetime, 1 Mbps)"
	if msg.plan != "" {
		planNote = "\n计划: " + msg.plan
	}
	fullURL := msg.url + msg.suffix
	m.chatWriteSystem(nextSystemID(), prefix+": "+fullURL+planNote+"\n扫码可用手机打开（Esc 关闭二维码）")
	// QR 内容=完整含 #token= 的 URL（鉴权随码，手机直接打开零输入）。
	// 复用通用 QR overlay（与 IM 面板同机制），无新增渲染路径。
	m.openQROverlayDirect(
		m.t("panel.qr.title")+" — et tunnel",
		m.t("panel.qr.scan_hint"),
		renderContactQRCode(fullURL),
		fullURL,
	)
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

// etPlanLabel 计划标注：login 回执 Plan 为准（free 账户 login 后 token 非空
// 但并非付费，不得凭 token 误标 paid）；无回执（纯 /et setup 用户）退回
// #5395 启发式：subdomain/token 任一非空即付费。
func etPlanLabel(subdomain, token, planReceipt string) string {
	if planReceipt != "" {
		if planReceipt == "free" {
			return "free"
		}
		return "paid(" + planReceipt + " 子域名=" + subdomain + " token=" + maskToken(token) + ")"
	}
	if subdomain != "" || token != "" {
		return "paid(子域名=" + subdomain + " token=" + maskToken(token) + ")"
	}
	return "free"
}

// handleEtCommand /et [on|off|show|setup <subdomain> <token>|login]
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
		plan := etPlanLabel(cfg.Subdomain, cfg.Token, cfg.Plan)
		if u := m.webuiTunnel.TunnelURL(); u != "" {
			m.chatWriteSystem(nextSystemID(), "⬢ et tunnel active: "+u+suffix+"\n计划: "+plan)
		} else {
			m.chatWriteSystem(nextSystemID(), "ⓘ et tunnel 未激活。/et on 激活;计划: "+plan)
		}
		return nil
	case "on":
		return m.etTunnelOn()
	case "login":
		return m.etLoginStart()
	default:
		m.chatWriteSystem(nextSystemID(), "用法: /et [on|off|show|setup <子域名> <token>|login]")
		return nil
	}
}

// etLoginServer 授权服务器基址：与隧道默认服务同一主机（SDK DefaultServer
// wss://host/tunnel → https://host）。隧道与登录共用服务端。
func etLoginServer() string {
	norm := etunnel.NormalizeServerURL(etunnel.DefaultServer)
	u, err := url.Parse(norm)
	if err != nil || u.Host == "" {
		return "https://t.etun.uk" // 兜底=SDK 默认主机
	}
	return "https://" + u.Host
}

// etLoginStart 启动 SDK 浏览器授权流（loopback）：两条并行 bubbletea 命令——
// 授权 URL 上屏（openBrowser 回调内投递）与完成回执（阻塞至浏览器回调或
// 超时）。token 生效时机=下次 /et on。
func (m Model) etLoginStart() tea.Cmd {
	m.chatWriteSystem(nextSystemID(),
		"⬢ et login: 正在发起浏览器授权…（授权页将自动打开；超时上限 10 分钟）")

	authCh := make(chan string, 1)
	doneCh := make(chan etLoginDoneMsg, 1)
	// #5561: SDK 在 openBrowser 回调之前有三条失败路径（loopback bind、
	// auth start 网络失败、空 AuthURL），回调不触发时 authCh 永无消息。
	// quitCh 在 Login 返回后 close，给阻塞读一个退出路径，避免 bubbletea
	// Cmd goroutine 永久泄漏。
	quitCh := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	safego.Go("tui.etLogin", func() {
		defer cancel()
		defer close(quitCh)
		creds, err := etunnel.Login(ctx, etLoginServer(), func(authURL string) error {
			select {
			case authCh <- authURL:
			default:
			}
			var cmd *exec.Cmd
			switch runtime.GOOS {
			case "darwin":
				cmd = exec.Command("open", authURL)
			case "windows":
				cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", authURL)
			default:
				cmd = exec.Command("xdg-open", authURL)
			}
			return cmd.Start()
		})
		debug.Log("tui", "et login done: plan=%s err=%v", credsPlan(creds), err)
		doneCh <- etLoginDoneMsg{creds: creds, err: err}
	})
	return tea.Batch(
		func() tea.Msg {
			select {
			case u := <-authCh:
				return etLoginAuthMsg{authURL: u}
			case <-quitCh: // #5561: Login 已返回且回调未触发——退出而非永久阻塞
				return nil
			}
		},
		func() tea.Msg { return <-doneCh },
	)
}

// credsPlan 登录回执套餐（日志/展示用）。
func credsPlan(c *etunnel.Credentials) string {
	if c == nil {
		return ""
	}
	return c.Plan
}

// handleEtLoginAuthMsg 授权 URL 上屏（浏览器已尝试打开；未弹出则手动访问）。
func (m Model) handleEtLoginAuthMsg(msg etLoginAuthMsg) (tea.Model, tea.Cmd) {
	m.chatWriteSystem(nextSystemID(),
		"⬢ et login: 请在浏览器完成授权（若未自动打开，访问:\n"+msg.authURL+"\n授权完成后回到本会话，凭据将自动保存）")
	return m, nil
}

// handleEtLoginDoneMsg 登录完成：凭据持久化（APIToken→Token，同 setup 字段槽，
// 下次 /et on 即生效）。
func (m Model) handleEtLoginDoneMsg(msg etLoginDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, func() tea.Msg { return streamMsg("⚠ et login failed: " + msg.err.Error()) }
	}
	c := msg.creds
	m.config.ETunnel.Token = c.APIToken
	m.config.ETunnel.Plan = c.Plan
	m.config.ETunnel.Subdomains = c.Subdomains
	if err := m.config.SaveScoped(m.configSaveScope); err != nil {
		return m, func() tea.Msg { return streamMsg("⚠ et login 凭据保存失败: " + err.Error()) }
	}
	domains := "(无自定义域名=free)"
	if len(c.Subdomains) > 0 {
		domains = strings.Join(c.Subdomains, ", ")
	}
	m.chatWriteSystem(nextSystemID(),
		"⬢ et login 成功: 套餐="+c.Plan+" token="+maskToken(c.APIToken)+"\n可用域名: "+domains+
			"\n下次 /et on 即以本账户凭据激活。")
	return m, nil
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
	// #5395: 与 show 分支及 StartTunnelOpts 的付费语义一致；login 回执 Plan
	// 存在时以它为准（free 登录账户 token 非空但走免费路径，不误标 paid）。
	plan := etPlanLabel(cfg.Subdomain, cfg.Token, cfg.Plan)
	paid := strings.HasPrefix(plan, "paid")
	if paid {
		m.chatWriteSystem(nextSystemID(), "⬢ Opening et tunnel via custom subdomain \""+cfg.Subdomain+"\" …")
	} else {
		m.chatWriteSystem(nextSystemID(), "⬢ Opening et tunnel via free plan (random subdomain, ~seconds)。付费定制域名: /et setup <子域名> <token>")
	}
	return func() tea.Msg {
		tunnelURL, started, terr := m.webuiTunnel.StartTunnelOpts(localAddr, cfg.Subdomain, cfg.Token)
		debug.Log("tui", "et tunnel ready: %s started=%v err=%v", tunnelURL, started, terr)
		// readyMsg.plan 语义：""=free（沿用 4h/1Mbps free 注），非空=已标注计划
		readyPlan := ""
		if paid {
			readyPlan = plan
		}
		return etTunnelReadyMsg{url: tunnelURL, suffix: suffix, err: terr, started: started, plan: readyPlan}
	}
}
