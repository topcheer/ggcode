package tui

import (
	"context"
	"errors"
	"fmt"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/im"
)

func (m *Model) ensureCurrentWorkspaceIMManager(unavailableErr, disabledErr string, autoEnable bool) error {
	if m.imManager != nil {
		return nil
	}
	if m.config == nil {
		return errors.New(unavailableErr)
	}
	if !m.config.IM.Enabled {
		if !autoEnable {
			if disabledErr != "" {
				return fmt.Errorf("%s", disabledErr)
			}
			return errors.New(unavailableErr)
		}
		m.config.IM.Enabled = true
		if err := m.saveConfig(); err != nil {
			return fmt.Errorf("enable IM runtime: %w", err)
		}
	}

	adapters := make(map[string]bool)
	for name, acfg := range m.config.IM.Adapters {
		adapters[name] = acfg.Enabled
	}
	runtimeInit, err := im.InitRuntime(im.RuntimeInitOptions{
		Workspace:        m.currentWorkspacePath(),
		EnabledAdapters:  adapters,
		RegisterInstance: m.currentWorkspacePath() != "",
	})
	if err != nil {
		return fmt.Errorf("initializing IM runtime: %w", err)
	}
	m.SetIMManager(runtimeInit.Manager)
	return nil
}

type imEnsureGuard struct {
	mu       sync.Mutex
	starting bool
}

func (m *Model) ensureStartedCurrentWorkspaceIMRuntime(unavailableErr, disabledErr string, autoEnable bool) error {
	// #1379-A: a failed adapter start used to leave imManager set, so
	// every later call returned nil at the top guard - fake success,
	// never retried, SetBridge skipped, only a process restart recovered.
	// Roll the manager back on failure so the next call retries the whole
	// chain. #1379-D: a starting flag collapses concurrent Cmd goroutines
	// into one InitRuntime+Start (duplicate instance registrations
	// otherwise).
	if m.imEnsure == nil {
		m.imEnsure = &imEnsureGuard{}
	}
	m.imEnsure.mu.Lock()
	if m.imManager != nil || m.imEnsure.starting {
		// starting: another goroutine is mid-start; its result applies.
		m.imEnsure.mu.Unlock()
		return nil
	}
	m.imEnsure.starting = true
	m.imEnsure.mu.Unlock()
	defer func() {
		m.imEnsure.mu.Lock()
		m.imEnsure.starting = false
		m.imEnsure.mu.Unlock()
	}()

	if err := m.ensureCurrentWorkspaceIMManager(unavailableErr, disabledErr, autoEnable); err != nil {
		return err
	}
	if m.config == nil || m.imManager == nil {
		return nil
	}
	if _, err := im.StartCurrentBindingAdapter(context.Background(), m.config.IM, m.imManager); err != nil {
		// Roll back: without this the next call sees imManager != nil and
		// short-circuits to fake success forever.
		m.SetIMManager(nil)
		return fmt.Errorf("starting current workspace IM adapter: %w", err)
	}
	m.imManager.SetBridge(newTUIIMBridge(func() *tea.Program { return m.program }))
	return nil
}
