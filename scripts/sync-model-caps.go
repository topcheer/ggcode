//go:build ignore

// sync-model-caps fetches model capability data from the charmbracelet/catwalk
// repository and generates the knownModelCapabilities map for context_window.go.
//
// Preferred invocation: `make sync-model-caps` (gofmt + build check included).
// Direct usage with flags:
//
//	go run scripts/sync-model-caps.go [--dry-run] [--output FILE]
//
// Flags:
//
//	--dry-run    Print generated Go code to stdout without writing any file
//	--output     Output file path (default: internal/config/context_window.go)
//	             vendor_defaults.go is always rewritten alongside.
//
// This tool is NOT run in CI. Run it any time via `make sync-model-caps`;
// it is a required pre-release step (docs/release-process.md §3.3).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
)

const catwalkBaseURL = "https://raw.githubusercontent.com/charmbracelet/catwalk/main/internal/providers/configs/"

// catwalkProvider represents a provider config JSON from catwalk.
type catwalkProvider struct {
	ID                  string         `json:"id"`
	Name                string         `json:"name"`
	Type                string         `json:"type"`
	APIEndpoint         string         `json:"api_endpoint"`
	DefaultLargeModelID string         `json:"default_large_model_id"`
	DefaultSmallModelID string         `json:"default_small_model_id"`
	Models              []catwalkModel `json:"models"`
	// ExtraEndpoints carries locally maintained endpoint URLs that upstream
	// catwalk does not list. Never serialized from JSON; used only for
	// localProviders so their URLs enter the vendorAPIEndpoints match table.
	ExtraEndpoints []string
}

type catwalkModel struct {
	ID                  string  `json:"id"`
	Name                string  `json:"name"`
	ContextWindow       int     `json:"context_window"`
	DefaultMaxTokens    int     `json:"default_max_tokens"`
	SupportsAttachments bool    `json:"supports_attachments"`
	CanReason           bool    `json:"can_reason"`
	CostPer1mIn         float64 `json:"cost_per_1m_in"`
	CostPer1mOut        float64 `json:"cost_per_1m_out"`
}

// modelEntry is our output format — one line in the Go map literal.
type modelEntry struct {
	ID              string
	ContextWindow   int
	MaxOutputTokens int
	SupportsVision  bool
	SourceProvider  string // catwalk provider ID (e.g. "openai", "minimax")
}

// Which catwalk config files we care about.
// Maps catwalk filename → our vendor section header.
var desiredConfigs = map[string]string{
	"openai.json":        "OpenAI",
	"anthropic.json":     "Anthropic Claude",
	"gemini.json":        "Google Gemini",
	"deepseek.json":      "DeepSeek",
	"groq.json":          "Groq",
	"xai.json":           "xAI Grok",
	"copilot.json":       "GitHub Copilot",
	"openrouter.json":    "OpenRouter",
	"vercel.json":        "Vercel AI Gateway",
	"zai.json":           "Z.ai",
	"zhipu.json":         "Zhipu GLM",
	"zhipu-coding.json":  "Zhipu GLM (Coding)",
	"kimi.json":          "Moonshot / Kimi",
	"minimax.json":       "MiniMax",
	"minimax-china.json": "MiniMax China",
	"bedrock.json":       "AWS Bedrock",
	"azure.json":         "Azure OpenAI",
	"vertexai.json":      "Google Vertex AI",
	"huggingface.json":   "HuggingFace",
	"venice.json":        "Venice",
	"nebius.json":        "Nebius",
	"cerebras.json":      "Cerebras",
	"chutes.json":        "Chutes",
}

/*
localProviders carries ggcode-local vendor definitions that upstream catwalk
does not ship (e.g. "xiaomi-mimo", added manually in v1.3.154). Regeneration
used to wipe them whenever catwalk dropped or never had the provider.
During sync these are merged back: if upstream starts carrying the same
provider ID, upstream data wins. Capability-less models (e.g. TTS-only) are
kept in the vendor model list but skipped in the capability table.
*/
var localProviders = []*catwalkProvider{
	{
		ID:                  "xiaomi-mimo",
		Name:                "XiaoMi MIMO",
		DefaultLargeModelID: "MiMo-V2.5-Pro",
		DefaultSmallModelID: "MiMo-V2.5",
		ExtraEndpoints:      []string{"https://token-plan-cn.xiaomimimo.com/v1", "https://token-plan-cn.xiaomimimo.com/anthropic"},
		Models: []catwalkModel{
			{ID: "MiMo-V2.5-Pro", ContextWindow: 1000000, DefaultMaxTokens: 65536},
			{ID: "MiMo-V2.5", ContextWindow: 1000000, DefaultMaxTokens: 65536, SupportsAttachments: true},
			{ID: "MiMo-V2.5-TTS-VoiceClone"},
			{ID: "MiMo-V2.5-TTS-VoiceDesign"},
			{ID: "MiMo-V2.5-TTS"},
			{ID: "MiMo-V2-Pro", ContextWindow: 1000000},
			{ID: "MiMo-V2-Omni", SupportsAttachments: true},
			{ID: "MiMo-V2-TTS"},
		},
	},
}

