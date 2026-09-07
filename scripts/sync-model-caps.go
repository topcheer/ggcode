//go:build ignore

// sync-model-caps fetches model capability data from models.dev (api.json,
// MIT-licensed, https://github.com/anomalyco/models.dev) and generates the
// knownModelCapabilities map for context_window.go plus vendor_defaults.go.
// Single source since v1.3.234 - it replaced the former charmbracelet/catwalk
// fetch and the OpenRouter /v1/models fallback.
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

const modelsDevAPIURL = "https://models.dev/api.json"

// catwalkProvider is the internal provider representation the generator
// emits (name kept from the catwalk era; data now comes from models.dev).
type catwalkProvider struct {
	ID                  string         `json:"id"`
	Name                string         `json:"name"`
	Type                string         `json:"type"`
	APIEndpoint         string         `json:"api_endpoint"`
	DefaultLargeModelID string         `json:"default_large_model_id"`
	DefaultSmallModelID string         `json:"default_small_model_id"`
	Models              []catwalkModel `json:"models"`
	// ExtraEndpoints carries locally maintained endpoint URLs that upstream
	// does not list. Never serialized from JSON; used only for localProviders
	// so their URLs enter the vendorAPIEndpoints match table.
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
	// release_date drives the default-large-model heuristic (newest wins).
	// Only populated from models.dev; not serialized into the output.
	ReleaseDate string `json:"-"`
}

// modelEntry is our output format — one line in the Go map literal.
type modelEntry struct {
	ID              string
	ContextWindow   int
	MaxOutputTokens int
	SupportsVision  bool
	SourceProvider  string // models.dev provider ID (e.g. "openai", "minimax")
}

// Which models.dev providers we sync (provider ID → our section header).
// models.dev carries 200+ providers; this list keeps the vendors ggcode
// ships plus the major ones users point custom endpoints at.
var desiredProviders = map[string]string{
	"alibaba":                  "Alibaba (International)",
	"alibaba-cn":               "Alibaba Cloud (DashScope)",
	"amazon-bedrock":           "AWS Bedrock",
	"anthropic":                "Anthropic Claude",
	"azure":                    "Azure OpenAI",
	"azure-cognitive-services": "Azure AI",
	"cerebras":                 "Cerebras",
	"chutes":                   "Chutes",
	"cloudflare-workers-ai":    "Cloudflare Workers AI",
	"cohere":                   "Cohere",
	"databricks":               "Databricks",
	"deepseek":                 "DeepSeek",
	"fireworks-ai":             "Fireworks AI",
	"github-copilot":           "GitHub Copilot",
	"google":                   "Google Gemini",
	"google-vertex":            "Google Vertex AI",
	"groq":                     "Groq",
	"huggingface":              "HuggingFace",
	"iflowcn":                  "iFlow",
	"kimi-for-coding":          "Kimi for Coding",
	"longcat":                  "LongCat",
	"minimax":                  "MiniMax",
	"minimax-cn":               "MiniMax China",
	"mistral":                  "Mistral",
	"moonshotai":               "Moonshot",
	"moonshotai-cn":            "Moonshot (CN)",
	"nebius":                   "Nebius",
	"novita-ai":                "Novita",
	"nvidia":                   "NVIDIA",
	"ollama-cloud":             "Ollama Cloud",
	"openai":                   "OpenAI",
	"opencode":                 "OpenCode Zen",
	"openrouter":               "OpenRouter",
	"perplexity":               "Perplexity",
	"sensenova":                "SenseNova",
	"siliconflow":              "SiliconFlow",
	"siliconflow-cn":           "SiliconFlow (CN)",
	"snowflake-cortex":         "Snowflake Cortex",
	"stepfun":                  "StepFun",
	"thinkingmachines":         "Thinking Machines",
	"togetherai":               "Together AI",
	"upstage":                  "Upstage",
	"venice":                   "Venice",
	"vercel":                   "Vercel AI Gateway",
	"volcengine":               "Volcengine Ark",
	"wandb":                    "W&B",
	"watsonx":                  "IBM watsonx",
	"xai":                      "xAI Grok",
	"xiaomi":                   "Xiaomi MiMo",
	"xiaomi-token-plan-ams":    "Xiaomi MiMo (AMS)",
	"xiaomi-token-plan-cn":     "Xiaomi MiMo (CN)",
	"xiaomi-token-plan-sgp":    "Xiaomi MiMo (SGP)",
	"zai":                      "Z.ai",
	"zai-coding-plan":          "Z.ai (Coding Plan)",
	"zhipuai":                  "Zhipu GLM",
	"zhipuai-coding-plan":      "Zhipu GLM (Coding Plan)",
}

