package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// migrateSystemPromptVersion ensures the built-in system prompt is current.
//
// Rules:
//   - No system_prompt + no version → first run or never customized; stamp version, keep default.
//   - Has system_prompt + no version → user customized before versioning existed; keep their
//     custom prompt, stamp current version to opt them out of future auto-upgrades.
//   - Has version < current → was using old default; upgrade to latest default, rewrite YAML.
//   - Has version >= current → up-to-date; do nothing.
func migrateSystemPromptVersion(cfg *Config, raw map[string]interface{}, path string) {
	if cfg == nil || path == "" {
		return
	}

	savedVersion := 0
	if v, ok := raw["system_prompt_version"]; ok {
		switch sv := v.(type) {
		case int:
			savedVersion = sv
		case int64:
			savedVersion = int(sv)
		case float64:
			savedVersion = int(sv)
		}
	}

	if savedVersion >= DefaultSystemPromptVersion {
		return // already up-to-date
	}

	_, hasPrompt := raw["system_prompt"]

	if hasPrompt && savedVersion == 0 {
		// User has a custom prompt from before versioning. Keep it,
		// but stamp current version so we don't touch it again.
		debug.Log("config", "custom system_prompt detected (pre-version), stamping version %d", DefaultSystemPromptVersion)
		cfg.SystemPromptVersion = DefaultSystemPromptVersion
		stampSystemPromptVersion(path)
		return
	}

	// No prompt or old version → upgrade to latest default
	debug.Log("config", "upgrading system prompt to version %d", DefaultSystemPromptVersion)
	cfg.SystemPrompt = DefaultSystemPrompt
	cfg.SystemPromptVersion = DefaultSystemPromptVersion
	rewriteSystemPromptVersion(path)
}

// stampSystemPromptVersion adds/updates system_prompt_version without touching system_prompt.
func stampSystemPromptVersion(path string) {
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")
	found := false
	for i, line := range lines {
		if strings.HasPrefix(line, "system_prompt_version:") {
			lines[i] = fmt.Sprintf("system_prompt_version: %d", DefaultSystemPromptVersion)
			found = true
			break
		}
	}
	if !found {
		insertAt := len(lines)
		for i, line := range lines {
			if strings.HasPrefix(line, "system_prompt:") {
				insertAt = i + 1
				break
			}
		}
		newLine := fmt.Sprintf("system_prompt_version: %d", DefaultSystemPromptVersion)
		if insertAt >= len(lines) {
			lines = append(lines, newLine)
		} else {
			lines = append(lines[:insertAt], append([]string{newLine}, lines[insertAt:]...)...)
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0600); err != nil {
		debug.Log("config", "failed to stamp system_prompt_version: %v", err)
	}
}

// rewriteSystemPromptVersion writes the latest version and removes the
// system_prompt key so the built-in default takes effect.
func rewriteSystemPromptVersion(path string) {
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")

	// Remove system_prompt: line (keep system_prompt_version:)
	var filtered []string
	for _, line := range lines {
		if strings.HasPrefix(line, "system_prompt:") && !strings.HasPrefix(line, "system_prompt_version:") {
			continue
		}
		filtered = append(filtered, line)
	}

	// Add/update version line
	found := false
	for i, line := range filtered {
		if strings.HasPrefix(line, "system_prompt_version:") {
			filtered[i] = fmt.Sprintf("system_prompt_version: %d", DefaultSystemPromptVersion)
			found = true
			break
		}
	}
	if !found {
		filtered = append(filtered, fmt.Sprintf("system_prompt_version: %d", DefaultSystemPromptVersion))
	}

	if err := os.WriteFile(path, []byte(strings.Join(filtered, "\n")), 0600); err != nil {
		debug.Log("config", "failed to rewrite system_prompt_version: %v", err)
	}
}