/*
builtinEndpointFallback fills providers whose upstream api_endpoint is an
env-var placeholder (e.g. anthropic's "$ANTHROPIC_API_ENDPOINT") with the
corresponding builtin URL from config.go's builtin vendor definitions.
Matching-only reference: builtin endpoint URLs themselves are never
rewritten. Azure is omitted on purpose — its per-deployment hosts
(*.openai.azure.com) cannot be statically listed.
*/
var builtinEndpointFallback = map[string][]string{
	"anthropic": {"https://api.anthropic.com"},
}

func main() {
	dryRun := flag.Bool("dry-run", false, "Print to stdout instead of writing file")
	output := flag.String("output", "internal/config/context_window.go", "Output file path")
	flag.Parse()

	// 1. Fetch all config files.
	var allEntries []modelEntry
	var sections []string            // ordered section names for output
	var providers []*catwalkProvider // save for vendor_defaults.go

	// Sort config names for deterministic output order.
	configNames := make([]string, 0, len(desiredConfigs))
	for name := range desiredConfigs {
		configNames = append(configNames, name)
	}
	sort.Strings(configNames)

	for _, configName := range configNames {
		sectionHeader := desiredConfigs[configName]
		fmt.Fprintf(os.Stderr, "Fetching %s ...\n", configName)

		provider, err := fetchProvider(configName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  WARNING: %v\n", err)
			continue
		}

		var sectionEntries []modelEntry
		for _, m := range provider.Models {
			if m.ContextWindow <= 0 {
				continue
			}
			e := modelEntry{
				ID:              strings.ToLower(strings.TrimSpace(m.ID)),
				ContextWindow:   m.ContextWindow,
				MaxOutputTokens: m.DefaultMaxTokens,
				SupportsVision:  m.SupportsAttachments,
				SourceProvider:  provider.ID, // track origin for dedup
			}
			sectionEntries = append(sectionEntries, e)
		}

		if len(sectionEntries) == 0 {
			continue
		}

		sections = append(sections, sectionHeader)
		allEntries = append(allEntries, sectionEntries...)
		providers = append(providers, provider)
	}

	fmt.Fprintf(os.Stderr, "\nTotal models: %d\n\n", len(allEntries))

	// 1b. OpenRouter fallback: for vendors not in catwalk, fetch from OpenRouter /v1/models.
	openRouterFallback := map[string][]string{
		// ggcode vendor → OpenRouter provider prefixes
		"mistral":    {"mistralai/"},
		"perplexity": {"perplexity/"},
		"moonshot":   {"moonshotai/"},
		"nvidia":     {"nvidia/"},
		"ark":        {"bytedance/", "bytedance-seed/"},
		"aliyun":     {"qwen/"},
	}

	// Check which catwalk providers we already have.
	catwalkProviderIDs := make(map[string]bool)
	for _, p := range providers {
		catwalkProviderIDs[p.ID] = true
	}

	// Only fetch OpenRouter if we need any fallback vendors.
	needFallback := false
	for vendor := range openRouterFallback {
		catwalkID := vendorToCatwalkID(vendor)
		if !catwalkProviderIDs[catwalkID] {
			needFallback = true
			break
		}
	}

	if needFallback {
		fmt.Fprintf(os.Stderr, "Fetching OpenRouter models as fallback...\n")
		orModels, err := fetchOpenRouterModels()
		if err != nil {
			fmt.Fprintf(os.Stderr, "  WARNING: OpenRouter fetch failed: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "  OpenRouter returned %d models\n", len(orModels))
			for vendor, prefixes := range openRouterFallback {
				catwalkID := vendorToCatwalkID(vendor)
				if catwalkProviderIDs[catwalkID] {
					continue // already have from catwalk
				}
				var vendorModels []catwalkModel
				for _, m := range orModels {
					for _, prefix := range prefixes {
						if strings.HasPrefix(m.ID, prefix) {
							// Strip provider prefix for the model ID.
							stripped := strings.TrimPrefix(m.ID, prefix)
							vendorModels = append(vendorModels, catwalkModel{
								ID:                  stripped,
								ContextWindow:       m.ContextLength,
								DefaultMaxTokens:    m.MaxOutputTokens,
								SupportsAttachments: strings.Contains(m.Architecture.Modality, "image"),
							})
							// Also add with prefix for context_window.go exact match.
							allEntries = append(allEntries, modelEntry{
								ID:              strings.ToLower(m.ID),
								ContextWindow:   m.ContextLength,
								MaxOutputTokens: m.MaxOutputTokens,
								SupportsVision:  strings.Contains(m.Architecture.Modality, "image"),
								SourceProvider:  vendor,
							})
							break
						}
					}
				}
				if len(vendorModels) > 0 {
					fmt.Fprintf(os.Stderr, "  %s: %d models from OpenRouter\n", vendor, len(vendorModels))
					providers = append(providers, &catwalkProvider{
						ID:     vendor,
						Name:   vendor,
						Models: vendorModels,
					})
				}
			}
		}
	}

	// 1c. Merge local-only providers (see localProviders doc comment).
	for _, lp := range localProviders {
		upstream := false
		for _, p := range providers {
			if p.ID == lp.ID {
				upstream = true
				break
			}
		}
		if upstream {
			continue // upstream carries it now; upstream data wins
		}
		providers = append(providers, lp)
		sections = append(sections, lp.Name)
		for _, m := range lp.Models {
			if m.ContextWindow == 0 && m.DefaultMaxTokens == 0 && !m.SupportsAttachments {
				continue // no capability signal; keep out of the caps table
			}
			allEntries = append(allEntries, modelEntry{
				ID:              strings.ToLower(strings.TrimSpace(m.ID)),
				ContextWindow:   m.ContextWindow,
				MaxOutputTokens: m.DefaultMaxTokens,
				SupportsVision:  m.SupportsAttachments,
				SourceProvider:  lp.ID,
			})
		}
		fmt.Fprintf(os.Stderr, "  %s: %d models (local override)\n", lp.ID, len(lp.Models))
	}

	fmt.Fprintf(os.Stderr, "\nFinal total models: %d\n\n", len(allEntries))

	// 2. Generate Go source code.
	code := generateGoCode(allEntries, sections)

	// 3. Write output.
	if *dryRun {
		fmt.Print(code)
	} else {
		if err := os.WriteFile(*output, []byte(code), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", *output, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Written to %s\n", *output)
	}

	// 4. Generate vendor_defaults.go
	vendorDefaultsPath := "internal/config/vendor_defaults.go"
	if idx := strings.LastIndex(*output, "/"); idx >= 0 {
		vendorDefaultsPath = (*output)[:idx+1] + "vendor_defaults.go"
	}
	vdCode := generateVendorDefaults(providers)
	if *dryRun {
		fmt.Print(vdCode)
	} else {
		if err := os.WriteFile(vendorDefaultsPath, []byte(vdCode), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", vendorDefaultsPath, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Written to %s\n", vendorDefaultsPath)
	}
}

func fetchProvider(filename string) (*catwalkProvider, error) {
	url := catwalkBaseURL + filename
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("fetch %s: HTTP %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filename, err)
	}

	var provider catwalkProvider
	if err := json.Unmarshal(body, &provider); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filename, err)
	}
	return &provider, nil
}

