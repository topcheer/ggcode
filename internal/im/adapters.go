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
		if skipBindingStart(binding, mgr) {
			continue
		}
		// Built-in PC adapter — only start when binding explicitly targets it.
		if isPCBindingAdapter(binding.Adapter) {
			startPCBinding(ctx, cfg, binding, mgr)
			continue
		}
		if err := startBindingAdapter(ctx, cfg, binding.Adapter, mgr); err != nil {
			cancel()
			return nil, err
		}
	}
	return controller, nil
}

// bindingSessionOwnedElsewhere is the pure session-ownership predicate behind
// skipBindingStart: a persisted LastSessionID that doesn't match our (possibly
// empty) session id means another instance owns the binding. Bindings with an
// empty LastSessionID use the old logic (first process claims all) and never
// take this path.
func bindingSessionOwnedElsewhere(lastSessionID, currentSessionID string) bool {
	return currentSessionID == "" || lastSessionID != currentSessionID
}

// skipBindingStart reports whether the binding must not be started, keeping
// the original guard order and per-reason diagnostics: a silent skip for an
// empty adapter name, a logged skip for muted bindings (muted adapters must
// never start connections), and a logged skip for a binding owned by another
// session. The session lookup only happens for bindings that carry a
// LastSessionID, matching the previous inline control flow.
func skipBindingStart(binding ChannelBinding, mgr *Manager) bool {
	if strings.TrimSpace(binding.Adapter) == "" {
		return true
	}
	if binding.Muted {
		debug.Log("im", "StartCurrentBindingAdapter: skipping muted adapter %q", binding.Adapter)
		return true
	}
	if binding.LastSessionID != "" {
		sessionID := mgr.CurrentSessionID()
		if bindingSessionOwnedElsewhere(binding.LastSessionID, sessionID) {
			debug.Log("im", "StartCurrentBindingAdapter: skipping adapter %q — owned by session %s, ours=%s",
				binding.Adapter, binding.LastSessionID, sessionID)
			return true
		}
	}
	return false
}

// isPCBindingAdapter reports whether the binding targets the built-in
// PrivateClaw adapter: either the reserved _pc_builtin auto binding name or a
// binding matching the platform name. #1543: the platform-name arm exists so
// a binding literally NAMED "privateclaw" (UI default names bindings after
// the platform) takes the PC branch instead of falling through to
// startConfiguredAdapter.
func isPCBindingAdapter(adapter string) bool {
	return adapter == "_pc_builtin" || strings.EqualFold(adapter, string(PlatformPrivateClaw))
}

// startPCBinding starts the built-in PrivateClaw adapter for a PC-targeted
// binding. The reserved _pc_builtin binding always auto-starts with defaults;
// a user-named binding is treated like any explicit config entry — it starts
// only when an enabled config entry exists, and its start errors are logged
// rather than fatal for sibling bindings.
func startPCBinding(ctx context.Context, cfg config.IMConfig, binding ChannelBinding, mgr *Manager) {
	if binding.Adapter == "_pc_builtin" {
		startPCAdapter(ctx, cfg, mgr)
		return
	}
	// User-named binding: treat like any explicit config entry.
	if adapterCfg, ok := cfg.Adapters[binding.Adapter]; ok && adapterCfg.Enabled {
		if err := startConfiguredAdapter(ctx, cfg, binding.Adapter, adapterCfg, mgr); err != nil {
			debug.Log("im", "binding adapter %q: %v", binding.Adapter, err)
		}
	}
}

