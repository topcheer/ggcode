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

// adhocEnvTouched reports whether any GGCODE_LLM_* provider variable is set
// (used for the partial-pair diagnostic: env half-set deserves a hint).
func adhocEnvTouched() bool {
	for _, k := range []string{adhocEnvBaseURL, adhocEnvModel, adhocEnvAPIKey, adhocEnvProtocol, adhocEnvContextWindow, adhocEnvMaxTokens, adhocEnvRetryAlways} {
		if strings.TrimSpace(os.Getenv(k)) != "" {
			return true
		}
	}
	return false
}

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
	// Retry-always is a per-run escape hatch (the user owns the endpoint and
	// knows its flakiness). It must activate whenever the flag/env is set -
	// including plain -p pipe runs against the CONFIG-FILE provider. Gating
	// it behind adhocActive() (which requires --llm-base-url AND --llm-model)
	// silently disabled it for exactly the batch callers who passed only the
	// flag: their 401s terminated on the first attempt with no retry at all.
	// It must not leak into interactive TUI/daemon sessions: those surfaces
	// never set the flag, and the env var is opt-in per process.
	if adhocBool(f.RetryAlways, adhocEnvRetryAlways) {
		provider.SetRetryAlways(true)
	}
	if f.adhocActive() {
		return f.synthesizeAdhocConfig()
	}
	// Enforce the file-header contract (#4216): a half-set pair must be a
	// hard error, not a silent fallthrough to the config-file provider —
	// the batch would run against the wrong endpoint/model with no clue.
	if err := f.checkPartialPair(); err != nil {
		return nil, err
	}
	wd, _ := os.Getwd()
	return config.LoadWithInstance(cfgFile, wd)
}

// checkPartialPair rejects exactly-one-of {base URL, model} (#4216). When
// neither is set, auxiliary --llm-* values (api-key/protocol/context-window/
// max-tokens/retry-always, via flag or GGCODE_LLM_* env) are tolerated — the
// leniency is intentional — but the user still gets a stderr hint instead of
// the old zero-feedback behavior.
func (f *adhocLLMFlags) checkPartialPair() error {
	baseURL := adhocValue(f.BaseURL, adhocEnvBaseURL)
	model := adhocValue(f.Model, adhocEnvModel)
	switch {
	case baseURL != "" && model == "":
		return fmt.Errorf(
			"ad-hoc provider mode needs BOTH a base URL and a model: --llm-base-url (or %s) is set but no model was given (use --llm-model or %s); refusing to silently fall back to the config-file provider (#4216)",
			adhocEnvBaseURL, adhocEnvModel)
	case baseURL == "" && model != "":
		return fmt.Errorf(
			"ad-hoc provider mode needs BOTH a base URL and a model: --llm-model (or %s) is set but no base URL was given (use --llm-base-url or %s); refusing to silently fall back to the config-file provider (#4216)",
			adhocEnvModel, adhocEnvBaseURL)
	}
	// Neither side of the pair: warn when any adhoc-ish source is set so the
	// user learns the settings are inert without the pair.
	orphanFlags := f.APIKey != "" || f.Protocol != "" || f.ContextWindow > 0 || f.MaxTokens > 0 || f.RetryAlways
	if orphanFlags || adhocEnvTouched() {
		fmt.Fprintf(os.Stderr, "[ggcode] note: ignoring --llm-* / %s settings: ad-hoc provider mode activates only when BOTH --llm-base-url and --llm-model are set\n", "GGCODE_LLM_*")
	}
	return nil
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