func generateGoCode(entries []modelEntry, sections []string) string {
	var sb strings.Builder

	sb.WriteString(`package config

import (
	"regexp"
	"strconv"
	"strings"
)

const defaultContextWindow = 128000
const defaultMaxOutputTokens = 16384

type modelCapability struct {
	ContextWindow   int
	MaxOutputTokens int
	SupportsVision  bool
}

// knownModelCapabilities maps exact model name (lowercased) to its capabilities.
// Auto-generated by: go run scripts/sync-model-caps.go
// Source: https://github.com/charmbracelet/catwalk/tree/main/internal/providers/configs
`)

	// We don't regenerate the full map literal with sections because the
	// surrounding code (inferContextWindow, etc.) has manual prefix-based
	// fallbacks that should be preserved. Instead, we generate ONLY the
	// knownModelCapabilities map as a data file, and the rest of context_window.go
	// should import it.
	//
	// Actually, the simplest approach: generate the entire file including
	// the static code that doesn't change.

	// Generate knownModelCapabilities map
	sb.WriteString("var knownModelCapabilities = map[string]modelCapability{\n")

	// Group by section for readability
	entryIdx := 0
	for _, section := range sections {
		// Find how many entries belong to this section
		// We just output all entries sorted by ID
		_ = section
	}

	// Deduplicate: multiple providers may carry the same model.
	entries = dedupEntries(entries)

	// Output entries grouped loosely — just output sorted with comments
	// Actually let's just output them sorted
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("\t%q: {ContextWindow: %d", e.ID, e.ContextWindow))
		if e.MaxOutputTokens > 0 {
			sb.WriteString(fmt.Sprintf(", MaxOutputTokens: %d", e.MaxOutputTokens))
		}
		if e.SupportsVision {
			sb.WriteString(", SupportsVision: true")
		}
		sb.WriteString("},\n")
	}

	sb.WriteString("}\n\n")

	// Append the static code that doesn't change
	sb.WriteString(staticCode())

	_ = entryIdx
	_ = sections

	return sb.String()
}

