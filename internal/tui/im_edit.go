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
	"github.com/topcheer/ggcode/internal/secretfield"
)

type imAdapterEditMode int

const (
	imEditNone imAdapterEditMode = iota
	imEditSelect
	imEditInput
)

type imAdapterEditState struct {
	mode          imAdapterEditMode
	adapterName   string
	fieldKeys     []string
	fieldValues   map[string]string
	editSelected  int
	editField     string
	editInput     string
	editMessage   string
	originalExtra map[string]string
}

type imEditResultMsg struct {
	adapterName string
	field       string
	value       string
	err         error
}

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
	adapter, ok := m.config.GetIMAdapter(adapterName)
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

	for k, v := range adapter.Env {
		key := "env." + k
		s.originalExtra[key] = v

		if looksLikeSecretField(k) {
			s.fieldValues[key] = maskSecret(v)
		} else {
			s.fieldValues[key] = v
		}
	}

	keys := make([]string, 0, len(s.fieldValues))
	for k := range s.fieldValues {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s.fieldKeys = keys
	return s
}

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

func maskedEditValue(field, value string) string {
	name := strings.TrimPrefix(field, "env.")
	if looksLikeSecretField(name) {
		return maskSecret(value)
	}
	return value
}

func maskedNewFieldEcho(input string) string {
	eq := strings.Index(input, "=")
	if eq < 0 {
		return input
	}
	name, value := input[:eq], input[eq+1:]
	if looksLikeSecretField(strings.TrimSpace(name)) {
		return name + "=" + maskSecret(value)
	}
	return input
}

func imEditEcho(s *imAdapterEditState) string {
	if s.editField == "" {
		return maskedNewFieldEcho(s.editInput)
	}
	return maskedEditValue(s.editField, s.editInput)
}

func (m *Model) renderIMEditInput(s *imAdapterEditState) string {
	if s == nil || s.mode != imEditInput {
		return ""
	}
	body := []string{
		lipgloss.NewStyle().Bold(true).Render(m.t("panel.im.edit.title")),
		fmt.Sprintf(" %s", m.t("panel.im.edit.adapter", s.adapterName)),
		"",
		fmt.Sprintf(" %s", m.t("panel.im.edit.field", s.editField)),
		fmt.Sprintf(" %s%s█", m.t("panel.im.edit.new_value"), imEditEcho(s)),
		"",
		renderPasteShortcutHint(m.currentLanguage()),
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" " + m.t("panel.im.edit.input_hint")),
	}
	if s.editMessage != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(s.editMessage))
	}
	return strings.Join(body, "\n")
}

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

func (m *Model) saveIMEditField(adapterName, field, value string) tea.Cmd {
	return func() tea.Msg {
		if m.config == nil {
			return imEditResultMsg{adapterName: adapterName, field: field, err: errors.New("config unavailable")}
		}

		return configMutationMsg{
			apply: func(m *Model) error {
				if strings.HasPrefix(field, "env.") {
					if err := m.config.SetIMAdapterEnv(adapterName, strings.TrimPrefix(field, "env."), value); err != nil {
						return err
					}
				} else if err := m.config.SetIMAdapterExtra(adapterName, field, value); err != nil {
					return err
				}
				return nil
			},
			next: func(m *Model) tea.Cmd {
				return func() tea.Msg {

					if m.imManager != nil && m.isAdapterRunning(adapterName) {
						m.imManager.StopAdapter(adapterName)
						if err := im.StartNamedAdapter(context.Background(), m.config.IMSnapshot(), adapterName, m.imManager); err != nil {
							return imEditResultMsg{adapterName: adapterName, field: field, err: fmt.Errorf("saved, but adapter restart failed: %w", err)}
						}
					}
					return imEditResultMsg{adapterName: adapterName, field: field, value: value}
				}
			},
			fail: func(err error) tea.Msg {
				return imEditResultMsg{adapterName: adapterName, field: field, err: err}
			},
		}
	}
}

func (m *Model) isAdapterRunning(name string) bool {
	if m.imManager == nil {
		return false
	}
	for _, state := range m.imManager.Snapshot().Adapters {
		if state.Name == name {

			return state.Status != "disconnected"
		}
	}
	return false
}

func (m *Model) applyIMEditResult(s *imAdapterEditState, msg imEditResultMsg) {
	if s == nil || s.adapterName != msg.adapterName {
		return
	}
	if msg.err != nil {
		s.editMessage = fmt.Sprintf("%s: %s", m.t("panel.im.edit.error"), msg.err.Error())
		return
	}
	s.editMessage = m.t("panel.im.edit.saved", msg.field)

	refreshed := m.enterIMEditSelect(s.adapterName)

	refreshed.mode = imEditSelect
	refreshed.editMessage = s.editMessage
	refreshed.editSelected = s.editSelected
	*s = refreshed
}

func maskSecret(value string) string {
	runes := []rune(value)
	if len(runes) <= 4 {
		return "****"
	}
	return "****" + string(runes[len(runes)-4:])
}

func maskPositionalCreateEcho(input string, secretFrom int) string {
	fields := strings.Fields(input)
	if secretFrom < 0 {
		secretFrom = 0
	}
	if len(fields) <= secretFrom {
		return input
	}
	masked := append([]string{}, fields[:secretFrom]...)
	for _, f := range fields[secretFrom:] {
		masked = append(masked, maskSecret(f))
	}
	return strings.Join(masked, " ")
}

func maskCreateEchoIndices(input string, indices ...int) string {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return input
	}
	mask := make(map[int]bool, len(indices))
	for _, i := range indices {
		mask[i] = true
	}
	masked := append([]string{}, fields...)
	for i := range masked {
		if mask[i] {
			masked[i] = maskSecret(masked[i])
		}
	}
	return strings.Join(masked, " ")
}

func looksLikeSecretField(key string) bool {

	return secretfield.LooksLikeSecretField(key)
}
