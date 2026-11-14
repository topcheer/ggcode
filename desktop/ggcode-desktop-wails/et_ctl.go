package main

// #5574: desktop etunnel public mapping (parity with TUI/daemon).
//
// Layering mirrors the daemon precedent (cmd/ggcode/daemon.go --etunnel +
// internal/im/daemon_bridge.go SetEtunnel), per the issue v2 design:
//
//	Wails bindings (this file) → webui.ControlEt (existing) →
//	*webui.TunnelCtl (existing) → etunnel SDK
//
// The bridge layer only assembles args and echoes state — it never
// re-implements start/stop. The host layer lazily hosts the shared webui
// Server on 127.0.0.1:0 (random port + #token fragment) so the desktop
// adds zero new HTTP surface of its own.

import (
	"context"
	"fmt"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/webui"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// EtunnelState is the tunnel-card payload: ControlEt's reply text plus the
// controller's post-call state for the status dot / URL display.
type EtunnelState struct {
	Message string `json:"message"`
	Active  bool   `json:"active"`
	URL     string `json:"url"`
}

// EtunnelStart activates the public tunnel (thin-wrap: /et on).
func (a *App) EtunnelStart() (EtunnelState, error) { return a.etControl("on") }

// EtunnelStop closes the tunnel (thin-wrap: /et off).
func (a *App) EtunnelStop() (EtunnelState, error) { return a.etControl("off") }

// EtunnelStatus reports current tunnel state (thin-wrap: /et show).
func (a *App) EtunnelStatus() (EtunnelState, error) { return a.etControl("show") }

// EtunnelSetup persists paid-plan credentials (thin-wrap: /et setup).
func (a *App) EtunnelSetup(subdomain, token string) (EtunnelState, error) {
	return a.etControl("setup", subdomain, token)
}

// etControl assembles args and delegates to webui.ControlEt; login guidance
// (ControlEt's TUI bootstrap text) rides through unchanged in Message.
func (a *App) etControl(args ...string) (EtunnelState, error) {
	ctl, localURL, err := a.etHost()
	if err != nil {
		return EtunnelState{}, err
	}
	sub, tok, plan := "", "", ""
	if a.dc != nil {
		sub, tok, plan = a.dc.GetETunnel()
	}
	msg, err := webui.ControlEt(ctl, localURL, sub, tok, plan, a.etSaveCfg(), args)
	return EtunnelState{
		Message: msg,
		Active:  ctl.TunnelURL() != "",
		URL:     ctl.TunnelURL(),
	}, err
}

// etSaveCfg adapts the desktop settings storage to ControlEt's saveCfg hook
// (nil for read-only hosts → ControlEt's setup errors out, matching daemon).
func (a *App) etSaveCfg() func(subdomain, token string) error {
	if a.dc == nil {
		return nil
	}
	return func(subdomain, token string) error {
		a.dc.SetETunnel(subdomain, token)
		return a.dc.Save()
	}
}

// etHost lazily hosts the webui Server on a random loopback port and the
// TunnelCtl singleton. Once hosted (or faked for tests) it is reused.
func (a *App) etHost() (webui.EtunnelController, string, error) {
	a.etMu.Lock()
	defer a.etMu.Unlock()
	if a.etCtl != nil {
		return a.etCtl, a.etLocalURL, nil
	}

	cfg, err := config.LoadWithInstance(config.ConfigPath(), a.workDir)
	if err != nil || cfg == nil {
		// Host anyway with defaults: the tunnel must not depend on a fully
		// configured ggcode.yaml (matches daemon's tolerant hosting).
		cfg = config.DefaultConfig()
	}
	srv := webui.NewServer(cfg)
	addr, serr := srv.Start("127.0.0.1:0")
	if serr != nil {
		_ = srv.Close()
		return nil, "", fmt.Errorf("etunnel: webui host failed: %w", serr)
	}
	localURL := "http://" + addr
	if tk := srv.Token(); tk != "" {
		localURL += "#token=" + tk
	}
	a.etSrv = srv
	a.etCtl = &webui.TunnelCtl{}
	a.etLocalURL = localURL
	a.startEtPollerLocked()
	debug.Log("desktop", "etunnel: webui hosted at %s", addr)
	return a.etCtl, a.etLocalURL, nil
}

// startEtPollerLocked pushes "etunnel:state" EventsEmit whenever the tunnel
// activity flips (tunnelMonitor clears state on remote disconnect — the
// poller relays that to the UI so the status dot falls back without manual
// refresh). Caller holds etMu.
func (a *App) startEtPollerLocked() {
	if a.ctx == nil || a.etPollCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.etPollCancel = cancel
	ctl := a.etCtl
	lastActive := ctl.TunnelURL() != ""
	safego.Go("desktop.etunnelStatePoller", func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				url := ctl.TunnelURL()
				if active := url != ""; active != lastActive {
					lastActive = active
					wailsruntime.EventsEmit(a.ctx, "etunnel:state", map[string]any{
						"active": active,
						"url":    url,
					})
				}
			}
		}
	})
}

// stopEtunnel tears the whole surface down on app exit: poller → tunnel →
// hosted Server (DoD: zero residue on exit).
func (a *App) stopEtunnel() {
	a.etMu.Lock()
	defer a.etMu.Unlock()
	if a.etPollCancel != nil {
		a.etPollCancel()
		a.etPollCancel = nil
	}
	if a.etCtl != nil {
		a.etCtl.StopTunnel()
		a.etCtl = nil
	}
	if a.etSrv != nil {
		if err := a.etSrv.Close(); err != nil {
			debug.Log("desktop", "etunnel: webui host close: %v", err)
		}
		a.etSrv = nil
	}
	a.etLocalURL = ""
}