func staticCode() string {
	return `var contextWindowHintPattern = regexp.MustCompile(` + "`" + `(^|[^0-9])(\d+)(k|m)($|[^a-z0-9])` + "`" + `)

// inferContextWindow resolves an approximate input context window.
// Explicit endpoint config should override this; this heuristic exists so
// auto-compaction can track common models more accurately than a fixed 128k.
func inferContextWindow(model, protocol string) int {
	if cap, ok := lookupModelCapability(model); ok && cap.ContextWindow > 0 {
		return cap.ContextWindow
	}
	if hinted := parseContextWindowHint(model); hinted > 0 {
		return hinted
	}

	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	// ─── Anthropic Claude ─────────────────────────────────────────
	case strings.Contains(m, "claude"):
		return 200000

	// ─── Google Gemini ────────────────────────────────────────────
	case strings.Contains(m, "gemini-1.5-pro"):
		return 2_000_000
	case strings.Contains(m, "gemini"):
		return 1_000_000

	// ─── OpenAI ───────────────────────────────────────────────────
	case strings.Contains(m, "gpt-5"),
		strings.Contains(m, "gpt-4.1"):
		return 1_000_000
	case strings.Contains(m, "gpt-4o"),
		strings.Contains(m, "gpt-4-turbo"):
		return 128000
	case strings.Contains(m, "o3"),
		strings.Contains(m, "o4-mini"):
		return 200000

	// ─── xAI Grok ────────────────────────────────────────────────
	case strings.Contains(m, "grok"):
		return 200000

	// ─── DeepSeek ─────────────────────────────────────────────────
	case strings.Contains(m, "deepseek"):
		return 128000

	// ─── Mistral ──────────────────────────────────────────────────
	case strings.Contains(m, "codestral"):
		return 256000
	case strings.Contains(m, "mistral-large"):
		return 128000
	case strings.Contains(m, "mistral"):
		return 32000

	// ─── Zhipu GLM ───────────────────────────────────────────────
	case strings.Contains(m, "glm-4-long"):
		return 1_000_000
	case strings.Contains(m, "glm-"):
		return 128000

	// ─── Moonshot / Kimi ─────────────────────────────────────────
	case strings.Contains(m, "kimi-k2"),
		strings.Contains(m, "kimi-k2.5"):
		return 262144
	case strings.Contains(m, "moonshot-v1-128k"),
		strings.Contains(m, "kimi"):
		return 131072
	case strings.Contains(m, "moonshot"):
		return 32768

	// ─── MiniMax ──────────────────────────────────────────────────
	case strings.Contains(m, "minimax-m1"),
		strings.Contains(m, "minimax-m2"),
		strings.Contains(m, "minimax-01"):
		return 1_000_192
	case strings.Contains(m, "minimax"):
		return 204800

	// ─── Doubao / Ark ─────────────────────────────────────────────
	case strings.Contains(m, "doubao"),
		strings.Contains(m, "ark-code"):
		return 200000

	// ─── Perplexity ──────────────────────────────────────────────
	case strings.Contains(m, "sonar-pro"):
		return 200000
	case strings.Contains(m, "sonar"):
		return 128000

	// ─── Meta Llama 4 ────────────────────────────────────────────
	case strings.Contains(m, "llama-4-scout"):
		return 10_000_000
	case strings.Contains(m, "llama-4"):
		return 1_000_000
	// ─── Meta Llama 3.x ─────────────────────────────────────────
	case strings.Contains(m, "llama-3"):
		return 128000

	// ─── Qwen ────────────────────────────────────────────────────
	case strings.Contains(m, "qwen-long"):
		return 1_000_000
	case strings.Contains(m, "qwen"):
		return 131072

	// ─── Groq hosted models ──────────────────────────────────────
	case strings.Contains(m, "mixtral-8x7b"):
		return 32768
	case strings.Contains(m, "gemma"):
		return 8192
	}

	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "anthropic":
		return 200000
	case "gemini":
		return 1_000_000
	default:
		return defaultContextWindow
	}
}

func inferMaxOutputTokens(model, protocol string) int {
	if cap, ok := lookupModelCapability(model); ok && cap.MaxOutputTokens > 0 {
		return cap.MaxOutputTokens
	}

	switch strings.ToLower(strings.TrimSpace(protocol)) {
	default:
		return defaultMaxOutputTokens
	}
}

func inferVisionSupport(model, protocol string) bool {
	if cap, ok := lookupModelCapability(model); ok && cap.SupportsVision {
		return true
	}

	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(m, "claude"),
		strings.Contains(m, "gpt"),
		strings.Contains(m, "gemini"),
		strings.Contains(m, "gemma"),
		strings.Contains(m, "grok"),
		strings.Contains(m, "seed-2"),
		strings.Contains(m, "qwen3.5"),
		strings.Contains(m, "qwen-3.5"),
		strings.Contains(m, "qwen3.6"),
		strings.Contains(m, "qwen-3.6"),
		(strings.Contains(m, "glm-") && strings.Contains(m, "v")),
		strings.Contains(m, "kimi-2.5"),
		strings.Contains(m, "kimi-k2"),
		strings.Contains(m, "kimi-vl"):
		return true
	case strings.Contains(m, "glm-"),
		strings.Contains(m, "kimi"),
		strings.Contains(m, "deepseek"),
		strings.Contains(m, "mistral"),
		strings.Contains(m, "qwen"),
		strings.Contains(m, "moonshot"),
		strings.Contains(m, "minimax"),
		strings.Contains(m, "llama"):
		return false
	}

	return strings.EqualFold(strings.TrimSpace(protocol), "gemini")
}

func lookupModelCapability(model string) (modelCapability, bool) {
	cap, ok := knownModelCapabilities[strings.ToLower(strings.TrimSpace(model))]
	return cap, ok
}

func parseContextWindowHint(model string) int {
	matches := contextWindowHintPattern.FindAllStringSubmatch(strings.ToLower(model), -1)
	best := 0
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		n, err := strconv.Atoi(match[2])
		if err != nil || n <= 0 {
			continue
		}
		switch match[3] {
		case "k":
			n *= 1000
		case "m":
			n *= 1000000
		}
		if n > best {
			best = n
		}
	}
	return best
}
`
}

