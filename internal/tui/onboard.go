package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"image/color"

	"github.com/topcheer/ggcode/internal/config"
)

// OnboardResult holds the user's selections from the onboard wizard.
type OnboardResult struct {
	Language   string
	VendorID   string
	EndpointID string
	APIKey     string
	Model      string
	Mode       string
	Knight     bool
	A2A        bool
	IMAdapters map[string]config.IMAdapterConfig
}

type onboardStep int

const (
	onboardStepLanguage onboardStep = iota
	onboardStepVendor
	onboardStepEndpoint
	onboardStepModel
	onboardStepOptional
	onboardStepIM
	onboardStepDone
)

type endpointFocus int

const (
	focusEndpoint endpointFocus = iota
	focusAPIKey
)

type discoverResultMsg struct {
	models []string
}

type onboardIMChannel struct {
	platform    string
	labelKey    string
	placeholder string
}

var imChannels = [4]onboardIMChannel{
	{"telegram", "im_telegram", "im_token_placeholder"},
	{"discord", "im_discord", "im_token_placeholder"},
	{"qq", "im_qq", "im_token_placeholder"},
	{"wechat", "im_wechat", "im_token_placeholder"},
}

var modeLabels = []string{"supervised", "auto", "bypass", "autopilot"}
var modeColors = []color.Color{
	lipgloss.Color("11"), // yellow - supervised
	lipgloss.Color("10"), // green  - auto
	lipgloss.Color("9"),  // red    - bypass
	lipgloss.Color("13"), // magenta - autopilot
}

type onboardModel struct {
	cfg     *config.Config
	presets []config.VendorPreset
	step    onboardStep
	width   int
	height  int
	err     string

	langCursor int
	langs      []struct {
		code string
		name string
	}

	vendorCursor   int
	vendorFilter   textinput.Model
	vendorFiltered []int

	selectedVendor config.VendorPreset
	endpointCursor int
	apiKeyInput    textinput.Model
	epFocus        endpointFocus

	modelCursor   int
	models        []string
	allModels     []string
	modelFilter   textinput.Model
	modelFiltered []int
	modelLoading  bool

	optCursor int
	optMode   int
	optKnight bool
	optA2A    bool

	imCursor  int
	imInputs  [5]textinput.Model // 0=telegram, 1=discord, 2=qq_appid, 3=qq_secret, 4=(unused, wechat=scan)
	imFocused int
}

func (m *onboardModel) currentLanguage() Language {
	if len(m.langs) == 0 || m.langCursor < 0 || m.langCursor >= len(m.langs) {
		return LangEnglish
	}
	return normalizeLanguage(m.langs[m.langCursor].code)
}

func (m *onboardModel) refreshInputPlaceholders() {
	lang := m.currentLanguage()
	m.vendorFilter.Placeholder = placeholderWithPasteShortcutHint("Type to filter vendors...", lang)
	m.apiKeyInput.Placeholder = placeholderWithPasteShortcutHint("Enter your API key...", lang)
	m.modelFilter.Placeholder = placeholderWithPasteShortcutHint("Type to filter models...", lang)
	imLabels := []string{
		"Bot token...",
		"Bot token...",
		"App ID...",
		"App Secret...",
		"",
	}
	for i := range m.imInputs {
		m.imInputs[i].Placeholder = placeholderWithPasteShortcutHint(imLabels[i], lang)
	}
}

