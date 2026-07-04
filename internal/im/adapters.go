package im

import (
	"context"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

type AdapterController struct {
	cancel context.CancelFunc
}

type startableSink interface {
	Sink
	Start(context.Context)
}

func (c *AdapterController) Stop() {
	if c == nil || c.cancel == nil {
		return
	}
	c.cancel()
}

func StartConfiguredAdapters(parent context.Context, cfg config.IMConfig, mgr *Manager) (*AdapterController, error) {
	if mgr == nil {
		return nil, fmt.Errorf("IM manager is nil")
	}
	ctx, cancel := context.WithCancel(parent)
	controller := &AdapterController{cancel: cancel}
	for name, adapterCfg := range cfg.Adapters {
		if !adapterCfg.Enabled {
			continue
		}
		if err := startConfiguredAdapter(ctx, cfg, name, adapterCfg, mgr); err != nil {
			cancel()
			return nil, err
		}
	}

	return controller, nil
}

func StartCurrentBindingAdapter(parent context.Context, cfg config.IMConfig, mgr *Manager) (*AdapterController, error) {
	if mgr == nil {
		return nil, fmt.Errorf("IM manager is nil")
	}
	ctx, cancel := context.WithCancel(parent)
	controller := &AdapterController{cancel: cancel}

	bindings := mgr.CurrentBindings()
	if len(bindings) == 0 {
		return controller, nil
	}

	for _, binding := range bindings {
		if strings.TrimSpace(binding.Adapter) == "" {
			continue
		}
		// Skip muted bindings — muted adapters must never start connections
		if binding.Muted {
			debug.Log("im", "StartCurrentBindingAdapter: skipping muted adapter %q", binding.Adapter)
			continue
		}
		// Built-in PC adapter — only start when binding explicitly targets it
		if binding.Adapter == "_pc_builtin" || strings.EqualFold(binding.Adapter, string(PlatformPrivateClaw)) {
			startPCAdapter(ctx, cfg, mgr)
			continue
		}

		adapterCfg, ok := cfg.Adapters[binding.Adapter]
		if !ok || !adapterCfg.Enabled {
			continue
		}
		if err := startConfiguredAdapter(ctx, cfg, binding.Adapter, adapterCfg, mgr); err != nil {
			cancel()
			return nil, err
		}
	}
	return controller, nil
}

func StartNamedAdapter(parent context.Context, cfg config.IMConfig, name string, mgr *Manager) error {
	if mgr == nil {
		return fmt.Errorf("IM manager is nil")
	}
	// Never start a muted adapter — this is a hard guard to prevent
	// muted adapters from opening connections regardless of caller.
	if mgr.IsMuted(name) {
		debug.Log("im", "StartNamedAdapter: refusing to start muted adapter %q", name)
		return fmt.Errorf("adapter %q is muted — unmute it first", name)
	}
	adapterCfg, ok := cfg.Adapters[name]
	if !ok {
		return fmt.Errorf("IM adapter %q is not configured", name)
	}
	return startConfiguredAdapter(parent, cfg, name, adapterCfg, mgr)
}

func startConfiguredAdapter(ctx context.Context, cfg config.IMConfig, name string, adapterCfg config.IMAdapterConfig, mgr *Manager) error {
	if !adapterCfg.Enabled {
		return nil
	}
	// Create a child context so Manager can cancel this adapter individually.
	adapterCtx, adapterCancel := context.WithCancel(ctx)
	mgr.RegisterAdapterCancel(name, adapterCancel)

	start := func(adapter startableSink) {
		mgr.RegisterSink(adapter)
		adapter.Start(adapterCtx)
	}

	switch Platform(strings.TrimSpace(adapterCfg.Platform)) {
	case PlatformQQ:
		adapter, err := newQQAdapter(name, cfg, adapterCfg, mgr)
		if err != nil {
			adapterCancel()
			return err
		}
		start(adapter)
	case PlatformTelegram:
		adapter, err := newTGAdapter(name, cfg, adapterCfg, mgr)
		if err != nil {
			adapterCancel()
			return err
		}
		start(adapter)
	case PlatformPrivateClaw:
		sessionStore := newDefaultPCSessionStore()
		adapter, err := newPCAdapter(name, cfg, adapterCfg, mgr, sessionStore)
		if err != nil {
			adapterCancel()
			return err
		}
		start(adapter)
	case PlatformDiscord:
		adapter, err := newDiscordAdapter(name, cfg, adapterCfg, mgr)
		if err != nil {
			adapterCancel()
			return err
		}
		start(adapter)
	case PlatformFeishu:
		adapter, err := newFeishuAdapter(name, cfg, adapterCfg, mgr)
		if err != nil {
			adapterCancel()
			return err
		}
		start(adapter)
	case PlatformDingTalk:
		adapter, err := newDingtalkAdapter(name, mgr, adapterCfg)
		if err != nil {
			adapterCancel()
			return err
		}
		start(adapter)
	case PlatformSlack:
		adapter, err := newSlackAdapter(name, cfg, adapterCfg, mgr)
		if err != nil {
			adapterCancel()
			return err
		}
		start(adapter)
	case PlatformDummy:
		adapter := newDummyAdapter(name, cfg, adapterCfg, mgr)
		start(adapter)
	case PlatformWechat:
		adapter, err := newWechatAdapter(name, cfg, adapterCfg, mgr)
		if err != nil {
			adapterCancel()
			return err
		}
		start(adapter)
	case PlatformWeCom:
		adapter, err := newWeComAdapter(name, cfg, adapterCfg, mgr)
		if err != nil {
			adapterCancel()
			return err
		}
		start(adapter)
	case PlatformMattermost:
		adapter, err := newMattermostAdapter(name, cfg, adapterCfg, mgr)
		if err != nil {
			adapterCancel()
			return err
		}
		start(adapter)
	case PlatformMatrix:
		adapter, err := newMatrixAdapter(name, cfg, adapterCfg, mgr)
		if err != nil {
			adapterCancel()
			return err
		}
		start(adapter)
	case PlatformSignal:
		adapter, err := newSignalAdapter(name, cfg, adapterCfg, mgr)
		if err != nil {
			adapterCancel()
			return err
		}
		start(adapter)
	case PlatformIRC:
		adapter, err := newIRCAdapter(name, cfg, adapterCfg, mgr)
		if err != nil {
			adapterCancel()
			return err
		}
		start(adapter)
	case PlatformNostr:
		adapter, err := newNostrAdapter(name, cfg, adapterCfg, mgr)
		if err != nil {
			adapterCancel()
			return err
		}
		start(adapter)
	case PlatformTwitch:
		adapter, err := newTwitchAdapter(name, cfg, adapterCfg, mgr)
		if err != nil {
			adapterCancel()
			return err
		}
		start(adapter)
	case PlatformWhatsApp:
		adapter, err := newWhatsAppAdapter(name, cfg, adapterCfg, mgr)
		if err != nil {
			adapterCancel()
			return err
		}
		start(adapter)
	}
	return nil
}

// StartPCAdapterOnly starts only the built-in PrivateClaw adapter.
// Used when IM is not explicitly enabled but PC should still be available.
func StartPCAdapterOnly(parent context.Context, cfg config.IMConfig, mgr *Manager) (*AdapterController, error) {
	if mgr == nil {
		return nil, fmt.Errorf("IM manager is nil")
	}
	ctx, cancel := context.WithCancel(parent)
	startPCAdapter(ctx, cfg, mgr)
	return &AdapterController{cancel: cancel}, nil
}

// newDefaultPCSessionStore creates a JSONFilePCSessionStore at the default path.
func newDefaultPCSessionStore() PCSessionStore {
	storePath, err := DefaultPCSessionStorePath()
	if err != nil {
		debug.Log("pc", "resolve session store path: %v", err)
		return NewMemoryPCSessionStore()
	}
	store, err := NewJSONFilePCSessionStore(storePath)
	if err != nil {
		debug.Log("pc", "create session store: %v", err)
		return NewMemoryPCSessionStore()
	}
	return store
}

// startPCAdapter starts the built-in PrivateClaw adapter with defaults.
// It uses config from im.adapters if a PrivateClaw entry exists, otherwise uses defaults.
func startPCAdapter(ctx context.Context, cfg config.IMConfig, mgr *Manager) {
	// Check if a PC adapter was already started via explicit config
	for name, adapterCfg := range cfg.Adapters {
		if adapterCfg.Enabled && strings.EqualFold(adapterCfg.Platform, string(PlatformPrivateClaw)) {
			debug.Log("pc", "skip auto-start: explicit config %q already present", name)
			return
		}
	}

	// Build a default adapter config
	defaultCfg := config.IMAdapterConfig{
		Enabled:  true,
		Platform: string(PlatformPrivateClaw),
	}
	sessionStore := newDefaultPCSessionStore()
	adapter, err := newPCAdapter("_pc_builtin", cfg, defaultCfg, mgr, sessionStore)
	if err != nil {
		debug.Log("pc", "auto-start failed: %v", err)
		return
	}
	adapterCtx, adapterCancel := context.WithCancel(ctx)
	mgr.RegisterAdapterCancel("_pc_builtin", adapterCancel)
	mgr.RegisterSink(adapter)
	adapter.Start(adapterCtx)
	debug.Log("pc", "auto-started _pc_builtin, sinks=%d", len(mgr.Snapshot().Adapters))
}