func generateVendorDefaults(providers []*catwalkProvider) string {
	var sb strings.Builder

	sb.WriteString(`package config

// Code generated by scripts/sync-model-caps.go. DO NOT EDIT.
// Source: https://github.com/charmbracelet/catwalk/tree/main/internal/providers/configs

import (
	"net/url"
	"sort"
	"strings"
)

type vendorModelInfo struct {
	Models []string
}

type defaultModelInfo struct {
	LargeModel string
	SmallModel string
}

// vendorModels maps provider ID to its available model list.
var vendorModels = map[string]vendorModelInfo{
`)

	// Sort providers by ID for deterministic output.
	sort.Slice(providers, func(i, j int) bool {
		return providers[i].ID < providers[j].ID
	})

	for _, p := range providers {
		if len(p.Models) == 0 {
			continue
		}
		ids := make([]string, 0, len(p.Models))
		for _, m := range p.Models {
			ids = append(ids, m.ID)
		}
		sb.WriteString(fmt.Sprintf("\t%q: {Models: []string{\n", p.ID))
		for _, id := range ids {
			sb.WriteString(fmt.Sprintf("\t\t%q,\n", id))
		}
		sb.WriteString("\t}},\n")
	}

	sb.WriteString(`}

// vendorDefaultModels maps provider ID to its default model IDs.
var vendorDefaultModels = map[string]defaultModelInfo{
`)

	for _, p := range providers {
		if p.DefaultLargeModelID == "" && p.DefaultSmallModelID == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("\t%q: {LargeModel: %q, SmallModel: %q},\n",
			p.ID, p.DefaultLargeModelID, p.DefaultSmallModelID))
	}

	sb.WriteString(`}

// vendorAPIEndpointHosts maps a lowercased URL host to its provider ID.
// Flattened (expanded) from each provider's known API base URLs — upstream
// catwalk api_endpoint plus locally maintained extras — so matching is a
// single map lookup. Read-only data: builtin endpoint URLs in config.go are
// never rewritten from this. When several providers share a host, the
// lexicographically smallest ID wins (resolved at generation time).
var vendorAPIEndpointHosts = map[string]string{
`)

	// Flatten: host -> provider ID, smallest ID wins on conflict.
	hostToPID := make(map[string]string)
	for _, p := range providers {
		cands := append([]string{p.APIEndpoint}, p.ExtraEndpoints...)
		cands = append(cands, builtinEndpointFallback[p.ID]...)
		for _, cand := range cands {
			// Skip env-var placeholders like "$ANTHROPIC_API_ENDPOINT".
			if !strings.HasPrefix(cand, "http://") && !strings.HasPrefix(cand, "https://") {
				continue
			}
			u, err := url.Parse(cand)
			if err != nil || u.Host == "" {
				continue
			}
			host := strings.ToLower(u.Hostname())
			if prev, ok := hostToPID[host]; !ok || p.ID < prev {
				hostToPID[host] = p.ID
			}
		}
	}
	hosts := make([]string, 0, len(hostToPID))
	for h := range hostToPID {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	for _, h := range hosts {
		sb.WriteString(fmt.Sprintf("\t%q: %q,\n", h, hostToPID[h]))
	}

	sb.WriteString(`}

// lookupVendorModels returns the model list for a given provider ID.
// Returns nil if the provider is unknown (caller should use /v1/models API as fallback).
func lookupVendorModels(providerID string) []string {
	info, ok := vendorModels[providerID]
	if !ok {
		return nil
	}
	return info.Models
}

// lookupVendorDefaultModel returns the default large model ID for a provider.
// Returns empty string if unknown.
func lookupVendorDefaultModel(providerID string) string {
	info, ok := vendorDefaultModels[providerID]
	if !ok {
		return ""
	}
	return info.LargeModel
}

// matchProviderByBaseURL returns the provider ID whose known API endpoint
// host equals the host of baseURL (e.g. "https://api.z.ai/api/coding/paas/v4"
// -> "zai"). Empty string when nothing matches.
func matchProviderByBaseURL(baseURL string) string {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Host == "" {
		return ""
	}
	return vendorAPIEndpointHosts[strings.ToLower(u.Hostname())]
}

// firstNonEmptyBaseURL returns the first endpoint BaseURL of a vendor,
// endpoints visited in sorted name order for determinism.
func firstNonEmptyBaseURL(vc VendorConfig) string {
	names := make([]string, 0, len(vc.Endpoints))
	for n := range vc.Endpoints {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if vc.Endpoints[n].BaseURL != "" {
			return vc.Endpoints[n].BaseURL
		}
	}
	return ""
}

// populateDefaultModels fills endpoint Models lists from the catwalk/OpenRouter data.
// Only sets Models on endpoints that don't already have user-defined models.
func populateDefaultModels(cfg *Config) {
	vendorToCatwalk := map[string][]string{
		"zai":           {"zai", "zhipu-coding"},
		"zhipu":         {"zhipu"},
		"anthropic":     {"anthropic"},
		"openai":        {"openai"},
		"google":        {"gemini"},
		"openrouter":    {"openrouter"},
		"groq":          {"groq"},
		"mistral":       {"mistral"},
		"deepseek":      {"deepseek"},
		"moonshot":      {"kimi", "moonshot"},
		"kimi":          {"kimi"},
		"minimax":       {"minimax", "minimax-china"},
		"perplexity":    {"perplexity"},
		"github-copilot": {"copilot"},
		"xiaomi-mimo":   {"xiaomi-mimo"},
		"xai":           {"xai"},
		"together":      {"together"},
		"nvidia":        {"nvidia"},
		"ark":           {"ark"},
		"aliyun":        {"aliyun"},
	}

	for vendorName, vc := range cfg.Vendors {
		catwalkIDs, ok := vendorToCatwalk[vendorName]
		if !ok {
			// Attribute unknown vendors to a provider by endpoint URL host so
			// custom endpoints pointing at a known provider (e.g. a user-added
			// vendor with base_url on api.z.ai) still receive that provider's
			// model list. Read-only: builtin URLs in config.go are untouched.
			if pid := matchProviderByBaseURL(firstNonEmptyBaseURL(vc)); pid != "" {
				catwalkIDs = []string{pid}
			} else {
				continue
			}
		}
		for epName, ep := range vc.Endpoints {
			if len(ep.Models) > 0 {
				continue
			}
			var models []string
			for _, cid := range catwalkIDs {
				if m := lookupVendorModels(cid); len(m) > 0 {
					models = append(models, m...)
				}
			}
			if len(models) > 0 {
				ep.Models = models
				vc.Endpoints[epName] = ep
			}
		}
		cfg.Vendors[vendorName] = vc
	}
}
`)

	return sb.String()
}

