//go:build ignore

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
	"time"
)

const modelsDevAPIURL = "https://models.dev/api.json"

type catwalkProvider struct {
	ID                  string         `json:"id"`
	Name                string         `json:"name"`
	Type                string         `json:"type"`
	APIEndpoint         string         `json:"api_endpoint"`
	DefaultLargeModelID string         `json:"default_large_model_id"`
	DefaultSmallModelID string         `json:"default_small_model_id"`
	Models              []catwalkModel `json:"models"`

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

	ReleaseDate string `json:"-"`

	SupportsChatOutput bool
}

func isChatModel(m catwalkModel) bool {
	return m.SupportsChatOutput
}

type modelEntry struct {
	ID              string
	ContextWindow   int
	MaxOutputTokens int
	SupportsVision  bool
	SourceProvider  string
}

var providerMerges = map[string]string{
	"kimi-code-plan-global": "kimi-for-coding",
	"kimi-code-plan-cn":     "kimi-for-coding",
}

var providerMergeNames = map[string]string{
	"kimi-for-coding": "Kimi for Coding",
}

func mergeProviderInto(dst, src *catwalkProvider) {
	for _, e := range append([]string{src.APIEndpoint}, src.ExtraEndpoints...) {
		if e == "" || e == dst.APIEndpoint || containsString(dst.ExtraEndpoints, e) {
			continue
		}
		dst.ExtraEndpoints = append(dst.ExtraEndpoints, e)
	}
	for _, m := range src.Models {
		dup := false
		for _, d := range dst.Models {
			if d.ID == m.ID {
				dup = true
				break
			}
		}
		if !dup {
			dst.Models = append(dst.Models, m)
		}
	}
	if dst.DefaultLargeModelID == "" {
		dst.DefaultLargeModelID = src.DefaultLargeModelID
	}
	if dst.DefaultSmallModelID == "" {
		dst.DefaultSmallModelID = src.DefaultSmallModelID
	}
}

var localModelExtras = []modelEntry{
	{ID: "glm-4v-plus", ContextWindow: 128000, MaxOutputTokens: 128000, SupportsVision: true, SourceProvider: "zhipuai"},
}

var desiredProviders = map[string]string{
	"302ai":                    "302.AI",
	"aihubmix":                 "AIHubMix",
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

	"kimi-code-plan-cn":     "Kimi for Coding (CN)",
	"kimi-code-plan-global": "Kimi for Coding (Global)",
	"longcat":               "LongCat",
	"minimax":               "MiniMax",
	"minimax-cn":            "MiniMax China",
	"mistral":               "Mistral",
	"moonshotai":            "Moonshot",
	"moonshotai-cn":         "Moonshot (CN)",
	"nebius":                "Nebius",
	"novita-ai":             "Novita",
	"nvidia":                "NVIDIA",
	"ollama-cloud":          "Ollama Cloud",
	"openai":                "OpenAI",
	"opencode":              "OpenCode Zen",
	"openrouter":            "OpenRouter",
	"perplexity":            "Perplexity",
	"poe":                   "Poe",
	"requesty":              "Requesty",
	"sensenova":             "SenseNova",
	"siliconflow":           "SiliconFlow",
	"siliconflow-cn":        "SiliconFlow (CN)",
	"snowflake-cortex":      "Snowflake Cortex",
	"stepfun":               "StepFun",
	"thinkingmachines":      "Thinking Machines",
	"togetherai":            "Together AI",
	"upstage":               "Upstage",
	"venice":                "Venice",
	"vercel":                "Vercel AI Gateway",
	"volcengine":            "Volcengine Ark",
	"wandb":                 "W&B",
	"watsonx":               "IBM watsonx",
	"xai":                   "xAI Grok",
	"xiaomi":                "Xiaomi MiMo",
	"xiaomi-token-plan-ams": "Xiaomi MiMo (AMS)",
	"xiaomi-token-plan-cn":  "Xiaomi MiMo (CN)",
	"xiaomi-token-plan-sgp": "Xiaomi MiMo (SGP)",
	"zai":                   "Z.ai",
	"zai-coding-plan":       "Z.ai (Coding Plan)",
	"zhipuai":               "Zhipu GLM",
	"zhipuai-coding-plan":   "Zhipu GLM (Coding Plan)",
}

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

	Modality struct {
		Input  []string `json:"input"`
		Output []string `json:"output"`
	} `json:"modality"`
	Limit struct {
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
			{ID: "MiMo-V2-Omni", ContextWindow: 262144, SupportsAttachments: true},
			{ID: "MiMo-V2-TTS"},
		},
	},
}

