package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	appconfig "github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/harness"
	"github.com/topcheer/ggcode/internal/provider"
)

var suggestHarnessContextsForTUI = func(ctx context.Context, cfg *appconfig.Config, root, goal string, elements []string) ([]harness.ContextConfig, error) {
	fallback := harness.DetectContexts(root)
	if cfg == nil {
		return fallback, nil
	}
	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil || strings.TrimSpace(resolved.APIKey) == "" {
		return fallback, nil
	}
	prov, err := provider.NewProvider(resolved)
	if err != nil {
		return fallback, nil
	}
	contexts, err := harness.SuggestContexts(ctx, prov, harness.ContextSuggestionRequest{
		RootDir:       root,
		ProjectName:   filepath.Base(root),
		Goal:          goal,
		ExtraElements: elements,
		HintContexts:  nil,
	})
	if err != nil {
		if len(fallback) > 0 {
			return fallback, nil
		}
		return nil, err
	}
	if len(contexts) == 0 {
		return fallback, nil
	}
	return contexts, nil
}

var executeHarnessInit = func(root string, opts harness.InitOptions) (*harness.InitResult, error) {
	return harness.Init(root, opts)
}

type harnessContextPromptMode string

const (
	harnessContextPromptInit harnessContextPromptMode = "init"
	harnessContextPromptRun  harnessContextPromptMode = "run"
)

type harnessContextPromptStep string

const (
	harnessContextPromptStepElements harnessContextPromptStep = "elements"
	harnessContextPromptStepLoading  harnessContextPromptStep = "loading"
	harnessContextPromptStepSelect   harnessContextPromptStep = "select"
	harnessContextPromptStepUpgrade  harnessContextPromptStep = "upgrade"
	harnessContextPromptStepApplying harnessContextPromptStep = "applying"
	harnessContextPromptStepPersist  harnessContextPromptStep = "persist"
)

type harnessContextPromptState struct {
	mode               harnessContextPromptMode
	step               harnessContextPromptStep
	commandText        string
	goal               string
	workDir            string
	project            *harness.Project
	cfg                *harness.Config
	fromHarnessPanel   bool
	existingProject    bool
	initElements       string
	selected           map[int]bool
	cursor             int
	inputFocus         bool
	input              textinput.Model
	suggestions        []harness.ContextConfig
	selectedRunContext *harness.ContextConfig
	persistChoice      bool
	upgradeForce       bool
	message            string
}

func newHarnessPromptInput(placeholder string) textinput.Model {
	ti := textinput.New()
	ti.Prompt = "❯ "
	ti.Placeholder = placeholder
	ti.CharLimit = 512
	ti.SetWidth(60)
	return ti
}

func (m *Model) beginHarnessInitPrompt(commandText, goal string, fromPanel bool) tea.Cmd {
	workDir, _ := os.Getwd()
	_, statErr := os.Stat(filepath.Join(workDir, harness.ConfigRelPath))
	state := &harnessContextPromptState{
		mode:             harnessContextPromptInit,
		step:             harnessContextPromptStepElements,
		commandText:      strings.TrimSpace(commandText),
		goal:             strings.TrimSpace(goal),
		workDir:          workDir,
		fromHarnessPanel: fromPanel,
		existingProject:  statErr == nil,
		selected:         map[int]bool{},
		inputFocus:       true,
		input:            newHarnessPromptInput("Optional project elements, comma-separated"),
	}
	state.input.Focus()
	m.harnessContextPrompt = state
	if fromPanel && m.harnessPanel != nil {
		m.harnessPanel.message = ""
	}
	return nil
}

func (m *Model) beginHarnessRunPrompt(commandText, goal string, project harness.Project, cfg *harness.Config, fromPanel bool) tea.Cmd {
	state := &harnessContextPromptState{
		mode:             harnessContextPromptRun,
		step:             harnessContextPromptStepSelect,
		commandText:      strings.TrimSpace(commandText),
		goal:             strings.TrimSpace(goal),
		project:          &project,
		cfg:              cfg,
		fromHarnessPanel: fromPanel,
		selected:         map[int]bool{},
		input:            newHarnessPromptInput("New context: payments or checkout=apps/checkout"),
		suggestions:      harness.AugmentRunContexts(append([]harness.ContextConfig(nil), cfg.Contexts...), strings.TrimSpace(goal)),
	}
	if len(state.suggestions) == 0 {
		state.inputFocus = true
		state.input.Focus()
	} else {
		state.input.Placeholder = "Press Tab to type a new context"
		state.input.Blur()
	}
	m.harnessContextPrompt = state
	if fromPanel && m.harnessPanel != nil {
		m.harnessPanel.message = ""
	}
	return nil
}

