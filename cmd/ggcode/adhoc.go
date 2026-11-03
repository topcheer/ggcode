package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

const (
	adhocVendorName   = "adhoc"
	adhocEndpointName = "adhoc"
)

type adhocLLMFlags struct {
	BaseURL       string
	Model         string
	APIKey        string `json:"-"`
	Protocol      string
	ContextWindow int
	MaxTokens     int
	RetryAlways   bool
}

func (f *adhocLLMFlags) adhocActive() bool {
	return adhocValue(f.BaseURL, adhocEnvBaseURL) != "" && adhocValue(f.Model, adhocEnvModel) != ""
}

func adhocValue(flagVal, envKey string) string {
	if strings.TrimSpace(flagVal) != "" {
		return strings.TrimSpace(flagVal)
	}
	return strings.TrimSpace(os.Getenv(envKey))
}

const (
	adhocEnvBaseURL       = "GGCODE_LLM_BASE_URL"
	adhocEnvModel         = "GGCODE_LLM_MODEL"
	adhocEnvAPIKey        = "GGCODE_LLM_API_KEY"
	adhocEnvProtocol      = "GGCODE_LLM_PROTOCOL"
	adhocEnvContextWindow = "GGCODE_LLM_CONTEXT_WINDOW"
	adhocEnvMaxTokens     = "GGCODE_LLM_MAX_TOKENS"
	adhocEnvRetryAlways   = "GGCODE_LLM_RETRY_ALWAYS"
)

func (f *adhocLLMFlags) synthesizeAdhocConfig() (*config.Config, error) {
	baseURL := adhocValue(f.BaseURL, adhocEnvBaseURL)
	model := adhocValue(f.Model, adhocEnvModel)
	apiKey := adhocValue(f.APIKey, adhocEnvAPIKey)
	protocol := adhocValue(f.Protocol, adhocEnvProtocol)

	if baseURL == "" || model == "" {

		return nil, fmt.Errorf(
			"ad-hoc provider mode needs BOTH a base URL and a model (flags --llm-base-url/--llm-model or env %s/%s); got base-url=%q model=%q",
			adhocEnvBaseURL, adhocEnvModel, baseURL, model)
	}
	if protocol == "" {
		protocol = adhocInferProtocol(baseURL)
	}

	ep := config.EndpointConfig{
		DisplayName:   "Ad-hoc (" + protocol + ")",
		Protocol:      protocol,
		BaseURL:       baseURL,
		APIKey:        apiKey,
		ContextWindow: adhocInt(f.ContextWindow, adhocEnvContextWindow),
		MaxTokens:     adhocInt(f.MaxTokens, adhocEnvMaxTokens),
		SelectedModel: model,
		Models:        []string{model},
	}

	cfg := &config.Config{
		Vendor:   adhocVendorName,
		Endpoint: adhocEndpointName,
		Model:    model,
		Vendors: map[string]config.VendorConfig{
			adhocVendorName: {
				DisplayName: "Ad-hoc",
				APIKey:      apiKey,
				Endpoints: map[string]config.EndpointConfig{
					adhocEndpointName: ep,
				},
			},
		},
	}
	return cfg, nil
}

func adhocInferProtocol(baseURL string) string {
	u := strings.ToLower(baseURL)
	switch {
	case strings.Contains(u, "anthropic"):
		return "anthropic"
	case strings.Contains(u, "generativelanguage") || strings.Contains(u, "aiplatform"):
		return "gemini"
	default:
		return "openai"
	}
}

var adhocLLM adhocLLMFlags

func loadConfigForRun(cfgFile string, f *adhocLLMFlags) (*config.Config, error) {
	if f.adhocActive() {

		if adhocBool(f.RetryAlways, adhocEnvRetryAlways) {
			provider.SetRetryAlways(true)
		}
		return f.synthesizeAdhocConfig()
	}

	if f != nil {
		if baseURL := adhocValue(f.BaseURL, adhocEnvBaseURL); (baseURL == "") != (adhocValue(f.Model, adhocEnvModel) == "") {
			missing, present := "--llm-model", "--llm-base-url"
			if baseURL == "" {
				missing, present = "--llm-base-url", "--llm-model"
			}
			fmt.Fprintf(os.Stderr, "WARNING: ad-hoc provider pair is incomplete: %s is set but %s is not - the ad-hoc endpoint is INACTIVE and this run uses the regular config file instead. Set %s (or env %s) or clear the other half to avoid billing the wrong endpoint.\n", present, missing, missing, func() string {
				if missing == "--llm-base-url" {
					return adhocEnvBaseURL
				}
				return adhocEnvModel
			}())
		}
	}
	wd, _ := os.Getwd()
	return config.LoadWithInstance(cfgFile, wd)
}

func adhocInt(flagVal int, envKey string) int {
	if flagVal > 0 {
		return flagVal
	}
	if raw := strings.TrimSpace(os.Getenv(envKey)); raw != "" {
		v, err := strconv.Atoi(raw)
		switch {
		case err != nil:
			fmt.Fprintf(os.Stderr, "WARNING: ignoring invalid %s=%q (%v) - using the default instead\n", envKey, raw, err)
		case v <= 0:
			fmt.Fprintf(os.Stderr, "WARNING: ignoring invalid %s=%q (must be a positive integer) - using the default instead\n", envKey, raw)
		default:
			return v
		}
	}
	return 0
}

func adhocBool(flagVal bool, envKey string) bool {
	if flagVal {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envKey))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
