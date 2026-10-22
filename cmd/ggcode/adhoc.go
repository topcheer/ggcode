package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

// Ad-hoc provider mode (#4117): run pipe (-p) with a model + endpoint URL
// given directly on the command line or via GGCODE_LLM_* environment
// variables, with ZERO config-file I/O — nothing is read from or written to
// ggcode.yaml. The Config below is synthesized purely in memory as a single
// vendor ("adhoc") with a single endpoint.
//
// Sources (flag wins over env):
//   --llm-base-url          GGCODE_LLM_BASE_URL
//   --llm-model             GGCODE_LLM_MODEL
//   --llm-api-key           GGCODE_LLM_API_KEY
//   --llm-protocol          GGCODE_LLM_PROTOCOL     (default: inferred from URL)
//   --llm-context-window    GGCODE_LLM_CONTEXT_WINDOW
//   --llm-max-tokens        GGCODE_LLM_MAX_TOKENS
//   --llm-retry-always      GGCODE_LLM_RETRY_ALWAYS (true/1: retry EVERY LLM
//                          error indefinitely — auth/404/quota/overflow all
//                          retried; only Ctrl-C/cancellation stops; lifts the
//                          20-attempt and 2-minute backoff caps)
//
// Mode activates only when BOTH base URL and model are present (from either
// source combined). A partial pair is a user error and is reported as such.

const (
	adhocVendorName   = "adhoc"
	adhocEndpointName = "adhoc"
)

// adhocLLMFlags holds the --llm-* flag values registered in root.go.
type adhocLLMFlags struct {
	BaseURL       string
	Model         string
	APIKey        string `json:"-"`
	Protocol      string
	ContextWindow int
	MaxTokens     int
	RetryAlways   bool
}

// adhocActive reports whether ad-hoc mode is requested: base URL + model both
// present, each taken from the flag or, when the flag is empty, from the
// corresponding GGCODE_LLM_* environment variable.
func (f *adhocLLMFlags) adhocActive() bool {
	return adhocValue(f.BaseURL, adhocEnvBaseURL) != "" && adhocValue(f.Model, adhocEnvModel) != ""
}

// resolved returns flag if non-empty, else the environment value. flag is
// passed as pointer indirection via closure-free form: caller passes the env
// key and the flag field address is implicit in the wrapper below.
func adhocValue(flagVal, envKey string) string {
	if strings.TrimSpace(flagVal) != "" {
		return strings.TrimSpace(flagVal)
	}
	return strings.TrimSpace(os.Getenv(envKey))
}

// adhocEnv* keys follow the existing GGCODE_LLM_* family convention
// (GGCODE_LLM_TAPE in internal/provider/tape_provider.go).
const (
	adhocEnvBaseURL       = "GGCODE_LLM_BASE_URL"
	adhocEnvModel         = "GGCODE_LLM_MODEL"
	adhocEnvAPIKey        = "GGCODE_LLM_API_KEY"
	adhocEnvProtocol      = "GGCODE_LLM_PROTOCOL"
	adhocEnvContextWindow = "GGCODE_LLM_CONTEXT_WINDOW"
	adhocEnvMaxTokens     = "GGCODE_LLM_MAX_TOKENS"
	adhocEnvRetryAlways   = "GGCODE_LLM_RETRY_ALWAYS"
)

// synthesizeAdhocConfig builds the in-memory Config for ad-hoc mode. It must
// never touch the filesystem: no load, no save path is viable later either,
// because root.go passes cfgFile="" which disables the hot-reload watcher and
// leaves the config tool without a write target.
func (f *adhocLLMFlags) synthesizeAdhocConfig() (*config.Config, error) {
	baseURL := adhocValue(f.BaseURL, adhocEnvBaseURL)
	model := adhocValue(f.Model, adhocEnvModel)
	apiKey := adhocValue(f.APIKey, adhocEnvAPIKey)
	protocol := adhocValue(f.Protocol, adhocEnvProtocol)

	if baseURL == "" || model == "" {
		// Partial pair: explain both sides so the user can fix either.
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

// adhocInferProtocol guesses the wire protocol from URL shape so users of
// mainstream providers do not need --llm-protocol. Anything unrecognized
// defaults to the OpenAI-compatible chat completions protocol — the de-facto
// standard for gateways and local inference servers.
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

// adhocLLM is the package-level flag holder wired in root.go flag
// registration and consumed by loadConfigForRun below.
var adhocLLM adhocLLMFlags

// loadConfigForRun returns the run's Config: the synthesized in-memory config
// when ad-hoc mode is active (config file is NEVER opened), or the regular
// file-loaded config otherwise.
func loadConfigForRun(cfgFile string, f *adhocLLMFlags) (*config.Config, error) {
	if f.adhocActive() {
		// Retry-always is an ad-hoc-mode-only escape hatch (the user owns the
		// endpoint and knows its flakiness); it must not leak into normal
		// file-config runs.
		if adhocBool(f.RetryAlways, adhocEnvRetryAlways) {
			provider.SetRetryAlways(true)
		}
		return f.synthesizeAdhocConfig()
	}
	// #4216: a HALF-SET pair (base-url without model or vice versa, flag or
	// env) used to fall through to the regular config completely silently -
	// unattended pipes ran on ggcode.yaml's default provider while the user
	// believed they were testing a new endpoint. The unreachable
	// synthesizeAdhocConfig error branch was the only diagnostic ever
	// written. Surface the partial pair loudly at the fallback point
	// instead (stderr survives pipe mode; hard-failing here would change
	// CLI behavior for users who set stray GGCODE_LLM_* vars).
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

// adhocInt resolves an int override: flag (non-zero) wins over env.
func adhocInt(flagVal int, envKey string) int {
	if flagVal > 0 {
		return flagVal
	}
	if raw := strings.TrimSpace(os.Getenv(envKey)); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			return v
		}
	}
	return 0
}

// adhocBool resolves a bool override: flag (true) wins over env. Env accepts
// the common truthy spellings (1/true/yes/on, case-insensitive).
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
