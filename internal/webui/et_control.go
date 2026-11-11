package webui

import (
	"fmt"
	"net/url"
	"strings"
)

// EtunnelController is the tunnel-controller surface ControlEt needs
// (satisfied by *TunnelCtl; kept as an interface so hosts/tests can fake it).
type EtunnelController interface {
	StartTunnelOpts(localAddr, subdomain, token string) (string, bool, error)
	StopTunnel()
	TunnelURL() string
}

// ControlEt implements the /et command semantics for non-TUI hosts (IM
// inbound, daemon). It mirrors the TUI implementation in
// internal/tui/et_cmd.go; the TUI "on" path stays TUI-specific (async
// bubbletea cmd) while this helper starts tunnels synchronously, which is
// the right trade-off for IM handlers that must reply with a string.
//
//	args: subcommand tokens after "/et" (may be empty for show).
//	saveCfg persists a paid-plan config change (subdomain, token); may be nil
//	for read-only hosts, in which case setup returns an error.
func ControlEt(ctl EtunnelController, localURL, subdomain, token string, saveCfg func(subdomain, token string) error, args []string) (string, error) {
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}
	mask := func(tok string) string {
		if tok == "" {
			return "(unset)"
		}
		if len(tok) <= 4 {
			return "****"
		}
		return "****" + tok[len(tok)-4:]
	}
	plan := func() string {
		if subdomain != "" || token != "" {
			return "paid (subdomain=" + subdomain + " token=" + mask(token) + ")"
		}
		return "free"
	}
	tokenSuffix := func() string {
		if idx := strings.Index(localURL, "#token="); idx >= 0 {
			return localURL[idx:]
		}
		return ""
	}

	if sub == "setup" {
		if len(args) < 3 {
			return fmt.Sprintf("usage: /et setup <subdomain> <token>\ncurrent config: subdomain=%s token=%s", subdomain, mask(token)), nil
		}
		if saveCfg == nil {
			return "", fmt.Errorf("et setup is not available on this host")
		}
		newSub := strings.Trim(args[1], ". ")
		newTok := args[2]
		if err := saveCfg(newSub, newTok); err != nil {
			return "", fmt.Errorf("et setup save failed: %w", err)
		}
		return fmt.Sprintf("et config saved: subdomain=%s token=%s\nnext /et on will request the custom domain (paid plan required)", newSub, mask(newTok)), nil
	}

	if ctl == nil {
		return "et tunnel is not available in this session", nil
	}
	switch sub {
	case "off":
		if ctl.TunnelURL() == "" {
			return "et tunnel is not active", nil
		}
		ctl.StopTunnel()
		return "et tunnel closed", nil
	case "show", "":
		if u := ctl.TunnelURL(); u != "" {
			return "et tunnel active: " + u + tokenSuffix() + "\nplan: " + plan(), nil
		}
		return "et tunnel not active. /et on to activate; plan: " + plan(), nil
	case "on":
		if u := ctl.TunnelURL(); u != "" {
			return "et tunnel (already active): " + u + tokenSuffix(), nil
		}
		if localURL == "" {
			return "WebUI server is not running in this session; cannot start the tunnel", nil
		}
		u, err := url.Parse(localURL)
		if err != nil || u.Host == "" {
			return "", fmt.Errorf("cannot parse local address from %s", localURL)
		}
		tunnelURL, _, err := ctl.StartTunnelOpts(u.Host, subdomain, token)
		if err != nil {
			return "", fmt.Errorf("et tunnel failed: %w", err)
		}
		return "et tunnel active: " + tunnelURL + tokenSuffix() + "\nplan: " + plan(), nil
	default:
		return "usage: /et [on|off|show|setup <subdomain> <token>]", nil
	}
}