// startBindingAdapter resolves the binding's adapter config and starts it.
// A missing or disabled config entry is a silent no-op (nil error); a
// construction/start error is fatal for the whole restore path — the caller
// cancels the controller and aborts.
func startBindingAdapter(ctx context.Context, cfg config.IMConfig, name string, mgr *Manager) error {
	adapterCfg, ok := cfg.Adapters[name]
	if !ok || !adapterCfg.Enabled {
		return nil
	}
	return startConfiguredAdapter(ctx, cfg, name, adapterCfg, mgr)
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

// adapterBuilder constructs an adapter for the already-resolved start inputs.
// A constructor error aborts only this adapter's start; sibling adapters in
// the same config are unaffected.
type adapterBuilder func() (startableSink, error)

// canonicalPlatform normalizes a configured platform string to the canonical
// registry ID (see #736): surrounding whitespace is trimmed, case is
// preserved so legacy non-canonical spellings (e.g. "Telegram") stay
// unrecognized and take the skip path.
func canonicalPlatform(raw string) Platform {
	return Platform(strings.TrimSpace(raw))
}

// adapterBuilders maps every canonical platform to its constructor, adapted
// to the uniform adapterBuilder signature. It is pure: building the map has
// no side effects (constructors run only when the selected builder is
// invoked), so dispatch order and per-platform argument wiring stay
// equivalent to the previous inline switch. PlatformSynology deliberately has
// no entry: it has no constructor today and keeps taking the unknown-platform
// skip path.
func adapterBuilders(name string, cfg config.IMConfig, adapterCfg config.IMAdapterConfig, mgr *Manager) map[Platform]adapterBuilder {
	return map[Platform]adapterBuilder{
		PlatformQQ: func() (startableSink, error) {
			return newQQAdapter(name, cfg, adapterCfg, mgr)
		},
		PlatformTelegram: func() (startableSink, error) {
			return newTGAdapter(name, cfg, adapterCfg, mgr)
		},
		PlatformPrivateClaw: func() (startableSink, error) {
			// The PC session store is resolved lazily, right before the
			// constructor runs, mirroring the previous inline ordering.
			return newPCAdapter(name, cfg, adapterCfg, mgr, newDefaultPCSessionStore())
		},
		PlatformDiscord: func() (startableSink, error) {
			return newDiscordAdapter(name, cfg, adapterCfg, mgr)
		},
		PlatformFeishu: func() (startableSink, error) {
			return newFeishuAdapter(name, cfg, adapterCfg, mgr)
		},
		PlatformDingTalk: func() (startableSink, error) {
			return newDingtalkAdapter(name, mgr, adapterCfg)
		},
		PlatformSlack: func() (startableSink, error) {
			return newSlackAdapter(name, cfg, adapterCfg, mgr)
		},
		PlatformDummy: func() (startableSink, error) {
			return newDummyAdapter(name, cfg, adapterCfg, mgr), nil
		},
		PlatformWechat: func() (startableSink, error) {
			return newWechatAdapter(name, cfg, adapterCfg, mgr)
		},
		PlatformWeCom: func() (startableSink, error) {
			return newWeComAdapter(name, cfg, adapterCfg, mgr)
		},
		PlatformMattermost: func() (startableSink, error) {
			return newMattermostAdapter(name, cfg, adapterCfg, mgr)
		},
		PlatformMatrix: func() (startableSink, error) {
			return newMatrixAdapter(name, cfg, adapterCfg, mgr)
		},
		PlatformSignal: func() (startableSink, error) {
			return newSignalAdapter(name, cfg, adapterCfg, mgr)
		},
		PlatformIRC: func() (startableSink, error) {
			return newIRCAdapter(name, cfg, adapterCfg, mgr)
		},
		PlatformNostr: func() (startableSink, error) {
			return newNostrAdapter(name, cfg, adapterCfg, mgr)
		},
		PlatformTwitch: func() (startableSink, error) {
			return newTwitchAdapter(name, cfg, adapterCfg, mgr)
		},
		PlatformWhatsApp: func() (startableSink, error) {
			return newWhatsAppAdapter(name, cfg, adapterCfg, mgr)
		},
	}
}

func startConfiguredAdapter(ctx context.Context, cfg config.IMConfig, name string, adapterCfg config.IMAdapterConfig, mgr *Manager) error {
	if !adapterCfg.Enabled {
		return nil
	}
	// Create a child context so Manager can cancel this adapter individually.
	adapterCtx, adapterCancel := context.WithCancel(ctx)
	mgr.RegisterAdapterCancel(name, adapterCancel)

	build, known := adapterBuilders(name, cfg, adapterCfg, mgr)[canonicalPlatform(adapterCfg.Platform)]
	if !known {
		// #736: an unrecognized platform previously fell through silently -
		// no error, no log, the adapter just never started (a diagnostic
		// black hole for legacy non-canonical configs like platform:
		// "Telegram"). Leave a traceable log line. We intentionally do NOT
		// return an error: one bad adapter must not block the others.
		adapterCancel()
		debug.Log("im", "adapter %q: unknown platform %q (expected canonical registry ID e.g. \"telegram\"), skipping start", name, adapterCfg.Platform)
		return nil
	}

	adapter, err := build()
	if err != nil {
		adapterCancel()
		return err
	}
	mgr.RegisterSink(adapter)
	adapter.Start(adapterCtx)
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
