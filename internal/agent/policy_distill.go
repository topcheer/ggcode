package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/topcheer/ggcode/internal/provider"
)

const (
	policyDistillMinInjections = 3

	policyDistillMinSuccessRatio = 0.75

	policyDistillBatchMax = 6

	policyDistillThrottle = time.Hour

	policyDistillTimeout = 15 * time.Second

	policyDistillMaxRunes = 280
	policyDistillMaxLines = 3

	policyDistillStampFile = "policy-distill.stamp"
)

type distillChatFn func(ctx context.Context, system, user string) (string, error)

func matureForDistill(l trajectoryLearning) bool {
	if l.Refined != "" {
		return false
	}
	if l.InjectedRuns < policyDistillMinInjections {
		return false
	}
	total := l.AfterSuccess + l.AfterFail
	if total < policyDistillMinInjections {
		return false
	}
	if effectivenessGated(l) {
		return false
	}
	return float64(l.AfterSuccess)/float64(total) >= policyDistillMinSuccessRatio
}

func sanitizePolicyText(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	var b strings.Builder
	for _, r := range s {
		if r == '\n' || r == '\t' || unicode.IsPrint(r) {
			b.WriteRune(r)
		}
	}
	s = b.String()

	lines := strings.Split(s, "\n")
	if len(lines) > policyDistillMaxLines {
		lines = lines[:policyDistillMaxLines]
	}
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	s = strings.TrimSpace(strings.Join(lines, "\n"))
	if s == "" {
		return "", false
	}
	s = truncateRunesUTF8(s, policyDistillMaxRunes)

	lower := strings.ToLower(s)
	for _, pat := range []string{
		"ignore previous", "ignore all previous", "disregard previous",
		"system prompt", "your instructions", "new instructions",
		"you are now", "act as", "jailbreak", "developer mode",
	} {
		if strings.Contains(lower, pat) {
			return "", false
		}
	}
	return s, true
}

func buildDistillUserPrompt(entries []trajectoryLearning) string {
	var b strings.Builder
	b.WriteString("Rewrite each measured learning below as direct policy for a coding agent.\n")
	b.WriteString("Rules: at most 2 short imperative sentences per item; concrete and actionable; no meta-commentary; keep the original meaning.\n\n")
	for i, l := range entries {
		fmt.Fprintf(&b, "%d. [%s/%s] %s (measured: %d injections, %d succeeded)\n",
			i+1, l.Type, l.Category, l.Insight, l.InjectedRuns, l.AfterSuccess)
	}
	b.WriteString("\nAnswer with exactly one line per item, format: `N. policy text`. No other output.\n")
	return b.String()
}

const policyDistillSystem = "You compile measured trajectory feedback into prompt policy. You rewrite statistical observations as concise imperative guidance a coding agent can follow directly."

func parseDistillResponse(resp string) map[int]string {
	out := map[int]string{}
	for _, line := range strings.Split(resp, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		line = strings.TrimPrefix(line, "-")
		line = strings.TrimSpace(line)
		var n int
		var text string
		if _, err := fmt.Sscanf(line, "%d.", &n); err != nil {
			continue
		}

		if idx := strings.Index(line, "."); idx >= 0 {
			text = strings.TrimSpace(line[idx+1:])
		}
		if n < 1 || text == "" {
			continue
		}
		out[n] = text
	}
	return out
}

func runPolicyDistill(ctx context.Context, entries []trajectoryLearning, chat distillChatFn) map[int]string {
	result := map[int]string{}
	if len(entries) == 0 || chat == nil {
		return result
	}

	if len(entries) > policyDistillBatchMax {
		entries = entries[:policyDistillBatchMax]
	}
	resp, err := chat(ctx, policyDistillSystem, buildDistillUserPrompt(entries))
	if err != nil {
		return result
	}
	parsed := parseDistillResponse(resp)
	for n, text := range parsed {
		if n < 1 || n > len(entries) {
			continue
		}
		if clean, ok := sanitizePolicyText(text); ok {
			result[n-1] = clean
		}
	}
	return result
}

func (a *Agent) maybeDistillPolicies(workingDir string) {
	if workingDir == "" || a.trajIntel == nil {
		return
	}
	ggDir := filepath.Join(workingDir, ".ggcode")
	stampPath := filepath.Join(ggDir, policyDistillStampFile)
	if info, err := os.Stat(stampPath); err == nil && time.Since(info.ModTime()) < policyDistillThrottle {
		return
	}

	a.trajIntel.mu.Lock()
	entries, err := a.trajIntel.loadFromFile()
	a.trajIntel.mu.Unlock()
	if err != nil {
		return
	}
	var candidates []trajectoryLearning
	var keys []string
	for _, l := range entries {
		if matureForDistill(l) {
			candidates = append(candidates, l)
			keys = append(keys, trajDedupeKey(l))
		}
	}
	if len(candidates) == 0 {
		return
	}
	if len(candidates) > policyDistillBatchMax {
		candidates = candidates[:policyDistillBatchMax]
		keys = keys[:policyDistillBatchMax]
	}

	chat := func(ctx context.Context, system, user string) (string, error) {
		if a.auxProviderFor() == nil {
			return "", fmt.Errorf("no aux provider")
		}
		resp, err := a.auxChat(ctx, []provider.Message{
			{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: system}}},
			{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: user}}},
		}, nil)
		if err != nil {
			return "", err
		}
		if resp == nil {
			return "", fmt.Errorf("empty aux response")
		}
		for _, blk := range resp.Message.Content {
			if blk.Type == "text" && strings.TrimSpace(blk.Text) != "" {
				return blk.Text, nil
			}
		}
		return "", fmt.Errorf("aux response had no text")
	}
	ctx, cancel := context.WithTimeout(context.Background(), policyDistillTimeout)
	defer cancel()
	refined := runPolicyDistill(ctx, candidates, chat)
	if len(refined) == 0 {

		_ = os.MkdirAll(ggDir, 0o755)
		_ = os.WriteFile(stampPath, []byte(time.Now().UTC().Format(time.RFC3339)), 0o644)
		return
	}

	byKey := map[string]string{}
	for idx, text := range refined {
		if idx < len(keys) {
			byKey[keys[idx]] = text
		}
	}
	_ = a.trajIntel.rewriteAllLocked(func(existing []trajectoryLearning, loadErr error) ([]trajectoryLearning, error) {
		if loadErr != nil {
			return existing, loadErr
		}
		for i := range existing {
			if text, ok := byKey[trajDedupeKey(existing[i])]; ok {
				existing[i].Refined = text
			}
		}
		return existing, nil
	})
	_ = os.MkdirAll(ggDir, 0o755)
	_ = os.WriteFile(stampPath, []byte(time.Now().UTC().Format(time.RFC3339)), 0o644)
}