// RunOnboard starts the onboard wizard as an independent Bubble Tea program.
func RunOnboard(cfg *config.Config) (*OnboardResult, error) {
	presets := config.VendorPresets()
	langs := []struct {
		code string
		name string
	}{
		{"en", "English"},
		{"zh-CN", "中文"},
	}

	vf := textinput.New()
	vf.Prompt = "> "

	ak := textinput.New()
	ak.Prompt = "> "
	ak.EchoMode = textinput.EchoPassword
	ak.EchoCharacter = '•'

	mf := textinput.New()
	mf.Prompt = "> "

	filtered := make([]int, len(presets))
	for i := range presets {
		filtered[i] = i
	}

	// IM inputs: 0=telegram, 1=discord, 2=qq_appid, 3=qq_secret, 4=unused(wechat=scan)
	var imInputs [5]textinput.Model
	for i := range imInputs {
		imInputs[i] = textinput.New()
		imInputs[i].Prompt = "> "
		imInputs[i].EchoMode = textinput.EchoPassword
		imInputs[i].EchoCharacter = '•'
	}

	m := onboardModel{
		cfg:            cfg,
		presets:        presets,
		langs:          langs,
		vendorFilter:   vf,
		vendorFiltered: filtered,
		apiKeyInput:    ak,
		modelFilter:    mf,
		imInputs:       imInputs,
		imFocused:      -1,
	}
	m.refreshInputPlaceholders()

	p := tea.NewProgram(&m)
	result, err := p.Run()
	if err != nil {
		return nil, err
	}
	if om, ok := result.(*onboardModel); ok && om.step == onboardStepDone {
		selectedModel := ""
		if len(om.modelFiltered) > 0 && om.modelCursor < len(om.modelFiltered) {
			selectedModel = om.models[om.modelFiltered[om.modelCursor]]
		} else if len(om.models) > 0 {
			selectedModel = om.models[0]
		}

		epID := ""
		if len(om.selectedVendor.Endpoints) > 0 && om.endpointCursor < len(om.selectedVendor.Endpoints) {
			epID = om.selectedVendor.Endpoints[om.endpointCursor].ID
		}

		r := &OnboardResult{
			Language:   om.langs[om.langCursor].code,
			VendorID:   om.selectedVendor.ID,
			EndpointID: epID,
			APIKey:     om.apiKeyInput.Value(),
			Model:      selectedModel,
			Mode:       modeLabels[om.optMode],
			Knight:     om.optKnight,
			A2A:        om.optA2A,
		}

		// Collect IM adapters
		r.IMAdapters = map[string]config.IMAdapterConfig{}
		// Telegram
		if token := strings.TrimSpace(om.imInputs[0].Value()); token != "" {
			r.IMAdapters["telegram"] = config.IMAdapterConfig{
				Enabled:  true,
				Platform: "telegram",
				Extra:    map[string]interface{}{"bot_token": token},
			}
		}
		// Discord
		if token := strings.TrimSpace(om.imInputs[1].Value()); token != "" {
			r.IMAdapters["discord"] = config.IMAdapterConfig{
				Enabled:  true,
				Platform: "discord",
				Extra:    map[string]interface{}{"bot_token": token},
			}
		}
		// QQ (needs both app_id and app_secret)
		appID := strings.TrimSpace(om.imInputs[2].Value())
		appSecret := strings.TrimSpace(om.imInputs[3].Value())
		if appID != "" && appSecret != "" {
			r.IMAdapters["qq"] = config.IMAdapterConfig{
				Enabled:   true,
				Platform:  "qq",
				Transport: "builtin",
				Extra:     map[string]interface{}{"app_id": appID, "app_secret": appSecret},
			}
		}

		return r, nil
	}
	return nil, fmt.Errorf("onboard cancelled")
}

func (m *onboardModel) Init() tea.Cmd {
	return nil
}

func (m *onboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case discoverResultMsg:
		if m.step == onboardStepModel && len(msg.models) > 0 {
			m.allModels = msg.models
			m.models = msg.models
			m.applyModelFilter()
			m.modelLoading = false
		}
		return m, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			if m.step == onboardStepLanguage {
				return m, tea.Quit
			}
			m.step--
			m.err = ""
			m.restoreFocus()
			return m, nil
		case "esc":
			if m.step == onboardStepLanguage {
				return m, tea.Quit
			}
			m.step--
			m.err = ""
			m.restoreFocus()
			return m, nil
		}
	}

	switch m.step {
	case onboardStepLanguage:
		return m.updateLanguage(msg)
	case onboardStepVendor:
		return m.updateVendor(msg)
	case onboardStepEndpoint:
		return m.updateEndpoint(msg)
	case onboardStepModel:
		return m.updateModel(msg)
	case onboardStepOptional:
		return m.updateOptional(msg)
	case onboardStepIM:
		return m.updateIM(msg)
	}
	return m, nil
}

func (m *onboardModel) restoreFocus() {
	switch m.step {
	case onboardStepVendor:
		m.vendorFilter.Focus()
	case onboardStepEndpoint:
		if m.epFocus == focusAPIKey {
			m.apiKeyInput.Focus()
		}
	case onboardStepModel:
		m.modelFilter.Focus()
	case onboardStepIM:
		if m.imFocused >= 0 && m.imFocused < len(m.imInputs) {
			m.imInputs[m.imFocused].Focus()
		}
	}
}

func (m *onboardModel) buildResolved() *config.ResolvedEndpoint {
	if len(m.selectedVendor.Endpoints) == 0 {
		return nil
	}
	ep := m.selectedVendor.Endpoints[m.endpointCursor]
	apiKey := strings.TrimSpace(m.apiKeyInput.Value())
	return &config.ResolvedEndpoint{
		VendorID:     m.selectedVendor.ID,
		VendorName:   m.selectedVendor.DisplayName,
		EndpointID:   ep.ID,
		EndpointName: ep.DisplayName,
		Protocol:     ep.Protocol,
		AuthType:     "api_key",
		BaseURL:      ep.BaseURL,
		APIKey:       apiKey,
		Model:        ep.DefaultModel,
	}
}
