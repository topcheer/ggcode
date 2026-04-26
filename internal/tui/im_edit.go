package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/im"
)

// imAdapterEditMode represents the current editing sub-state.
type imAdapterEditMode int

const (
	imEditNone   imAdapterEditMode = iota // not editing
	imEditSelect                          // selecting a field to edit
	imEditInput                           // typing a new value
)

// imAdapterEditState is shared state for editing an adapter's Extra fields.
// Each platform panel embeds this and delegates edit key/render calls.
type imAdapterEditState struct {
	mode          imAdapterEditMode
	adapterName   string
	fieldKeys     []string          // sorted extra field names
	fieldValues   map[string]string // current display values (masked for secrets)
	editSelected  int               // selected field index in editSelect mode
	editField     string            // field being edited in editInput mode
	editInput     string            // current input buffer
	editMessage   string            // success/error message
	originalExtra map[string]string // original (unmasked) values for non-secret fields
}

// imEditResultMsg is the tea.Msg returned after saving an edited field.
type imEditResultMsg struct {
	adapterName string
	field       string
	value       string
	err         error
}

// resetIMEdit resets the edit state.
func (s *imAdapterEditState) resetIMEdit() {
	s.mode = imEditNone
	s.adapterName = ""
	s.fieldKeys = nil
	s.fieldValues = nil
	s.editSelected = 0
	s.editField = ""
	s.editInput = ""
	s.editMessage = ""
	s.originalExtra = nil
}

// enterIMEditSelect populates the edit state from the adapter config.
func (m *Model) enterIMEditSelect(adapterName string) imAdapterEditState {
	s := imAdapterEditState{
		mode:          imEditSelect,
		adapterName:   adapterName,
		fieldValues:   make(map[string]string),
		originalExtra: make(map[string]string),
	}
	if m.config == nil {
		return s
	}
	adapter, ok := m.config.IM.Adapters[adapterName]
	if !ok {
		return s
	}
	for k, v := range adapter.Extra {
		val := fmt.Sprintf("%v", v)
		s.originalExtra[k] = val
		if looksLikeSecretField(k) {
			s.fieldValues[k] = maskSecret(val)
		} else {
			s.fieldValues[k] = val
		}
	}
	// Also add Env fields
	for k, v := range adapter.Env {
		key := "env." + k
		s.originalExtra[key] = v
		s.fieldValues[key] = v
	}

	keys := make([]string, 0, len(s.fieldValues))
	for k := range s.fieldValues {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s.fieldKeys = keys
	return s
}

// renderIMEditSelect renders the field selection view.
func (m *Model) renderIMEditSelect(s *imAdapterEditState) string {
	if s == nil || s.mode != imEditSelect {
		return ""
	}
	body := []string{
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.im.edit.title")),
		fmt.Sprintf(" %s", m.t("panel.im.edit.adapter", s.adapterName)),
		"",
	}
	if len(s.fieldKeys) == 0 {
		body = append(body, fmt.Sprintf(" %s", m.t("panel.im.edit.no_fields")))
	} else {
		selected := s.editSelected
		if selected >= len(s.fieldKeys) {
			selected = len(s.fieldKeys) - 1
		}
		if selected < 0 {
			selected = 0
		}
		for i, key := range s.fieldKeys {
			val := s.fieldValues[key]
			if i == selected {
				body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true).Render(
					fmt.Sprintf(" ▸ %s: %s", key, val)))
			} else {
				body = append(body, fmt.Sprintf("   %s: %s", key, val))
			}
		}
		body = append(body, "",
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.im.edit.select_hint")))
	}
	body = append(body, "",
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.im.edit.add_hint")))
	if s.editMessage != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(s.editMessage))
	}
	return strings.Join(body, "\n")
}

// renderIMEditInput renders the text input view for editing a field value.
func (m *Model) renderIMEditInput(s *imAdapterEditState) string {
	if s == nil || s.mode != imEditInput {
		return ""
	}
	body := []string{
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.im.edit.title")),
		fmt.Sprintf(" %s", m.t("panel.im.edit.adapter", s.adapterName)),
		"",
		fmt.Sprintf(" %s", m.t("panel.im.edit.field", s.editField)),
		fmt.Sprintf(" %s%s█", m.t("panel.im.edit.new_value"), s.editInput),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" " + m.t("panel.im.edit.input_hint")),
	}
	if s.editMessage != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(s.editMessage))
	}
	return strings.Join(body, "\n")
}

// handleIMEditKey handles key events for the adapter edit mode.
// Returns the updated edit state and a tea.Cmd.
func (m *Model) handleIMEditKey(s *imAdapterEditState, msg tea.KeyPressMsg) (*imAdapterEditState, tea.Cmd) {
	if s == nil || s.mode == imEditNone {
		return s, nil
	}

	switch s.mode {
	case imEditSelect:
		return m.handleIMEditSelectKey(s, msg)
	case imEditInput:
		return m.handleIMEditInputKey(s, msg)
	}
	return s, nil
}

