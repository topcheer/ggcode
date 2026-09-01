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
	// #1417-A: the starter publishes its result here and closes done -
	// concurrent waiters block on done and get the REAL outcome instead
	// of an unconditional nil (fake success while the start later fails
	// was exactly what #1379 set out to kill; callers like qq_panel then
	// nil-deref imManager on their next line).
	done chan struct{}
	err  error
}

func (m *Model) ensureStartedCurrentWorkspaceIMRuntime(unavailableErr, disabledErr string, autoEnable bool) error {
	// #1379-A: a failed adapter start used to leave imManager set, so
	// every later call returned nil at the top guard - fake success,
	// never retried, SetBridge skipped, only a process restart recovered.
	// Roll the manager back on failure so the next call retries the whole
	// chain. #1379-D: a starting flag collapses concurrent Cmd goroutines
	// into one InitRuntime+Start (duplicate instance registrations
	// otherwise). #1417-A: waiters used to return nil immediately while the
	// starter was mid-flight - they now block on done and observe the
	// starter's real result.
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
		// The starter's outcome is fully applied (imManager set on success,
		// rolled back on failure); report it - including its error.
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

// startIMRuntimeChain runs the actual init+start sequence. #1417-B: on
// failure the rollback now stops the binding watcher and unregisters the
// instance record InitRuntime created - previously only the pointer was
// cleared, leaking one watcher goroutine plus a stale on-disk instance
// record per failed attempt (phantom entries pollute other instances'
// OtherInstances / auto-mute detection). #1417-C: a bounded context - a
// future blocking constructor would otherwise leave 'starting' true
// forever (permanent fake success for waiters, unrecoverable without a
// restart).
func (m *Model) startIMRuntimeChain(unavailableErr, disabledErr string, autoEnable bool) error {
	if err := m.ensureCurrentWorkspaceIMManager(unavailableErr, disabledErr, autoEnable); err != nil {
		return err
	}
	if m.config == nil || m.imManager == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := im.StartCurrentBindingAdapter(ctx, m.config.IM, m.imManager); err != nil {
		// Roll back: without this the next call sees imManager != nil and
		// short-circuits to fake success forever (#1379).
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