var builtinEndpointFallback = map[string][]string{
	"anthropic": {"https://api.anthropic.com"},
}

func main() {
	dryRun := flag.Bool("dry-run", false, "Print to stdout instead of writing file")
	forceShrink := flag.Bool("force", false, "Allow >20%% entry-count shrinkage (checked against the existing output file)")
	output := flag.String("output", "internal/config/context_window.go", "Output file path")
	flag.Parse()

	doc, err := fetchModelsDev()
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: fetch models.dev: %v\n", err)
		os.Exit(1)
	}

	providers, sections, allEntries := collectProviders(doc)
	fmt.Fprintf(os.Stderr, "\nTotal models: %d\n\n", len(allEntries))

	providers, sections, allEntries = mergeLocalProviders(providers, sections, allEntries)
	fmt.Fprintf(os.Stderr, "\nFinal total models: %d\n\n", len(allEntries))

	allEntries = append(allEntries, localModelExtras...)

	code := generateGoCode(allEntries, sections)
	vdCode := generateVendorDefaults(providers)

	writeOutputs(code, vdCode, *output, allEntries, *dryRun, *forceShrink)
}

// officialFamilyOutputCaps pins vendor-mirror rows to the vendor's own
// documented output ceiling (#5141 Class 2). models.dev mirrors inflate
// MaxOutputTokens above the official cap: e.g. deepseek-v4 mirrors report
// ~0.94M-1.05M while api-docs.deepseek.com caps max_tokens at "1 and 384K
// (393216)" (verified 2026-11-07). Clamping at emit keeps sibling spellings
// (@cf/, bare, vendor-prefixed) in sync with the official family cap and
// stops the weekly-drift reds from mirror inflation.
var officialFamilyOutputCaps = []struct {
	substr string
	cap    int
}{
	{"deepseek", 393216},
	// GLM official output ceiling is 128K (131072) per z.ai docs; pinned by
	// the #4946 router-alias contract ("must mirror official caps").
	{"glm", 131072},
	// Kimi K3 officially supports 1M output (1048576), pinned by #4946;
	// models.dev mirror rows under-report ~0.9M (943718).
	{"kimi", 1048576},
}

// clampMaxOutputTokens ceilings vendor-mirror rows at the family's official
// cap; router-alias rows ("~" prefix, e.g. "~moonshotai/kimi-latest") are
// pinned EXACTLY to the official cap in both directions, per the #4946
// contract "router alias must mirror official caps".
func clampMaxOutputTokens(model string, maxOut int) int {
	for _, c := range officialFamilyOutputCaps {
		if !strings.Contains(model, c.substr) {
			continue
		}
		if strings.HasPrefix(model, "~") {
			return c.cap
		}
		if maxOut > c.cap {
			return c.cap
		}
	}
	return maxOut
}

// upstreamVisionOverrides pins capability fields where models.dev data is
// known-wrong (#5332). Entries are keyed "provider/model-id" (both lowercase)
// and applied right after the upstream fetch, BEFORE any code generation, so
// the drift-guard fact pins stay the source of truth rather than chasing
// upstream regressions.
//
// openai/o3-mini and openai/o3-mini-high: text-only models; models.dev
// intermittently re-tags them SupportsVision=true (observed 2026-11-09, run
// 42602486566). First flagged and pinned text-only by the manual calibration
// batch #5320; the weekly sync kept failing the drift-guard until this
// local correction layer existed.
var upstreamVisionOverrides = map[string]bool{
	"openai/o3-mini":      false,
	"openai/o3-mini-high": false,
}