func (m *Model) handleIMEditSelectKey(s *imAdapterEditState, msg tea.KeyPressMsg) (*imAdapterEditState, tea.Cmd) {
	switch msg.String() {
	case "esc":
		s.resetIMEdit()
		return s, nil
	case "up", "k":
		if len(s.fieldKeys) > 0 {
			s.editSelected = (s.editSelected - 1 + len(s.fieldKeys)) % len(s.fieldKeys)
		}
		s.editMessage = ""
		return s, nil
	case "down", "j":
		if len(s.fieldKeys) > 0 {
			s.editSelected = (s.editSelected + 1) % len(s.fieldKeys)
		}
		s.editMessage = ""
		return s, nil
	case "enter":
		if len(s.fieldKeys) == 0 {
			s.editMessage = m.t("panel.im.edit.no_fields")
			return s, nil
		}
		selected := s.editSelected
		if selected >= len(s.fieldKeys) {
			selected = len(s.fieldKeys) - 1
		}
		field := s.fieldKeys[selected]
		s.mode = imEditInput
		s.editField = field
		s.editInput = s.originalExtra[field]
		s.editMessage = ""
		return s, nil
	case "n", "N":
		// Add new field: enter input mode with empty field name prompt
		s.mode = imEditInput
		s.editField = ""
		s.editInput = ""
		s.editMessage = m.t("panel.im.edit.enter_field_name")
		return s, nil
	}
	return s, nil
}

func (m *Model) handleIMEditInputKey(s *imAdapterEditState, msg tea.KeyPressMsg) (*imAdapterEditState, tea.Cmd) {
	switch msg.String() {
	case "esc":
		s.mode = imEditSelect
		s.editField = ""
		s.editInput = ""
		s.editMessage = ""
		return s, nil
	case "enter":
		input := strings.TrimSpace(s.editInput)
		if s.editField == "" {
			// New field mode: input is "field_name=value"
			parts := strings.SplitN(input, "=", 2)
			if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
				s.editMessage = m.t("panel.im.edit.invalid_format")
				return s, nil
			}
			fieldName := strings.TrimSpace(parts[0])
			fieldValue := parts[1]
			s.editField = fieldName
			s.editInput = fieldValue
			input = fieldValue
		}
		if input == "" {
			s.editMessage = m.t("panel.im.edit.empty_value")
			return s, nil
		}
		adapterName := s.adapterName
		field := s.editField
		// Return to select mode immediately; result comes via imEditResultMsg
		s.mode = imEditSelect
		s.editField = ""
		s.editInput = ""
		s.editMessage = m.t("panel.im.edit.saving")
		return s, m.saveIMEditField(adapterName, field, input)
	case "backspace":
		runes := []rune(s.editInput)
		if len(runes) > 0 {
			s.editInput = string(runes[:len(runes)-1])
		}
		return s, nil
	case "space", " ":
		s.editInput += " "
		return s, nil
	}
	if len(msg.Text) > 0 {
		s.editInput += msg.Text
	}
	return s, nil
}

// saveIMEditField creates a tea.Cmd that persists an edited field,
// writes the secret to keys.env, and hot-reloads the adapter.
func (m *Model) saveIMEditField(adapterName, field, value string) tea.Cmd {
	return func() tea.Msg {
		if m.config == nil {
			return imEditResultMsg{adapterName: adapterName, field: field, err: errors.New("config unavailable")}
		}
		// 1. Persist to config struct + YAML + keys.env (via Save's post-migration).
		if err := m.config.SetIMAdapterExtra(adapterName, field, value); err != nil {
			return imEditResultMsg{adapterName: adapterName, field: field, err: err}
		}
		// 2. Hot-reload: if the adapter is running, stop it and restart with new config.
		if m.imManager != nil && m.isAdapterRunning(adapterName) {
			m.imManager.StopAdapter(adapterName)
			_ = im.StartNamedAdapter(context.Background(), m.config.IM, adapterName, m.imManager)
		}
		return imEditResultMsg{adapterName: adapterName, field: field, value: value}
	}
}

// isAdapterRunning checks if an adapter is currently active in the IM manager.
func (m *Model) isAdapterRunning(name string) bool {
	if m.imManager == nil {
		return false
	}
	for _, state := range m.imManager.Snapshot().Adapters {
		if state.Name == name {
			return true
		}
	}
	return false
}

// applyIMEditResult applies an imEditResultMsg to the edit state and refreshes it.
func (m *Model) applyIMEditResult(s *imAdapterEditState, msg imEditResultMsg) {
	if s == nil || s.adapterName != msg.adapterName {
		return
	}
	if msg.err != nil {
		s.editMessage = fmt.Sprintf("%s: %s", m.t("panel.im.edit.error"), msg.err.Error())
		return
	}
	s.editMessage = m.t("panel.im.edit.saved", msg.field)
	// Refresh the field list from config
	refreshed := m.enterIMEditSelect(s.adapterName)
	// Preserve the mode and selected index
	refreshed.mode = imEditSelect
	refreshed.editMessage = s.editMessage
	*s = refreshed
}

// isSecretKey checks if a key name looks like a secret field.
// Reuses the config package's looksLikeSecretField naming convention.
func isSecretKey(key string) bool {
	return looksLikeSecretField(key)
}

// maskSecret masks a secret value, showing only the last 4 characters.
func maskSecret(value string) string {
	runes := []rune(value)
	if len(runes) <= 4 {
		return "****"
	}
	return "****" + string(runes[len(runes)-4:])
}

// looksLikeSecretField checks if a key name suggests it holds a secret.
// Duplicated from config package to avoid import cycle concerns.
func looksLikeSecretField(key string) bool {
	lower := strings.ToLower(key)
	for _, pattern := range []string{"secret", "token", "password", "credential"} {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}
