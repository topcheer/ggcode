package tui

import (
	"fmt"
	"strings"
)

func (m *Model) ExecuteRemoteSlashCommand(text string) (string, bool) {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) == 0 || !strings.HasPrefix(parts[0], "/") {
		return "", false
	}
	switch strings.ToLower(parts[0]) {
	case "/provider":
		return m.executeRemoteProviderCommand(parts), true
	case "/model":
		return m.executeRemoteModelCommand(parts), true
	default:
		return "", false
	}
}

func (m *Model) executeRemoteProviderCommand(parts []string) string {
	if m.config == nil {
		return "provider switching is unavailable without config"
	}
	if len(parts) == 1 {
		return m.providerCommandSummary()
	}
	vendor := parts[1]
	endpoints := m.config.EndpointNames(vendor)
	if len(endpoints) == 0 {
		return m.t("command.provider_unknown", vendor, m.vendorNames())
	}
	endpoint := endpoints[0]
	if len(parts) > 2 {
		endpoint = parts[2]
	}
	if err := m.config.SetActiveSelection(vendor, endpoint, ""); err != nil {
		return m.t("command.provider_failed", err)
	}
	if err := m.reloadActiveProvider(); err != nil {
		return m.t("command.provider_failed", err)
	}
	return m.t("command.provider_switched", vendor, m.config.Model)
}

func (m *Model) executeRemoteModelCommand(parts []string) string {
	if m.config == nil {
		return "model switching is unavailable without config"
	}
	if len(parts) == 1 {
		return m.modelCommandSummary()
	}
	model := parts[1]
	if err := m.config.SetActiveSelection(m.config.Vendor, m.config.Endpoint, model); err != nil {
		return m.t("command.model_failed", err)
	}
	if err := m.reloadActiveProvider(); err != nil {
		return m.t("command.model_failed", err)
	}
	return m.t("command.model_switched", model, m.config.Vendor)
}

func (m *Model) providerCommandSummary() string {
	if m.config == nil {
		return ""
	}
	resolved, err := m.config.ResolveActiveEndpoint()
	if err != nil {
		return m.t("command.provider_failed", err)
	}
	return m.t(
		"command.provider_current",
		resolved.VendorName,
		resolved.EndpointName,
		resolved.Model,
		m.vendorNames(),
		strings.Join(m.config.EndpointNames(m.config.Vendor), ", "),
	)
}

func (m *Model) modelCommandSummary() string {
	if m.config == nil {
		return ""
	}
	resolved, err := m.config.ResolveActiveEndpoint()
	if err != nil {
		return m.t("command.model_failed", err)
	}
	models := uniqueStrings(append([]string(nil), resolved.Models...))
	if len(models) == 0 && strings.TrimSpace(resolved.Model) != "" {
		models = []string{resolved.Model}
	}
	return m.t("command.model_current", resolved.Model, resolved.VendorName, strings.Join(models, ", "))
}

func (m *Model) remoteSwitchChoices() string {
	return fmt.Sprintf("%s%s", m.providerCommandSummary(), m.modelCommandSummary())
}
