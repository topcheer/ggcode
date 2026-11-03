package config

import (
	"sort"

	"gopkg.in/yaml.v3"
)

type KnightConfig struct {
	Enabled bool `yaml:"enabled,omitempty"`

	enabledSet bool `yaml:"-"`

	Vendor   string `yaml:"vendor,omitempty"`
	Endpoint string `yaml:"endpoint,omitempty"`
	Model    string `yaml:"model,omitempty"`

	TrustLevel string `yaml:"trust_level,omitempty"`

	DailyTokenBudget int `yaml:"daily_token_budget,omitempty"`

	Capabilities []string `yaml:"capabilities,omitempty"`

	QuietHours []string `yaml:"quiet_hours,omitempty"`

	IdleDelaySec int `yaml:"idle_delay_sec,omitempty"`

	dailyTokenBudgetSet bool `yaml:"-"`
}

func DefaultKnightConfig() KnightConfig {
	return KnightConfig{
		Enabled:          false,
		TrustLevel:       "staged",
		DailyTokenBudget: 50_000_000,
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

func (kc *KnightConfig) UnmarshalYAML(value *yaml.Node) error {
	type rawKnightConfig KnightConfig
	var decoded rawKnightConfig
	if err := value.Decode(&decoded); err != nil {
		return err
	}
	*kc = KnightConfig(decoded)
	for i := 0; i+1 < len(value.Content); i += 2 {
		switch value.Content[i].Value {
		case "daily_token_budget":
			kc.dailyTokenBudgetSet = true
		case "enabled":
			kc.enabledSet = true
		}
	}
	return nil
}

func (kc KnightConfig) HasExplicitDailyTokenBudget() bool {
	return kc.dailyTokenBudgetSet
}

func (kc KnightConfig) HasExplicitEnabled() bool {
	return kc.enabledSet
}

func (kc *KnightConfig) SetEnabledExplicitly() {
	kc.enabledSet = true
}

func (kc KnightConfig) MarshalYAML() (interface{}, error) {
	type rawKnightConfig KnightConfig
	var node yaml.Node
	if err := node.Encode(rawKnightConfig(kc)); err != nil {
		return nil, err
	}
	if kc.enabledSet && !kc.Enabled && node.Kind == yaml.MappingNode {
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "enabled"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "false"},
		)
	}
	return node, nil
}

func (c *Config) Knight() KnightConfig {
	kc := c.KnightConfig

	kc.SetDefaults()
	return kc
}

func (kc *KnightConfig) SetDefaults() {
	if kc.DailyTokenBudget < 0 {
		kc.DailyTokenBudget = 50_000_000
	}
	if kc.DailyTokenBudget == 0 && !kc.HasExplicitDailyTokenBudget() {
		kc.DailyTokenBudget = 50_000_000
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
}

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
	model := kc.Model
	if vendor == c.Vendor {

		if endpoint == "" {
			endpoint = c.Endpoint
		}
		if model == "" {
			model = c.Model
		}
	} else if endpoint == "" {

		if vc, ok := c.Vendors[vendor]; ok && len(vc.Endpoints) > 0 {
			ids := make([]string, 0, len(vc.Endpoints))
			for id := range vc.Endpoints {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			endpoint = ids[0]
		}
	}

	return c.ResolveEndpointSelection(vendor, endpoint, model)
}
