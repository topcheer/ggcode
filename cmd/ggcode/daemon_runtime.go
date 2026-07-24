package main

import (
	"os/exec"
	"runtime"
	"strings"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tool"
)

// daemonToolPresenter adapts the IM DescribeTool function to daemon.ToolPresenter.
type daemonToolPresenter struct {
	lang im.ToolLanguage
}

func (p *daemonToolPresenter) Present(toolName, rawArgs string) (displayName, detail, activity string) {
	pres := im.DescribeTool(p.lang, toolName, rawArgs)
	return pres.DisplayName, pres.Detail, pres.Activity
}

// writeClipboard copies text to the system clipboard (best-effort).
func writeClipboard(text string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "linux":
		if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		} else if _, err := exec.LookPath("xsel"); err == nil {
			cmd = exec.Command("xsel", "--clipboard", "--input")
		} else if _, err := exec.LookPath("wl-copy"); err == nil {
			cmd = exec.Command("wl-copy")
		}
	case "windows":
		cmd = exec.Command("clip")
	}
	if cmd == nil {
		return
	}
	cmd.Stdin = strings.NewReader(text)
	_ = cmd.Run()
}

// daemonModeSwitcher implements tool.ModeSwitcher to persist permission mode
// changes to session metadata in daemon mode.
type daemonModeSwitcher struct {
	policy *permission.ConfigPolicy
	ses    *session.Session
	store  session.Store
}

func (s *daemonModeSwitcher) Mode() permission.PermissionMode {
	if s.policy == nil {
		return permission.DefaultMode
	}
	return s.policy.CurrentMode()
}

func (s *daemonModeSwitcher) SetMode(mode permission.PermissionMode) {
	if s.policy != nil {
		s.policy.SetMode(mode)
	}
	// Persist to session, not to global config.
	if s.ses != nil && s.store != nil {
		s.ses.PermissionMode = mode.String()
		_ = s.store.AppendMetaToDisk(s.ses)
	}
}

func (s *daemonModeSwitcher) RememberMode(mode permission.PermissionMode) permission.PermissionMode {
	return permission.SupervisedMode
}

func (s *daemonModeSwitcher) RestoreMode(fallback permission.PermissionMode) permission.PermissionMode {
	return fallback
}

// daemonRuntimeProvider implements tool.RuntimeStatusProvider for daemon mode.
type daemonRuntimeProvider struct {
	ses    *session.Session
	cfg    *config.Config
	imMgr  *im.Manager
	bridge *im.DaemonBridge
	agent  *agent.Agent
}

func (p *daemonRuntimeProvider) RuntimeSessionID() string {
	if p.ses != nil {
		return p.ses.ID
	}
	return ""
}

func (p *daemonRuntimeProvider) RuntimePermissionMode() string {
	if p.ses != nil && p.ses.PermissionMode != "" {
		return p.ses.PermissionMode
	}
	return p.cfg.DefaultMode
}

func (p *daemonRuntimeProvider) RuntimeVendor() string {
	if p.cfg != nil {
		return p.cfg.Vendor
	}
	return ""
}

func (p *daemonRuntimeProvider) RuntimeEndpoint() string {
	if p.cfg != nil {
		return p.cfg.Endpoint
	}
	return ""
}

func (p *daemonRuntimeProvider) RuntimeModel() string {
	if p.cfg != nil {
		return p.cfg.Model
	}
	return ""
}

func (p *daemonRuntimeProvider) RuntimeLanguage() string {
	if p.cfg != nil {
		return p.cfg.Language
	}
	return ""
}

func (p *daemonRuntimeProvider) RuntimeContextWindow() int {
	if p.agent != nil && p.agent.ContextManager() != nil {
		return p.agent.ContextManager().ContextWindow()
	}
	return 0
}

func (p *daemonRuntimeProvider) RuntimeMaxTokens() int {
	if p.agent != nil && p.agent.ContextManager() != nil {
		return p.agent.ContextManager().OutputReserve()
	}
	return 0
}

func (p *daemonRuntimeProvider) RuntimeIMAdapters() []tool.RuntimeIMAdapterInfo {
	if p.imMgr == nil {
		return nil
	}
	snap := p.imMgr.Snapshot()
	channels := make(map[string]string)
	for _, b := range snap.CurrentBindings {
		channels[b.Adapter] = b.ChannelID
	}
	var result []tool.RuntimeIMAdapterInfo
	for _, a := range snap.Adapters {
		result = append(result, tool.RuntimeIMAdapterInfo{
			Name:     a.Name,
			Platform: string(a.Platform),
			Online:   a.Healthy,
			Muted:    a.Status == "muted",
			Channel:  channels[a.Name],
		})
	}
	return result
}

func (p *daemonRuntimeProvider) RuntimeMobile() tool.RuntimeMobileInfo {
	if p.bridge == nil {
		return tool.RuntimeMobileInfo{}
	}
	// Daemon doesn't have a mobile tunnel — return empty.
	return tool.RuntimeMobileInfo{}
}
