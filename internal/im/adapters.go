package im

import (
	"context"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/config"
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

	binding := mgr.CurrentBinding()
	if binding == nil || strings.TrimSpace(binding.Adapter) == "" {
		return controller, nil
	}
	adapterCfg, ok := cfg.Adapters[binding.Adapter]
	if !ok || !adapterCfg.Enabled {
		return controller, nil
	}
	if err := startConfiguredAdapter(ctx, cfg, binding.Adapter, adapterCfg, mgr); err != nil {
		cancel()
		return nil, err
	}
	return controller, nil
}

func StartNamedAdapter(parent context.Context, cfg config.IMConfig, name string, mgr *Manager) error {
	if mgr == nil {
		return fmt.Errorf("IM manager is nil")
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
	switch Platform(strings.TrimSpace(adapterCfg.Platform)) {
	case PlatformQQ:
		adapter, err := newQQAdapter(name, cfg, adapterCfg, mgr)
		if err != nil {
			return err
		}
		mgr.RegisterSink(adapter)
		adapter.Start(ctx)
	}
	return nil
}