func (m *Model) loadHarnessContextSuggestions(state *harnessContextPromptState) tea.Cmd {
	if state == nil {
		return nil
	}
	root := state.workDir
	goal := state.goal
	elements := harness.SplitCommaInput(state.initElements)
	cfg := m.config
	return func() tea.Msg {
		contexts, err := suggestHarnessContextsForTUI(context.Background(), cfg, root, goal, elements)
		return harnessContextSuggestionsMsg{Contexts: contexts, Err: err}
	}
}

func (m *Model) closeHarnessContextPrompt(message string) {
	if m.harnessContextPrompt != nil && m.harnessContextPrompt.fromHarnessPanel && m.harnessPanel != nil && strings.TrimSpace(message) != "" {
		m.harnessPanel.message = message
	}
	m.harnessContextPrompt = nil
}

func (m *Model) handleHarnessContextPromptKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	state := m.harnessContextPrompt
	if state == nil {
		return m, nil
	}
	switch state.step {
	case harnessContextPromptStepElements:
		return m.handleHarnessInitElementsKey(msg)
	case harnessContextPromptStepLoading:
		if msg.String() == "esc" || msg.String() == "ctrl+c" {
			m.closeHarnessContextPrompt("Cancelled harness context selection.")
		}
		return m, nil
	case harnessContextPromptStepSelect:
		return m.handleHarnessContextSelectKey(msg)
	case harnessContextPromptStepUpgrade:
		return m.handleHarnessInitUpgradeKey(msg)
	case harnessContextPromptStepApplying:
		return m, nil
	case harnessContextPromptStepPersist:
		return m.handleHarnessContextPersistKey(msg)
	default:
		return m, nil
	}
}

func (m *Model) handleHarnessInitElementsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	state := m.harnessContextPrompt
	if state == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		m.closeHarnessContextPrompt("Cancelled harness initialization.")
		return m, nil
	case "enter":
		state.initElements = state.input.Value()
		state.step = harnessContextPromptStepLoading
		state.message = ""
		state.input.Blur()
		return m, m.loadHarnessContextSuggestions(state)
	}
	var cmd tea.Cmd
	state.input, cmd = state.input.Update(msg)
	return m, cmd
}

func (m *Model) handleHarnessContextSelectKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	state := m.harnessContextPrompt
	if state == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		if state.fromHarnessPanel {
			m.closeHarnessContextPrompt("Cancelled harness action.")
			return m, nil
		}
		m.closeHarnessContextPrompt("")
		return m, nil
	case "tab":
		state.inputFocus = !state.inputFocus
		if state.inputFocus {
			state.input.Focus()
		} else {
			state.input.Blur()
		}
		return m, nil
	case "shift+tab":
		state.inputFocus = !state.inputFocus
		if state.inputFocus {
			state.input.Focus()
		} else {
			state.input.Blur()
		}
		return m, nil
	}
	if state.inputFocus {
		if msg.String() == "enter" {
			return m, m.submitHarnessContextCustomInput()
		}
		var cmd tea.Cmd
		state.input, cmd = state.input.Update(msg)
		return m, cmd
	}
	switch msg.String() {
	case "up", "k":
		if len(state.suggestions) > 0 {
			state.cursor = (state.cursor - 1 + len(state.suggestions)) % len(state.suggestions)
		}
	case "down", "j":
		if len(state.suggestions) > 0 {
			state.cursor = (state.cursor + 1) % len(state.suggestions)
		}
	case " ":
		if state.mode == harnessContextPromptInit && len(state.suggestions) > 0 {
			state.selected[state.cursor] = !state.selected[state.cursor]
		}
	case "enter":
		if state.mode == harnessContextPromptInit {
			return m, m.submitHarnessInitSelection()
		}
		if len(state.suggestions) == 0 {
			state.message = "Type a new context first."
			return m, nil
		}
		selected := state.suggestions[state.cursor]
		m.closeHarnessContextPrompt("")
		if state.fromHarnessPanel {
			m.closeHarnessPanel()
		}
		return m, m.runTrackedHarnessGoal(state.commandText, state.goal, *state.project, state.cfg, harness.RunTaskOptions{
			ContextName: selected.Name,
			ContextPath: selected.Path,
		})
	}
	return m, nil
}