// visionOverrideFor resolves the override for an entry, tolerating BOTH
// upstream ID shapes models.dev has used: bare ("o3-mini", sourceProvider
// ="openai") and provider-prefixed ("openai/o3-mini", where concatenating
// the provider again would produce "openai/openai/o3-mini" and miss).
// The bare form is tried as the entry ID itself so prefixed entries hit
// the table key directly.
func visionOverrideFor(sourceProvider, id string) (bool, bool) {
	if v, ok := upstreamVisionOverrides[sourceProvider+"/"+id]; ok {
		return v, true
	}
	if v, ok := upstreamVisionOverrides[id]; ok {
		return v, true
	}
	return false, false
}

func collectProviders(doc *modelsDevDoc) (providers []*catwalkProvider, sections []string, allEntries []modelEntry) {

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

		if target, ok := providerMerges[pid]; ok {
			provider.ID = target
			if name, ok2 := providerMergeNames[target]; ok2 {
				provider.Name = name
			}
		}

		var sectionEntries []modelEntry
		for _, m := range provider.Models {
			if m.ContextWindow <= 0 {
				continue
			}
			e := modelEntry{
				ID:              strings.ToLower(strings.TrimSpace(m.ID)),
				ContextWindow:   m.ContextWindow,
				MaxOutputTokens: clampMaxOutputTokens(strings.ToLower(strings.TrimSpace(m.ID)), m.DefaultMaxTokens),
				SupportsVision:  m.SupportsAttachments,
				SourceProvider:  provider.ID,
			}
			// #5332: apply local correction layer after the upstream read so
			// known-bad models.dev rows cannot re-enter the generated table
			// (the drift-guard pins stay untouched and authoritative).
			if fixed, ok := visionOverrideFor(e.SourceProvider, e.ID); ok {
				if e.SupportsVision != fixed {
					fmt.Fprintf(os.Stderr, "  override: %s/%s SupportsVision %v -> %v (#5332 local fix)\n", e.SourceProvider, e.ID, e.SupportsVision, fixed)
				}
				e.SupportsVision = fixed
			}
			sectionEntries = append(sectionEntries, e)
		}

		fmt.Fprintf(os.Stderr, "  %s: %d models\n", pid, len(provider.Models))
		if len(sectionEntries) == 0 && len(provider.Models) == 0 {
			continue
		}

		sections = append(sections, desiredProviders[pid])

		if len(sectionEntries) == 0 {
			fmt.Fprintf(os.Stderr, "FATAL: provider %q yielded 0 usable entries (models.dev schema change or partial fetch?) - aborting to protect existing tables\n", pid)
			os.Exit(1)
		}
		allEntries = append(allEntries, sectionEntries...)

		merged := false
		for i := range providers {
			if providers[i].ID == provider.ID {
				mergeProviderInto(providers[i], provider)
				merged = true
				break
			}
		}
		if !merged {
			providers = append(providers, provider)
		}
	}
	return providers, sections, allEntries
}