func dedupEntries(allEntries []modelEntry) []modelEntry {
	// Phase 1: group by lowercase model ID.
	groups := make(map[string][]modelEntry)
	for _, e := range allEntries {
		groups[e.ID] = append(groups[e.ID], e)
	}

	// Phase 2: for each group, pick the best entry.
	dedup := make(map[string]modelEntry, len(groups))
	for id, group := range groups {
		if len(group) == 1 {
			dedup[id] = group[0]
			continue
		}

		// Heuristic: prefer the entry whose SourceProvider appears in the model ID.
		// E.g. "minimax-m2.7" should prefer the entry from provider "minimax",
		// not from "openrouter" or "fireworks".
		var best modelEntry
		bestScore := -1
		for _, e := range group {
			score := 0
			// Strongly prefer: source provider name is a prefix of the model ID.
			if strings.HasPrefix(e.ID, e.SourceProvider) ||
				strings.Contains(e.ID, e.SourceProvider) {
				score += 100
			}
			// Weakly prefer: larger context window (more accurate).
			if e.ContextWindow > best.ContextWindow {
				score += 1
			}
			if score > bestScore {
				bestScore = score
				best = e
			}
		}
		dedup[id] = best
	}

	entries := make([]modelEntry, 0, len(dedup))
	for _, e := range dedup {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ID < entries[j].ID
	})
	return entries
}