// models.dev api.json wire types.
type modelsDevDoc map[string]modelsDevProvider

type modelsDevProvider struct {
	ID     string                    `json:"id"`
	Name   string                    `json:"name"`
	NPM    string                    `json:"npm"`
	API    string                    `json:"api"`
	Doc    string                    `json:"doc"`
	Env    []string                  `json:"env"`
	Models map[string]modelsDevModel `json:"models"`
}

type modelsDevModel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Attachment  bool   `json:"attachment"`
	ReleaseDate string `json:"release_date"`
	Limit       struct {
		Context int `json:"context"`
		Input   int `json:"input"`
		Output  int `json:"output"`
	} `json:"limit"`
	Cost struct {
		Input     float64 `json:"input"`
		Output    float64 `json:"output"`
		CacheRead float64 `json:"cache_read"`
	} `json:"cost"`
}

/*
localProviders carries ggcode-local vendor definitions that upstream
does not ship (e.g. "xiaomi-mimo", added manually in v1.3.154). Regeneration
used to wipe them whenever upstream dropped or never had the provider.
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

	// 1. Fetch models.dev api.json (single source of truth).
	var allEntries []modelEntry
	var sections []string            // ordered section names for output
	var providers []*catwalkProvider // save for vendor_defaults.go

	doc, err := fetchModelsDev()
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: fetch models.dev: %v\n", err)
		os.Exit(1)
	}

	// Deterministic output order: sort provider IDs alphabetically.
	pids := make([]string, 0, len(desiredProviders))
	for pid := range desiredProviders {
		pids = append(pids, pid)
	}
	sort.Strings(pids)

	for _, pid := range pids {
		mdp, ok := (*doc)[pid]
		if !ok {
			fmt.Fprintf(os.Stderr, "  WARNING: models.dev has no provider %q\n", pid)
			continue
		}
		provider := adaptModelsDevProvider(pid, desiredProviders[pid], &mdp)

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

		fmt.Fprintf(os.Stderr, "  %s: %d models\n", pid, len(provider.Models))
		if len(sectionEntries) == 0 && len(provider.Models) == 0 {
			continue
		}

		sections = append(sections, desiredProviders[pid])
		allEntries = append(allEntries, sectionEntries...)
		providers = append(providers, provider)
	}

	fmt.Fprintf(os.Stderr, "\nTotal models: %d\n\n", len(allEntries))

	// 1b. Merge local-only providers (see localProviders doc comment).
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

func fetchModelsDev() (*modelsDevDoc, error) {
	resp, err := http.Get(modelsDevAPIURL)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", modelsDevAPIURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("fetch %s: HTTP %d", modelsDevAPIURL, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", modelsDevAPIURL, err)
	}

	var doc modelsDevDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", modelsDevAPIURL, err)
	}
	return &doc, nil
}

// adaptModelsDevProvider converts a models.dev provider entry into the
// generator's internal representation. models.dev has no explicit default
// model field, so DefaultLargeModelID is derived: the newest model by
// release_date whose ID is not a third-party re-serve (no "/" in the ID -
// openrouter-style prefixed entries) wins.
func adaptModelsDevProvider(pid, sectionName string, mdp *modelsDevProvider) *catwalkProvider {
	p := &catwalkProvider{
		ID:          pid,
		Name:        sectionName,
		APIEndpoint: mdp.API,
	}
	for mid, m := range mdp.Models {
		id := mid
		if m.ID != "" {
			id = m.ID
		}
		p.Models = append(p.Models, catwalkModel{
			ID:                  id,
			Name:                m.Name,
			ContextWindow:       m.Limit.Context,
			DefaultMaxTokens:    m.Limit.Output,
			SupportsAttachments: m.Attachment,
			CostPer1mIn:         m.Cost.Input,
			CostPer1mOut:        m.Cost.Output,
			ReleaseDate:         m.ReleaseDate,
		})
	}
	// Deterministic order: newest release first, then ID.
	sort.SliceStable(p.Models, func(i, j int) bool {
		if p.Models[i].ReleaseDate != p.Models[j].ReleaseDate {
			return p.Models[i].ReleaseDate > p.Models[j].ReleaseDate
		}
		return p.Models[i].ID < p.Models[j].ID
	})
	// Default large model: newest own model (skip re-served IDs with "/").
	for _, m := range p.Models {
		if !strings.Contains(m.ID, "/") {
			p.DefaultLargeModelID = m.ID
			break
		}
	}
	return p
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
// Source: https://models.dev (api.json), MIT-licensed
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
// Source: https://models.dev (api.json), MIT-licensed

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
// catwalk api_endpoint (models.dev era: provider api field) plus locally
// maintained extras — so matching is a
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

// populateDefaultModels fills endpoint Models lists from the models.dev data.
// Only sets Models on endpoints that don't already have user-defined models.
func populateDefaultModels(cfg *Config) {
	vendorToProvider := map[string][]string{
		"zai":            {"zai", "zai-coding-plan"},
		"zhipu":          {"zhipuai", "zhipuai-coding-plan"},
		"anthropic":      {"anthropic"},
		"openai":         {"openai"},
		"google":         {"google"},
		"openrouter":     {"openrouter"},
		"groq":           {"groq"},
		"mistral":        {"mistral"},
		"deepseek":       {"deepseek"},
		"moonshot":       {"moonshotai", "moonshotai-cn"}, // #1525 lineage: coding-plan entries stay out of the plain OpenAI-protocol list
		"kimi":           {"kimi-for-coding"},
		"minimax":        {"minimax", "minimax-cn"},
		"perplexity":     {"perplexity"},
		"github-copilot": {"github-copilot"},
		"copilot":        {"github-copilot"},
		"xiaomi-mimo":    {"xiaomi-mimo"},
		"xiaomi":         {"xiaomi"},
		"xai":            {"xai"},
		"together":       {"togetherai"},
		"nvidia":         {"nvidia"},
		"ark":            {"volcengine"},
		"volcengine":     {"volcengine"},
		"aliyun":         {"alibaba-cn"},
		"dashscope":      {"alibaba-cn"},
		"alibaba":        {"alibaba"},
		"azure":          {"azure"},
		"bedrock":        {"amazon-bedrock"},
		"vertexai":       {"google-vertex"},
		"huggingface":    {"huggingface"},
		"fireworks":      {"fireworks-ai"},
		"cerebras":       {"cerebras"},
		"siliconflow":    {"siliconflow"},
		"novita":         {"novita-ai"},
		"chutes":         {"chutes"},
		"venice":         {"venice"},
		"nebius":         {"nebius"},
		"stepfun":        {"stepfun"},
	}

	for vendorName, vc := range cfg.Vendors {
		providerIDs, ok := vendorToProvider[vendorName]
		if !ok {
			// Attribute unknown vendors to a provider by endpoint URL host so
			// custom endpoints pointing at a known provider (e.g. a user-added
			// vendor with base_url on api.z.ai) still receive that provider's
			// model list. Read-only: builtin URLs in config.go are untouched.
			if pid := matchProviderByBaseURL(firstNonEmptyBaseURL(vc)); pid != "" {
				providerIDs = []string{pid}
			} else {
				continue
			}
		}
		for epName, ep := range vc.Endpoints {
			if len(ep.Models) > 0 {
				continue
			}
			var models []string
			seen := make(map[string]bool)
			for _, cid := range providerIDs {
				if m := lookupVendorModels(cid); len(m) > 0 {
					for _, name := range m {
						// #1525 lineage: alias pairs (zai/zai-coding-plan,
					// minimax/minimax-cn, ...) may carry overlapping lists - merge
					// by name or models show twice in the model panel.
						if !seen[name] {
							seen[name] = true
							models = append(models, name)
						}
					}
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

		// Heuristic ranking, lexicographic (strict tuple compare, no additive
		// scores - additive scores deadlock on ties where the first entry has
		// already banked the same points):
		//  1. SourceProvider appears in the model ID ("minimax-m2.7" from
		//     provider "minimax" beats the same name served by "openrouter").
		//  2. Richer max output: first-party sources expose larger output
		//     limits than third-party mirrors serving the same bare model ID
		//     (glm-5: zai 131072 vs alibaba-cn 16384).
		//  3. Larger context window.
		//  4. Prefer re-serve IDs containing "/" LAST: "zai-org/glm-5" from a
		//     catalog is a worse witness than the bare "glm-5" entry.
		rank := func(e modelEntry) (nameScore, out, ctx int) {
			if strings.HasPrefix(e.ID, e.SourceProvider) ||
				strings.Contains(e.ID, e.SourceProvider) {
				nameScore = 1
			}
			if strings.Contains(e.ID, "/") {
				nameScore = -1 // catalog re-serve: weakest witness
			}
			return nameScore, e.MaxOutputTokens, e.ContextWindow
		}
		best := group[0]
		bestName, bestOut, bestCtx := rank(best)
		for _, e := range group[1:] {
			n, o, c := rank(e)
			if n > bestName ||
				(n == bestName && (o > bestOut || (o == bestOut && c > bestCtx))) {
				best, bestName, bestOut, bestCtx = e, n, o, c
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
