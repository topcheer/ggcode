package tui

import (
	"context"
	"errors"
	"fmt"

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

func (m *Model) ensureStartedCurrentWorkspaceIMRuntime(unavailableErr, disabledErr string, autoEnable bool) error {
	if m.imManager != nil {
		return nil
	}
	if err := m.ensureCurrentWorkspaceIMManager(unavailableErr, disabledErr, autoEnable); err != nil {
		return err
	}
	if m.config == nil || m.imManager == nil {
		return nil
	}
	if _, err := im.StartCurrentBindingAdapter(context.Background(), m.config.IM, m.imManager); err != nil {
		return fmt.Errorf("starting current workspace IM adapter: %w", err)
	}
	m.imManager.SetBridge(newTUIIMBridge(func() *tea.Program { return m.program }))
	return nil
}
