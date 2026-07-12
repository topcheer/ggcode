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

// CustomProviderConfig holds user-entered custom provider details.
type CustomProviderConfig struct {
	Name     string
	Protocol string
	BaseURL  string
	APIKey   string
	Model    string
}

// OnboardResult holds the user's selections from the onboard wizard.
type OnboardResult struct {
	Language       string
	VendorID       string
	EndpointID     string
	APIKey         string
	Model          string
	Mode           string
	Knight         bool
	A2A            bool
	IMAdapters     map[string]config.IMAdapterConfig
	CustomProvider *CustomProviderConfig
}

type onboardStep int

const (
	onboardStepLanguage onboardStep = iota
	onboardStepVendor
	onboardStepCustom
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

var customProtocols = []string{"openai", "anthropic", "ollama"}

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

	// Custom provider
	customProtocolIdx int
	customFields      [4]textinput.Model       // 0=name, 1=url, 2=apikey, 3=model
	customCursor      int                      // 0=protocol, 1-4=fields, 5=submit
	customResolved    *config.ResolvedEndpoint // built when custom provider submits, used for model discovery
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
	// Custom provider field placeholders
	m.customFields[0].Placeholder = "My Provider..."
	m.customFields[1].Placeholder = "https://api.example.com/v1"
	m.customFields[2].Placeholder = "sk-..."
	m.customFields[3].Placeholder = "gpt-4o"
}

// RunOnboard starts the onboard wizard as an independent Bubble Tea program.
func RunOnboard(cfg *config.Config) (*OnboardResult, error) {
	presets := config.VendorPresets()
	langs := []struct {
		code string
		name string
	}{
		{"en", "English"},
		{"zh-CN", "简体中文"},
		{"zh-TW", "繁體中文"},
		{"ja", "日本語"},
		{"ko", "한국어"},
		{"es", "Español"},
		{"fr", "Français"},
		{"de", "Deutsch"},
		{"ru", "Русский"},
		{"pt", "Português"},
		{"vi", "Tiếng Việt"},
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

	// Custom provider inputs: 0=name, 1=url, 2=apikey, 3=model
	var customFields [4]textinput.Model
	for i := range customFields {
		customFields[i] = textinput.New()
		customFields[i].Prompt = "> "
	}
	customFields[2].EchoMode = textinput.EchoPassword
	customFields[2].EchoCharacter = '•'

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
		customFields:   customFields,
		customCursor:   0,
		optMode:        2, // bypass
		optA2A:         true,
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

		// Collect custom provider if user entered one
		if strings.TrimSpace(om.customFields[0].Value()) != "" {
			r.CustomProvider = &CustomProviderConfig{
				Name:     strings.TrimSpace(om.customFields[0].Value()),
				Protocol: customProtocols[om.customProtocolIdx],
				BaseURL:  strings.TrimSpace(om.customFields[1].Value()),
				APIKey:   strings.TrimSpace(om.customFields[2].Value()),
				Model:    strings.TrimSpace(om.customFields[3].Value()),
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
		if m.step == onboardStepModel {
			m.modelLoading = false
			if len(msg.models) > 0 {
				m.allModels = msg.models
				m.models = msg.models
				m.applyModelFilter()
			}
		}
		return m, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			if m.step == onboardStepLanguage {
				return m, tea.Quit
			}
			m.step = m.prevStep()
			m.err = ""
			m.restoreFocus()
			return m, nil
		case "esc":
			if m.step == onboardStepLanguage {
				return m, tea.Quit
			}
			m.step = m.prevStep()
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
	case onboardStepCustom:
		return m.updateCustom(msg)
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

// prevStep returns the correct previous step, accounting for the non-linear
// step structure where Custom(2) is a branch off Vendor that jumps to Optional(5).
// A naive m.step-- from Endpoint(3) would land on Custom(2) instead of Vendor(1),
// and from Optional(5) would land on Model(4) even if the user came via Custom.
func (m *onboardModel) prevStep() onboardStep {
	switch m.step {
	case onboardStepEndpoint:
		// Endpoint is always reached from Vendor, never from Custom.
		return onboardStepVendor
	case onboardStepModel:
		// If the user came via Custom path (selectedVendor not set),
		// go back to Custom, not Endpoint.
		if m.selectedVendor.ID == "" {
			return onboardStepCustom
		}
		return onboardStepEndpoint
	case onboardStepOptional:
		// If the user came via Custom path (selectedVendor not set),
		// go back to Custom, not Model.
		if m.selectedVendor.ID == "" {
			return onboardStepCustom
		}
		return onboardStepModel
	default:
		if m.step > onboardStepLanguage {
			return m.step - 1
		}
		return m.step
	}
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
	case onboardStepCustom:
		if m.customCursor >= 1 && m.customCursor <= 4 {
			m.customFields[m.customCursor-1].Focus()
		}
	case onboardStepIM:
		if m.imFocused >= 0 && m.imFocused < len(m.imInputs) {
			m.imInputs[m.imFocused].Focus()
		}
	}
}

func (m *onboardModel) buildResolved() *config.ResolvedEndpoint {
	// Custom provider path: use the resolved endpoint built from custom fields
	if m.selectedVendor.ID == "" && m.customResolved != nil {
		return m.customResolved
	}
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

// buildCustomResolved creates a ResolvedEndpoint from custom provider form fields.
func (m *onboardModel) buildCustomResolved() *config.ResolvedEndpoint {
	protocol := customProtocols[m.customProtocolIdx]
	name := strings.TrimSpace(m.customFields[0].Value())
	url := strings.TrimSpace(m.customFields[1].Value())
	apiKey := strings.TrimSpace(m.customFields[2].Value())
	model := strings.TrimSpace(m.customFields[3].Value())
	if url == "" {
		return nil
	}
	return &config.ResolvedEndpoint{
		VendorID:     "custom",
		VendorName:   name,
		EndpointID:   "default",
		EndpointName: name,
		Protocol:     protocol,
		AuthType:     "api_key",
		BaseURL:      url,
		APIKey:       apiKey,
		Model:        model,
	}
}
