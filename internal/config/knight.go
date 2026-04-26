package config

import "gopkg.in/yaml.v3"

// KnightConfig holds configuration for the Knight background agent.
type KnightConfig struct {
	// Enabled controls whether Knight runs in daemon mode.
	Enabled bool `yaml:"enabled,omitempty"`

	// Vendor/Endpoint/Model optionally override the main agent LLM selection.
	// Any empty field falls back to the main active selection.
	Vendor   string `yaml:"vendor,omitempty"`
	Endpoint string `yaml:"endpoint,omitempty"`
	Model    string `yaml:"model,omitempty"`

	// TrustLevel controls Knight's autonomy for skill management.
	// "readonly" — Knight only analyzes, never writes anything
	// "staged"   — Knight writes to staging, user approves promotion (default)
	// "auto"     — Knight auto-promotes skills that pass validation
	TrustLevel string `yaml:"trust_level,omitempty"`

	// DailyTokenBudget is the maximum tokens Knight may consume per day.
	// Default: 5,000,000 (5M). Set to 0 to disable budget checking.
	DailyTokenBudget int `yaml:"daily_token_budget,omitempty"`

	// Capabilities lists what Knight is allowed to do.
	// Available: skill_creation, skill_validation, test_generation,
	//            regression_testing, doc_sync
	Capabilities []string `yaml:"capabilities,omitempty"`

	// QuietHours defines time ranges when Knight should not run tasks
	// or send notifications. Format: "HH:MM-HH:MM".
	QuietHours []string `yaml:"quiet_hours,omitempty"`

	// IdleDelaySec is how long to wait after the last user interaction
	// before Knight starts idle tasks. Default: 300 (5 minutes).
	IdleDelaySec int `yaml:"idle_delay_sec,omitempty"`

	dailyTokenBudgetSet bool `yaml:"-"`
}

// DefaultKnightConfig returns the default Knight configuration.
func DefaultKnightConfig() KnightConfig {
	return KnightConfig{
		Enabled:          false,
		TrustLevel:       "staged",
		DailyTokenBudget: 5_000_000,
		Capabilities: []string{
			"skill_creation",
			"skill_validation",
			"test_generation",
			"regression_testing",
			"doc_sync",
		},
		IdleDelaySec:        300,
		dailyTokenBudgetSet: true,
	}
}

// UnmarshalYAML keeps track of whether daily_token_budget was explicitly set so
// runtime defaults can distinguish "unset" from "set to 0 (unlimited)".
func (kc *KnightConfig) UnmarshalYAML(value *yaml.Node) error {
	type rawKnightConfig KnightConfig
	var decoded rawKnightConfig
	if err := value.Decode(&decoded); err != nil {
		return err
	}
	*kc = KnightConfig(decoded)
	for i := 0; i+1 < len(value.Content); i += 2 {
		if value.Content[i].Value == "daily_token_budget" {
			kc.dailyTokenBudgetSet = true
			break
		}
	}
	return nil
}

// HasExplicitDailyTokenBudget reports whether daily_token_budget was explicitly
// configured, including an explicit 0 to disable budget enforcement.
func (kc KnightConfig) HasExplicitDailyTokenBudget() bool {
	return kc.dailyTokenBudgetSet
}

// Knight returns the Knight configuration, applying defaults for zero values.
func (c *Config) Knight() KnightConfig {
	kc := c.KnightConfig
	if kc.DailyTokenBudget < 0 {
		kc.DailyTokenBudget = 5_000_000
	}
	if kc.DailyTokenBudget == 0 && !kc.HasExplicitDailyTokenBudget() {
		kc.DailyTokenBudget = 5_000_000
	}
	if kc.TrustLevel == "" {
		kc.TrustLevel = "staged"
	}
	if kc.IdleDelaySec == 0 {
		kc.IdleDelaySec = 300
	}
	if len(kc.Capabilities) == 0 {
		kc.Capabilities = []string{
			"skill_creation",
			"skill_validation",
			"test_generation",
			"regression_testing",
			"doc_sync",
		}
	}
	return kc
}

// ResolveKnightEndpoint resolves Knight's optional dedicated provider selection.
// Any missing vendor/endpoint/model field falls back to the main active selection.
func (c *Config) ResolveKnightEndpoint() (*ResolvedEndpoint, error) {
	if c == nil {
		return nil, nil
	}
	kc := c.Knight()
	vendor := kc.Vendor
	if vendor == "" {
		vendor = c.Vendor
	}
	endpoint := kc.Endpoint
	if endpoint == "" {
		endpoint = c.Endpoint
	}
	model := kc.Model
	if model == "" {
		model = c.Model
	}
	return c.ResolveEndpointSelection(vendor, endpoint, model)
}
