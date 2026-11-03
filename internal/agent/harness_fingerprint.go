package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

func sha256Prefix(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

func hashNames(names []string) (string, int) {
	sorted := make([]string, len(names))
	copy(sorted, names)
	sort.Strings(sorted)
	return sha256Prefix(strings.Join(sorted, "\x00")), len(sorted)
}

func integrityCheckNames() []string {
	names := make([]string, 0, len(allChecks))
	for _, c := range allChecks {
		names = append(names, c.Name)
	}
	return names
}

func harnessToolNames(reg *tool.Registry) []string {
	if reg == nil {
		return nil
	}
	names := make([]string, 0, 32)
	for _, t := range reg.List() {
		names = append(names, t.Name())
	}
	return names
}

type HarnessFingerprint struct {
	SystemPromptSHA string    `json:"system_prompt_sha"`
	SystemPromptLen int       `json:"system_prompt_len"`
	ChecksSHA       string    `json:"checks_sha"`
	ChecksCount     int       `json:"checks_count"`
	ToolsSHA        string    `json:"tools_sha"`
	ToolsCount      int       `json:"tools_count"`
	ComputedAt      time.Time `json:"computed_at"`
}

func (fp HarnessFingerprint) Sum() string {
	return fmt.Sprintf("sp=%s/%d checks=%s/%d tools=%s/%d",
		fp.SystemPromptSHA, fp.SystemPromptLen,
		fp.ChecksSHA, fp.ChecksCount,
		fp.ToolsSHA, fp.ToolsCount)
}

func (a *Agent) ComputeHarnessFingerprint() HarnessFingerprint {
	a.mu.RLock()
	prompt := a.baseSystemPrompt
	reg := a.tools
	a.mu.RUnlock()

	checksSHA, checksN := hashNames(integrityCheckNames())
	toolsSHA, toolsN := hashNames(harnessToolNames(reg))
	return HarnessFingerprint{
		SystemPromptSHA: sha256Prefix(prompt),
		SystemPromptLen: len(prompt),
		ChecksSHA:       checksSHA,
		ChecksCount:     checksN,
		ToolsSHA:        toolsSHA,
		ToolsCount:      toolsN,
		ComputedAt:      time.Now(),
	}
}

func (a *Agent) logHarnessFingerprint() {
	sum := a.ComputeHarnessFingerprint().Sum()
	a.mu.Lock()
	prev := a.harnessFPLast
	a.harnessFPLast = sum
	a.mu.Unlock()

	switch {
	case prev == sum:

	case prev == "":
		debug.Log("harness", "harness fingerprint: %s", sum)
	default:
		debug.Log("harness", "harness fingerprint CHANGED: %s (was %s)", sum, prev)
	}
}