func (m *Model) submitHarnessContextCustomInput() tea.Cmd {
	state := m.harnessContextPrompt
	if state == nil {
		return nil
	}
	custom := harness.ParseContextSpecs(state.input.Value())
	if len(custom) == 0 {
		state.message = "Enter a context like payments or checkout=apps/checkout."
		return nil
	}
	selected := custom[0]
	state.message = ""
	if state.mode == harnessContextPromptInit {
		contexts := harness.NormalizeContexts(append(state.suggestions, custom...))
		if state.existingProject {
			state.suggestions = contexts
			state.step = harnessContextPromptStepUpgrade
			state.upgradeForce = false
			state.input.Blur()
			state.inputFocus = false
			return nil
		}
		return m.runHarnessInitWithContexts(contexts, false)
	}
	state.selectedRunContext = &selected
	if state.cfg != nil {
		if existing := resolveExistingRunContext(state.cfg, state.input.Value(), selected); existing != nil {
			m.closeHarnessContextPrompt("")
			if state.fromHarnessPanel {
				m.closeHarnessPanel()
			}
			return m.runTrackedHarnessGoal(state.commandText, state.goal, *state.project, state.cfg, harness.RunTaskOptions{
				ContextName: existing.Name,
				ContextPath: existing.Path,
			})
		}
	}
	state.step = harnessContextPromptStepPersist
	state.persistChoice = true
	state.input.Blur()
	return nil
}

func resolveExistingRunContext(cfg *harness.Config, raw string, parsed harness.ContextConfig) *harness.ContextConfig {
	if cfg == nil {
		return nil
	}
	candidates := []string{strings.TrimSpace(raw), strings.TrimSpace(parsed.Name), strings.TrimSpace(parsed.Path)}
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		match, err := harness.ResolveContext(cfg, candidate)
		if err == nil && match != nil {
			return match
		}
	}
	return nil
}

func (m *Model) submitHarnessInitSelection() tea.Cmd {
	state := m.harnessContextPrompt
	if state == nil {
		return nil
	}
	var selected []harness.ContextConfig
	if len(state.selected) == 0 {
		selected = append(selected, state.suggestions...)
	} else {
		for idx, item := range state.suggestions {
			if state.selected[idx] {
				selected = append(selected, item)
			}
		}
	}
	if extra := harness.ParseContextSpecs(state.input.Value()); len(extra) > 0 {
		selected = append(selected, extra...)
	}
	selected = harness.NormalizeContexts(selected)
	if state.existingProject {
		state.suggestions = selected
		state.step = harnessContextPromptStepUpgrade
		state.upgradeForce = false
		state.input.Blur()
		state.inputFocus = false
		return nil
	}
	return m.runHarnessInitWithContexts(selected, false)
}

func (m *Model) runHarnessInitWithContexts(contexts []harness.ContextConfig, force bool) tea.Cmd {
	state := m.harnessContextPrompt
	if state == nil {
		return nil
	}
	workDir := state.workDir
	state.step = harnessContextPromptStepApplying
	state.upgradeForce = force
	state.message = ""
	return func() tea.Msg {
		result, err := executeHarnessInit(workDir, harness.InitOptions{
			Goal:     state.goal,
			Force:    force,
			Contexts: contexts,
		})
		return harnessInitResultMsg{Result: result, Err: err}
	}
}

func (m *Model) handleHarnessInitUpgradeKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	state := m.harnessContextPrompt
	if state == nil {
		return m, nil
	}
	switch msg.String() {
	case "left", "h", "tab", "shift+tab":
		state.upgradeForce = !state.upgradeForce
	case "right", "l", "up", "k", "down", "j":
		state.upgradeForce = !state.upgradeForce
	case "y", "Y":
		state.upgradeForce = true
	case "n", "N":
		state.upgradeForce = false
	case "esc", "ctrl+c":
		state.step = harnessContextPromptStepSelect
		return m, nil
	case "enter":
		return m, m.runHarnessInitWithContexts(state.suggestions, state.upgradeForce)
	}
	return m, nil
}

func (m *Model) handleHarnessContextPersistKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	state := m.harnessContextPrompt
	if state == nil {
		return m, nil
	}
	switch msg.String() {
	case "left", "h", "tab", "shift+tab":
		state.persistChoice = !state.persistChoice
	case "right", "l", "up", "k", "down", "j":
		state.persistChoice = !state.persistChoice
	case "y", "Y":
		state.persistChoice = true
	case "n", "N":
		state.persistChoice = false
	case "esc", "ctrl+c":
		state.step = harnessContextPromptStepSelect
		if state.inputFocus {
			state.input.Focus()
		}
		return m, nil
	case "enter":
		selected := state.selectedRunContext
		if selected == nil {
			state.step = harnessContextPromptStepSelect
			return m, nil
		}
		if state.persistChoice && state.project != nil && state.cfg != nil {
			state.cfg.Contexts = harness.NormalizeContexts(append(state.cfg.Contexts, *selected))
			if err := harness.SaveConfig(state.project.ConfigPath, state.cfg); err != nil {
				state.message = err.Error()
				return m, nil
			}
		}
		m.closeHarnessContextPrompt("")
		if state.fromHarnessPanel {
			m.closeHarnessPanel()
		}
		return m, m.runTrackedHarnessGoal(state.commandText, state.goal, *state.project, state.cfg, harness.RunTaskOptions{
			ContextName: selected.Name,
			ContextPath: selected.Path,
		})
	}
	return m, nil
}