func mergeLocalProviders(providers []*catwalkProvider, sections []string, allEntries []modelEntry) ([]*catwalkProvider, []string, []modelEntry) {
	for _, lp := range localProviders {
		upstream := false
		for _, p := range providers {
			if p.ID == lp.ID {
				upstream = true
				break
			}
		}
		if upstream {
			continue
		}
		providers = append(providers, lp)
		sections = append(sections, lp.Name)
		for _, m := range lp.Models {
			if m.ContextWindow == 0 && m.DefaultMaxTokens == 0 && !m.SupportsAttachments {
				continue
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
	return providers, sections, allEntries
}

func writeOutputs(code, vdCode string, output string, allEntries []modelEntry, dryRun, forceShrink bool) {

	if !dryRun {
		prevTable, _ := os.ReadFile(output)
		oldCount := strings.Count(string(prevTable), ": {ContextWindow")
		newCount := len(dedupEntries(append([]modelEntry(nil), allEntries...)))
		if oldCount > 0 && newCount < oldCount*8/10 {
			fmt.Fprintf(os.Stderr, "FATAL: entry count %d is >20%% below existing %d - aborting (pass -force to override)\n", newCount, oldCount)
			if !forceShrink {
				os.Exit(1)
			}
		}
	}

	if dryRun {
		fmt.Print(code)
	} else {
		if err := os.WriteFile(output, []byte(code), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", output, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Written to %s\n", output)
	}

	vendorDefaultsPath := "internal/config/vendor_defaults.go"
	if idx := strings.LastIndex(output, "/"); idx >= 0 {
		vendorDefaultsPath = output[:idx+1] + "vendor_defaults.go"
	}
	if dryRun {
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

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(modelsDevAPIURL)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", modelsDevAPIURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("fetch %s: HTTP %d", modelsDevAPIURL, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", modelsDevAPIURL, err)
	}

	var doc modelsDevDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", modelsDevAPIURL, err)
	}
	return &doc, nil
}

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
			SupportsChatOutput:  len(m.Modality.Output) == 0 || containsString(m.Modality.Output, "text"),
			CostPer1mIn:         m.Cost.Input,
			CostPer1mOut:        m.Cost.Output,
			ReleaseDate:         m.ReleaseDate,
		})
	}

	sort.SliceStable(p.Models, func(i, j int) bool {
		if p.Models[i].ReleaseDate != p.Models[j].ReleaseDate {
			return p.Models[i].ReleaseDate > p.Models[j].ReleaseDate
		}
		return p.Models[i].ID < p.Models[j].ID
	})

	for _, m := range p.Models {
		if strings.Contains(m.ID, "/") {
			continue
		}
		if !isChatModel(m) {
			continue
		}
		p.DefaultLargeModelID = m.ID
		break
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

	sb.WriteString("var knownModelCapabilities = map[string]modelCapability{\n")

	entryIdx := 0
	for _, section := range sections {

		_ = section
	}

	entries = dedupEntries(entries)

	// #5332 final enforcement: dedup ranks vision=true sources ABOVE
	// false ones (v && !bestVision at the comparison), so a reseller row
	// tagged vision=true can out-rank the corrected provider row and
	// reintroduce the mislabel. The correction table wins over every
	// post-dedup winner — this is the single point where the generated
	// table's final vision values are pinned.
	for i := range entries {
		if fixed, ok := visionOverrideFor(entries[i].SourceProvider, entries[i].ID); ok && entries[i].SupportsVision != fixed {
			fmt.Fprintf(os.Stderr, "  override(final): %s SupportsVision %v -> %v (#5332 local fix beats dedup pick)\n", entries[i].ID, entries[i].SupportsVision, fixed)
			entries[i].SupportsVision = fixed
		}
	}

	// Output entries grouped loosely — just output sorted with comments

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

// #2613/#5141: the caps table is authoritative - trust a hit even when it
// says false (table-only semantics). Unknown models fall back to the
// gemini protocol default; keyword heuristics were removed in 3bfd20bfe1
// because they misreport proxy/gateway names (my-claude-proxy,
// custom-gpt-gateway) as vision-capable. The template MUST stay in sync
// with the committed fix or every regen reverts it (Face A drift guard:
// TestInferVisionSupportCapabilityTableOnly).
func inferVisionSupport(model, protocol string) bool {
	if cap, ok := lookupModelCapability(model); ok {
		return cap.SupportsVision
	}

	return strings.EqualFold(strings.TrimSpace(protocol), "gemini")
}

func lookupModelCapability(model string) (modelCapability, bool) {
	cap, ok := knownModelCapabilities[strings.ToLower(strings.TrimSpace(model))]
	return cap, ok
}

func parseContextWindowHint(model string) int {
	// #5050: FindAllStringSubmatch consumes each match's trailing boundary
	// char, which is also the LEADING boundary of the next k/m token - a
	// later, larger token ("8k-128k") then never matches and the take-max
	// degrades to the smaller hint. RE2 has no lookahead, so scan manually
	// and resume each match at the END of the k/m suffix, leaving the
	// trailing boundary char available for the next token.
	s := strings.ToLower(model)
	best := 0
	for i := 0; i < len(s); {
		loc := contextWindowHintPattern.FindStringSubmatchIndex(s[i:])
		if loc == nil {
			break
		}
		n, err := strconv.Atoi(s[i+loc[4] : i+loc[5]])
		if err == nil && n > 0 {
			switch s[i+loc[6] : i+loc[7]] {
			case "k":
				n *= 1000
			case "m":
				n *= 1000000
			}
			if n > best {
				best = n
			}
		}
		// Advance past the digits+k/m suffix only (loc[7] = end of group 3).
		i += loc[7]
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

	hostToPID := make(map[string]string)
	for _, p := range providers {
		cands := append([]string{p.APIEndpoint}, p.ExtraEndpoints...)
		cands = append(cands, builtinEndpointFallback[p.ID]...)
		for _, cand := range cands {

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
		// ai-gateway is a pure container of independent gateways: populate
		// each endpoint from ITS OWN provider via its own BaseURL host.
		// The generic vendor-level mapping would give every endpoint the
		// first endpoint's provider list - wrong for all others.
		if vendorName == "ai-gateway" {
			for epName, ep := range vc.Endpoints {
				if len(ep.Models) > 0 {
					continue
				}
				if pid := matchProviderByBaseURL(ep.BaseURL); pid != "" {
					if m := lookupVendorModels(pid); len(m) > 0 {
							// #2157: copy out of the package-level registry - direct
							// assignment shares the backing array across Configs, so
							// concurrent Loads racing expandEnvWithLookup's in-place
							// ep.Models[i] writes corrupt the shared table (and each
							// other) - deterministic darwin -race DATA RACE.
							ep.Models = append([]string(nil), m...)
						vc.Endpoints[epName] = ep
					}
				}
			}
			cfg.Vendors[vendorName] = vc
			continue
		}
		providerIDs, ok := vendorToProvider[vendorName]
		if !ok {
			// #1668 case 3: attribute UNKNOWN vendors per ENDPOINT, not by
			// the vendor's first sorted BaseURL. The old single-URL pick
			// typed the whole vendor by one endpoint: a two-endpoint gateway
			// (a -> api.z.ai, b -> api.deepseek.com) showed the GLM list on
			// the deepseek endpoint's model panel, steering users to models
			// the far side does not have. Each endpoint gets the list its
			// OWN URL matches; unmatched endpoints stay empty. Read-only:
			// builtin URLs in config.go are untouched.
			for epName, ep := range vc.Endpoints {
				if len(ep.Models) > 0 {
					continue
				}
				if pid := matchProviderByBaseURL(ep.BaseURL); pid != "" {
					if m := lookupVendorModels(pid); len(m) > 0 {
							// #2157: same shared-registry copy as the ai-gateway branch.
							ep.Models = append([]string(nil), m...)
						vc.Endpoints[epName] = ep
					}
				}
			}
			cfg.Vendors[vendorName] = vc
			continue
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

	groups := make(map[string][]modelEntry)
	for _, e := range allEntries {
		groups[e.ID] = append(groups[e.ID], e)
	}

	dedup := make(map[string]modelEntry, len(groups))
	for id, group := range groups {
		if len(group) == 1 {
			dedup[id] = group[0]
			continue
		}

		rank := func(e modelEntry) (nameScore, out, ctx int, vision bool) {
			if strings.HasPrefix(e.ID, e.SourceProvider) ||
				strings.Contains(e.ID, e.SourceProvider) {
				nameScore = 1
			}
			if strings.Contains(e.ID, "/") {
				nameScore = -1
			}
			return nameScore, e.MaxOutputTokens, e.ContextWindow, e.SupportsVision
		}
		best := group[0]
		bestName, bestOut, bestCtx, bestVision := rank(best)
		for _, e := range group[1:] {
			n, o, c, v := rank(e)
			if n > bestName ||
				(n == bestName && (v && !bestVision ||
					(v == bestVision && (o > bestOut || (o == bestOut && c > bestCtx))))) {
				best, bestName, bestOut, bestCtx, bestVision = e, n, o, c, v
			}
		}

		for _, e := range group {
			if e.SupportsVision != best.SupportsVision {
				fmt.Fprintf(os.Stderr,
					"  WARNING: dedup %q: vision divergence across sources (picked %v, source %s says %v)\n",
					id, best.SupportsVision, e.SourceProvider, e.SupportsVision)
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

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
