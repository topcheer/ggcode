package agentruntime

// Aux small-model routing (multi-model cascade).
//
// Industry pattern (2025-2026): cheap "small" models (Claude Haiku,
// Gemini Flash, MiMo) run auxiliary one-shot tasks so they never consume
// the main coding model's budget or latency. ggcode's generated vendor
// defaults already carried a SmallModel field, but no runtime path ever
// consumed it. This module resolves the auxiliary model and provides the
// first consumer: LLM-assisted session title generation, with the
// deterministic heuristics in auto_title.go as the fallback.

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/util"
)

const (
	// AuxTitleTimeout bounds the one-shot aux title call so a slow relay
	// can never stall the session lifecycle (fire-and-forget caller).
	AuxTitleTimeout = 10 * time.Second
	// auxTitleMaxInput caps how much of the first user message is fed to
	// the small model - titles don't need the whole dump.
	auxTitleMaxInput = 2000
	// auxTitleMaxTok keeps the output budget tiny; a title is one line.
	auxTitleMaxTok = 64
)

// titlePrefixRe strips "Title:"-style lead-ins small models like to add.
var titlePrefixRe = regexp.MustCompile(`^(?i)(title|session title|标题|会话标题)\s*[:：]\s*`)

// ResolveAuxModel returns the auxiliary small model id for this config:
// an explicit aux_model setting wins, then the vendor's generated
// small-model default. Empty means aux routing is off and deterministic
// heuristics stay in charge.
func ResolveAuxModel(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if s := strings.TrimSpace(cfg.AuxModel); s != "" {
		return s
	}
	return config.DefaultSmallModel(cfg.Vendor)
}

// AuxProvider builds a dedicated provider instance bound to the auxiliary
// model. It reuses the configured vendor/endpoint (and therefore the same
// API key), overriding only the model - the same account-level pattern
// Claude Code uses for Haiku side-calls.
func AuxProvider(cfg *config.Config) (provider.Provider, error) {
	aux := ResolveAuxModel(cfg)
	if aux == "" {
		return nil, fmt.Errorf("no auxiliary model configured (set aux_model or use a vendor with a small-model default)")
	}
	// Resolve on the original config - Config carries a mutex and must not
	// be copied; only the model argument changes.
	resolved, err := cfg.ResolveEndpointSelection(cfg.Vendor, cfg.Endpoint, aux)
	if err != nil {
		return nil, fmt.Errorf("resolving aux endpoint (model %s): %w", aux, err)
	}
	if resolved.APIKey == "" {
		return nil, fmt.Errorf("missing API key for aux model %s", aux)
	}
	p, err := provider.NewProvider(resolved)
	if err != nil {
		return nil, fmt.Errorf("creating aux provider (model %s): %w", aux, err)
	}
	debug.Log("aux-model", "aux provider ready: model=%s base=%s", aux, p.Name())
	return p, nil
}

// TitleLLMPrompt builds the system/user prompt pair for the aux title
// call. The user message is capped at auxTitleMaxInput runes.
func TitleLLMPrompt(firstUserMessage string) (system, user string) {
	s := []rune(strings.TrimSpace(firstUserMessage))
	if len(s) > auxTitleMaxInput {
		s = s[:auxTitleMaxInput]
	}
	system = "You generate concise titles for coding-assistant sessions. " +
		"Reply with ONLY the title: 3-8 words, no quotes, no trailing period, " +
		"same language as the user message. Do not explain."
	user = "User message:\n" + string(s)
	return system, user
}

// CleanLLMTitle sanitizes raw small-model output into a usable session
// title: first line only, prefix/quote stripping, whitespace collapse,
// and the same 60-rune cap as the heuristic titles.
func CleanLLMTitle(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	s = strings.TrimSpace(titlePrefixRe.ReplaceAllString(s, ""))
	s = strings.Trim(s, "\"'`“”‘’")
	s = strings.TrimSpace(excessiveSpacesRe.ReplaceAllString(s, " "))
	if utf8.RuneCountInString(s) < titleMinRunes {
		return ""
	}
	// Reject punctuation-only output ("...", "…") - a usable title must
	// contain at least one letter or CJK rune.
	hasLetter := false
	for _, r := range s {
		if unicode.IsLetter(r) {
			hasLetter = true
			break
		}
	}
	if !hasLetter {
		return ""
	}
	return truncateTitle(s, titleMaxRunes)
}

// NeedsLLMTitleUpgrade reports whether the current title is machine-set
// (empty placeholder, generic, or exactly the store's naive first-60-rune
// truncation) and may therefore be replaced by a better generated one.
// User-chosen titles never qualify.
func NeedsLLMTitleUpgrade(currentTitle, firstUserMessage string) bool {
	if ShouldAutoTitle(currentTitle) {
		return true
	}
	if isGenericTitle(currentTitle) {
		return true
	}
	// JSONLStore.AppendMessage derives the auto title as
	// util.Truncate(firstText, 60); equality means the title is still the
	// machine-set naive truncation, not a user choice.
	naive := util.Truncate(strings.TrimSpace(firstUserMessage), titleMaxRunes)
	return naive != "" && currentTitle == naive
}

// GenerateLLMTitle performs the one-shot aux-model call and returns a
// cleaned title. Errors are expected and non-fatal - callers keep the
// heuristic title on any failure.
func GenerateLLMTitle(ctx context.Context, p provider.Provider, firstUserMessage string) (string, error) {
	if p == nil {
		return "", fmt.Errorf("no provider for aux title")
	}
	system, user := TitleLLMPrompt(firstUserMessage)
	messages := []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{provider.TextBlock(system)}},
		{Role: "user", Content: []provider.ContentBlock{provider.TextBlock(user)}},
	}
	// Keep the output budget tiny via the same atomic override the MCP
	// sampling path uses (#2248); providers without it keep their default.
	if so, ok := p.(provider.SamplingOverrideSetter); ok {
		prev := so.SamplingOverride()
		so.SetSamplingOverride(&provider.SamplingOverride{MaxTokens: auxTitleMaxTok})
		defer func() { so.SetSamplingOverride(prev) }()
	}
	resp, err := p.Chat(ctx, messages, nil)
	if err != nil {
		return "", fmt.Errorf("aux title chat: %w", err)
	}
	var text string
	for _, b := range resp.Message.Content {
		if b.Type == "text" {
			text += b.Text
		}
	}
	title := CleanLLMTitle(text)
	if title == "" {
		return "", fmt.Errorf("aux title: empty/unsuitable output")
	}
	debug.Log("aux-model", "aux title generated: %q", title)
	return title, nil
}