func (m *Model) renderHarnessContextPrompt() string {
	state := m.harnessContextPrompt
	if state == nil {
		return ""
	}
	var title string
	var body strings.Builder
	accent := lipgloss.Color("12")
	switch state.mode {
	case harnessContextPromptInit:
		title = "Harness init contexts"
		if strings.TrimSpace(state.goal) != "" {
			fmt.Fprintf(&body, "Goal: %s\n\n", state.goal)
		}
		switch state.step {
		case harnessContextPromptStepElements:
			body.WriteString("Add any project elements that help the model infer bounded contexts.\n\n")
			body.WriteString(state.input.View())
			body.WriteString("\n")
			body.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("Enter continues • Esc cancels"))
		case harnessContextPromptStepLoading:
			body.WriteString("Generating context suggestions...\n")
		case harnessContextPromptStepSelect:
			body.WriteString("Select suggested contexts, or add custom ones.\n\n")
			body.WriteString(renderHarnessContextList(state.suggestions, state.cursor, state.selected, true))
			body.WriteString("\n\n")
			body.WriteString(state.input.View())
			body.WriteString("\n")
			body.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("Space toggles • Tab switches focus • Enter confirms"))
		case harnessContextPromptStepUpgrade:
			body.WriteString("Harness is already initialized. Re-run init to sync newer scaffold logic.\n\n")
			if state.upgradeForce {
				body.WriteString(m.styles.approvalCursor.Render("❯ Overwrite existing harness-managed templates (--force)") + "\n")
				body.WriteString(m.styles.approvalDim.Render("  Keep existing files and only add missing scaffold") + "\n")
			} else {
				body.WriteString(m.styles.approvalDim.Render("  Overwrite existing harness-managed templates (--force)") + "\n")
				body.WriteString(m.styles.approvalCursor.Render("❯ Keep existing files and only add missing scaffold") + "\n")
			}
			body.WriteString("\n")
			body.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("Enter continues • Y force overwrite • N keep existing"))
		case harnessContextPromptStepApplying:
			if state.upgradeForce {
				body.WriteString("Applying harness scaffold update with template overwrite...\n")
			} else {
				body.WriteString("Applying harness scaffold update...\n")
			}
		}
	case harnessContextPromptRun:
		title = "Harness run context"
		if strings.TrimSpace(state.goal) != "" {
			fmt.Fprintf(&body, "Goal: %s\n\n", state.goal)
		}
		switch state.step {
		case harnessContextPromptStepSelect:
			body.WriteString("Choose an existing context or type a new one.\n\n")
			body.WriteString(renderHarnessContextList(state.suggestions, state.cursor, nil, false))
			body.WriteString("\n\n")
			body.WriteString(state.input.View())
			body.WriteString("\n")
			body.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("Enter picks selection • Tab switches focus"))
		case harnessContextPromptStepPersist:
			label := ""
			if state.selectedRunContext != nil {
				label = firstNonEmptyHarness(state.selectedRunContext.Name, state.selectedRunContext.Path)
			}
			fmt.Fprintf(&body, "New context: %s\n\nPersist it to .ggcode/harness.yaml?\n\n", label)
			yes := "Yes"
			no := "No"
			if state.persistChoice {
				yes = m.styles.approvalCursor.Render("❯ Yes")
				no = m.styles.approvalDim.Render("  No")
			} else {
				yes = m.styles.approvalDim.Render("  Yes")
				no = m.styles.approvalCursor.Render("❯ No")
			}
			body.WriteString(yes + "\n" + no + "\n")
			body.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("←/→/Tab switch • Enter confirm"))
		}
	}
	if msg := strings.TrimSpace(state.message); msg != "" {
		body.WriteString("\n\n")
		body.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(msg))
	}
	return m.renderContextBox(title, body.String(), accent)
}

func renderHarnessContextList(contexts []harness.ContextConfig, cursor int, selected map[int]bool, multi bool) string {
	if len(contexts) == 0 {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("No suggested contexts yet.")
	}
	lines := make([]string, 0, len(contexts))
	for idx, item := range contexts {
		label := firstNonEmptyHarness(item.Name, item.Path)
		desc := strings.TrimSpace(item.Description)
		prefix := "  "
		if idx == cursor {
			prefix = "❯ "
		}
		check := " "
		if multi && selected != nil && selected[idx] {
			check = "x"
		}
		line := fmt.Sprintf("%s[%s] %s", prefix, check, label)
		if !multi {
			line = prefix + label
		}
		if desc != "" {
			line += " — " + desc
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
