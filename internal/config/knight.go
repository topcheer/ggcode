package config

// KnightConfig holds configuration for the Knight background agent.
type KnightConfig struct {
	// Enabled controls whether Knight runs in daemon mode.
	Enabled bool `yaml:"enabled,omitempty"`

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
		IdleDelaySec: 300,
	}
}

// Knight returns the Knight configuration, applying defaults for zero values.
func (c *Config) Knight() KnightConfig {
	kc := c.KnightConfig
	if kc.DailyTokenBudget == 0 {
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