// openRouterModel represents a model from the OpenRouter /v1/models API.
type openRouterModel struct {
	ID              string `json:"id"`
	ContextLength   int    `json:"context_length"`
	MaxOutputTokens int    `json:"max_output_tokens"`
	Architecture    struct {
		Modality string `json:"modality"`
	} `json:"architecture"`
}

type openRouterResponse struct {
	Data []openRouterModel `json:"data"`
}

func fetchOpenRouterModels() ([]openRouterModel, error) {
	resp, err := http.Get("https://openrouter.ai/api/v1/models")
	if err != nil {
		return nil, fmt.Errorf("fetch OpenRouter: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read OpenRouter: %w", err)
	}

	var orResp openRouterResponse
	if err := json.Unmarshal(body, &orResp); err != nil {
		return nil, fmt.Errorf("parse OpenRouter: %w", err)
	}

	// Convert to catwalkModel-compatible format.
	var models []openRouterModel
	for _, m := range orResp.Data {
		if m.ContextLength <= 0 {
			continue
		}
		models = append(models, m)
	}
	return models, nil
}

// vendorToCatwalkID maps a ggcode vendor name to its primary catwalk provider ID.
func vendorToCatwalkID(vendor string) string {
	m := map[string]string{
		"zai":            "zai",
		"zhipu":          "zhipu",
		"anthropic":      "anthropic",
		"openai":         "openai",
		"google":         "gemini",
		"openrouter":     "openrouter",
		"groq":           "groq",
		"mistral":        "mistral",
		"deepseek":       "deepseek",
		"moonshot":       "kimi",
		"kimi":           "kimi",
		"minimax":        "minimax",
		"perplexity":     "perplexity",
		"github-copilot": "copilot",
		"xai":            "xai",
		"together":       "together",
		"nvidia":         "nvidia",
		"ark":            "ark",
		"aliyun":         "aliyun",
	}
	if id, ok := m[vendor]; ok {
		return id
	}
	return vendor
}
