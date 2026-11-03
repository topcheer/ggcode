package tui

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/im"
)

func (m *Model) ensureCurrentWorkspaceIMManager(unavailableErr, disabledErr string, autoEnable bool) error {

	if m.imEnsure == nil {
		m.imEnsure = &imEnsureGuard{}
	}
	m.imEnsure.mu.Lock()
	defer m.imEnsure.mu.Unlock()
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
	for name, acfg := range m.config.IMSnapshot().Adapters {
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

	done chan struct{}
	err  error
}

func (m *Model) ensureStartedCurrentWorkspaceIMRuntime(unavailableErr, disabledErr string, autoEnable bool) error {

	if m.imEnsure == nil {
		m.imEnsure = &imEnsureGuard{}
	}
	m.imEnsure.mu.Lock()
	if m.imManager != nil {
		m.imEnsure.mu.Unlock()
		return nil
	}
	if m.imEnsure.starting {
		done := m.imEnsure.done
		m.imEnsure.mu.Unlock()
		<-done

		m.imEnsure.mu.Lock()
		err := m.imEnsure.err
		m.imEnsure.mu.Unlock()
		return err
	}
	m.imEnsure.starting = true
	m.imEnsure.done = make(chan struct{})
	m.imEnsure.mu.Unlock()
	var startErr error
	defer func() {
		m.imEnsure.mu.Lock()
		m.imEnsure.starting = false
		m.imEnsure.err = startErr
		close(m.imEnsure.done)
		m.imEnsure.mu.Unlock()
	}()
	startErr = m.startIMRuntimeChain(unavailableErr, disabledErr, autoEnable)
	return startErr
}

func (m *Model) startIMRuntimeChain(unavailableErr, disabledErr string, autoEnable bool) error {
	if err := m.ensureCurrentWorkspaceIMManager(unavailableErr, disabledErr, autoEnable); err != nil {
		return err
	}
	if m.config == nil || m.imManager == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := im.StartCurrentBindingAdapter(ctx, m.config.IMSnapshot(), m.imManager); err != nil {

		mgr := m.imManager
		m.SetIMManager(nil)
		if mgr != nil {
			mgr.StopBindingWatcher()
			mgr.UnregisterInstance()
		}
		return fmt.Errorf("starting current workspace IM adapter: %w", err)
	}
	m.imManager.SetBridge(newTUIIMBridge(func() *tea.Program { return m.program }))
	return nil
}
