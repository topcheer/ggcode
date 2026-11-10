package agentruntime

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// gatesAccessAdapter implements tool.GatesAccess over the runtime config
// access layer so merge_preflight CRUD goes through the same locked,
// persisted config path as the generic config tool (LLM channel b).
type gatesAccessAdapter struct{ inner *configAccess }

func (g gatesAccessAdapter) Describe() (string, error)           { return g.inner.gatesDescribe() }
func (g gatesAccessAdapter) Set(key, value string) error         { return g.inner.gatesSetTool(key, value) }
func (g gatesAccessAdapter) Unset(key, value string) error       { return g.inner.gatesUnsetTool(key, value) }
func (g gatesAccessAdapter) Test(command string) (string, error) { return g.inner.gatesTest(command) }
func (g gatesAccessAdapter) HoldPatterns() ([]*regexp.Regexp, error) {
	return g.inner.gatesHoldPatterns()
}

// gatesDescribe renders the effective gates.merge configuration.
// Nothing in gates is secret, so values are shown as configured.
func (a *configAccess) gatesDescribe() (string, error) {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	if a.cfg == nil {
		return "", fmt.Errorf("config is nil")
	}
	return a.gatesDescribeLocked(), nil
}

func (a *configAccess) gatesDescribeLocked() string {
	m := a.cfg.Gates.Merge
	out := struct {
		Enabled       bool     `json:"enabled"`
		Commands      []string `json:"commands"`
		HoldPatterns  []string `json:"hold_patterns,omitempty"`
		PreflightTTL  string   `json:"preflight_ttl"`
		DefaultsInUse []string `json:"defaults_in_use,omitempty"`
	}{
		Enabled:      m.EffectiveEnabled(),
		Commands:     m.EffectiveCommands(),
		HoldPatterns: m.EffectiveHoldPatterns(),
		PreflightTTL: m.EffectivePreflightTTL().String(),
	}
	if m.Enabled == nil {
		out.DefaultsInUse = append(out.DefaultsInUse, "enabled")
	}
	if len(m.Commands) == 0 {
		out.DefaultsInUse = append(out.DefaultsInUse, "commands")
	}
	if len(m.HoldPatterns) == 0 {
		out.DefaultsInUse = append(out.DefaultsInUse, "hold_patterns")
	}
	if m.PreflightTTL == 0 {
		out.DefaultsInUse = append(out.DefaultsInUse, "preflight_ttl")
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return "gates.merge (effective values; defaults_in_use lists keys at their default):\n" + string(b) + "\n"
}

// gatesKeyFull accepts both short keys ("enabled", tool channel) and
// full paths ("gates.merge.enabled", config-tool channel).
func gatesKeyFull(key string) string {
	k := strings.TrimSpace(key)
	if !strings.HasPrefix(k, "gates.merge.") {
		k = "gates.merge." + k
	}
	return k
}

// gatesSetTool validates and persists one gates key (LLM channel b).
func (a *configAccess) gatesSetTool(key, value string) error {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	if a.cfg == nil {
		return fmt.Errorf("config is nil")
	}
	full := gatesKeyFull(key)
	handled, err := a.setGatesSectionKey(full, value)
	if !handled {
		return fmt.Errorf("unknown gates key %q (use enabled, commands, hold_patterns, preflight_ttl)", key)
	}
	if err != nil {
		return err
	}
	return a.saveAndPatch(full, value)
}

// gatesUnsetTool resets a scalar gates key to its default or removes a
// list entry by value (LLM channel b).
func (a *configAccess) gatesUnsetTool(key, value string) error {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	if a.cfg == nil {
		return fmt.Errorf("config is nil")
	}
	key = strings.TrimPrefix(gatesKeyFull(key), "gates.merge.")
	switch key {
	case "enabled":
		a.cfg.Gates.Merge.Enabled = nil
	case "preflight_ttl":
		a.cfg.Gates.Merge.PreflightTTL = 0
	case "commands", "hold_patterns":
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("unset of %q requires value (the list entry to remove)", key)
		}
		list := a.cfg.Gates.Merge.Commands
		if key == "hold_patterns" {
			list = a.cfg.Gates.Merge.HoldPatterns
		}
		kept := list[:0:0]
		found := false
		for _, entry := range list {
			if entry == value {
				found = true
				continue
			}
			kept = append(kept, entry)
		}
		if !found {
			return fmt.Errorf("%q not present in gates.merge.%s", value, key)
		}
		if key == "commands" {
			a.cfg.Gates.Merge.Commands = kept
		} else {
			a.cfg.Gates.Merge.HoldPatterns = kept
		}
	default:
		return fmt.Errorf("unknown gates key %q (use enabled, commands, hold_patterns, preflight_ttl)", key)
	}
	return a.saveAndPatch("gates.merge."+key, "")
}

// gatesTest dry-runs a command against the protected patterns and the
// current session's preflight snapshots. It performs no writes.
func (a *configAccess) gatesTest(command string) (string, error) {
	a.cfgMu.RLock()
	var m config.MergeGateConfig
	var ag interface {
		MergeGatePreflightStatus(int) (bool, string)
	}
	if a.cfg != nil {
		m = a.cfg.Gates.Merge
	}
	if a.agentInst != nil {
		ag = a.agentInst
	}
	a.cfgMu.RUnlock()

	cmd := strings.TrimSpace(command)
	var b strings.Builder
	fmt.Fprintf(&b, "command: %s\n", cmd)
	fmt.Fprintf(&b, "gates.merge.enabled: %v\n", m.EffectiveEnabled())
	if !m.EffectiveEnabled() {
		b.WriteString("protected: no (gate disabled)\ndry-run only: nothing was executed or written\n")
		return b.String(), nil
	}
	pr, hit := config.MatchMergeGateCommand(cmd, m.EffectiveCommands())
	if !hit {
		fmt.Fprintf(&b, "protected: no (no pattern in %v matches)\ndry-run only: nothing was executed or written\n", m.EffectiveCommands())
		return b.String(), nil
	}
	fmt.Fprintf(&b, "protected: yes (prefix in gates.merge.commands)\npr: %d\n", pr)
	if ag != nil {
		if fresh, head := ag.MergeGatePreflightStatus(pr); fresh {
			fmt.Fprintf(&b, "preflight snapshot: fresh (head %s, within ttl %s) - gate requirement satisfied\n",
				head, m.EffectivePreflightTTL())
		} else {
			fmt.Fprintf(&b, "preflight snapshot: none or stale (ttl %s) - run merge_preflight {\"pr_number\":%d} first\n",
				m.EffectivePreflightTTL(), pr)
		}
	} else {
		fmt.Fprintf(&b, "preflight snapshot: unknown (no live agent session in this runtime)\n")
	}
	b.WriteString("dry-run only: nothing was executed or written\n")
	return b.String(), nil
}

// gatesHoldPatterns compiles the configured hold regexes for the
// merge_preflight tool.
func (a *configAccess) gatesHoldPatterns() ([]*regexp.Regexp, error) {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	if a.cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	return a.cfg.Gates.Merge.CompiledHoldPatterns()
}

// MergeGateConfig returns the live gates.merge config (used as the
// agent-side merge gate's config provider, so writes via either channel
// take effect immediately).
func (a *configAccess) MergeGateConfig() config.MergeGateConfig {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	if a.cfg == nil {
		return config.MergeGateConfig{}
	}
	return a.cfg.Gates.Merge
}

// --- generic config tool section (LLM channel a) ---

func (a *configAccess) getGatesSectionKey(key string) (string, bool, error) {
	switch key {
	case "gates.merge":
		return a.gatesDescribeLocked(), true, nil
	case "gates.merge.enabled":
		return strconv.FormatBool(a.cfg.Gates.Merge.EffectiveEnabled()), true, nil
	case "gates.merge.commands":
		b, _ := json.Marshal(a.cfg.Gates.Merge.EffectiveCommands())
		return string(b), true, nil
	case "gates.merge.hold_patterns":
		b, _ := json.Marshal(a.cfg.Gates.Merge.EffectiveHoldPatterns())
		return string(b), true, nil
	case "gates.merge.preflight_ttl":
		return a.cfg.Gates.Merge.EffectivePreflightTTL().String(), true, nil
	}
	return "", false, nil
}

// setGatesSectionKey validates and applies one gates.merge.* key.
// Caller must hold cfgMu for writing.
func (a *configAccess) setGatesSectionKey(key, value string) (bool, error) {
	switch key {
	case "gates.merge.enabled":
		b, err := parseGatesBool(value)
		if err != nil {
			return true, fmt.Errorf("invalid gates.merge.enabled: %w", err)
		}
		a.cfg.Gates.Merge.Enabled = &b
		return true, nil
	case "gates.merge.commands":
		cmds, err := parseGatesStringList(value)
		if err != nil {
			return true, fmt.Errorf("invalid gates.merge.commands: %w", err)
		}
		for i, c := range cmds {
			if strings.TrimSpace(c) == "" {
				return true, fmt.Errorf("gates.merge.commands[%d] must not be empty", i)
			}
		}
		a.cfg.Gates.Merge.Commands = cmds
		return true, nil
	case "gates.merge.hold_patterns":
		pats, err := parseGatesStringList(value)
		if err != nil {
			return true, fmt.Errorf("invalid gates.merge.hold_patterns: %w", err)
		}
		old := a.cfg.Gates.Merge.HoldPatterns
		a.cfg.Gates.Merge.HoldPatterns = pats
		if _, cerr := a.cfg.Gates.Merge.CompiledHoldPatterns(); cerr != nil {
			// Roll back so a rejected value never lingers in memory.
			a.cfg.Gates.Merge.HoldPatterns = old
			return true, cerr
		}
		return true, nil
	case "gates.merge.preflight_ttl":
		d, err := parseGatesDuration(value)
		if err != nil {
			return true, fmt.Errorf("invalid gates.merge.preflight_ttl: %w", err)
		}
		if d != 0 && (d < config.MinMergeGatePreflightTTL || d > config.MaxMergeGatePreflightTTL) {
			return true, fmt.Errorf("gates.merge.preflight_ttl %s must be between %s and %s",
				d, config.MinMergeGatePreflightTTL, config.MaxMergeGatePreflightTTL)
		}
		a.cfg.Gates.Merge.PreflightTTL = d
		return true, nil
	}
	return false, nil
}

func (a *configAccess) listSectionGates() string {
	return a.gatesDescribeLocked()
}

func parseGatesBool(value string) (bool, error) {
	if b, err := strconv.ParseBool(strings.TrimSpace(value)); err == nil {
		return b, nil
	}
	var b bool
	if err := json.Unmarshal([]byte(value), &b); err != nil {
		return false, fmt.Errorf("want a boolean, got %q", value)
	}
	return b, nil
}

// parseGatesStringList accepts a JSON array of strings or a bare
// comma-separated list.
func parseGatesStringList(value string) ([]string, error) {
	trimmed := strings.TrimSpace(value)
	var items []string
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal([]byte(trimmed), &items); err != nil {
			return nil, fmt.Errorf("want a JSON array of strings, got %q", value)
		}
		return items, nil
	}
	if trimmed == "" {
		return nil, fmt.Errorf("want a JSON array of strings")
	}
	for _, part := range strings.Split(trimmed, ",") {
		items = append(items, strings.TrimSpace(part))
	}
	return items, nil
}

// parseGatesDuration accepts a duration string ("90s", "2m"), a JSON
// string holding one, or a bare number of seconds.
func parseGatesDuration(value string) (time.Duration, error) {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.Trim(trimmed, `"`)
	if d, err := time.ParseDuration(trimmed); err == nil {
		return d, nil
	}
	if n, err := strconv.ParseFloat(trimmed, 64); err == nil {
		return time.Duration(n * float64(time.Second)), nil
	}
	return 0, fmt.Errorf("want a duration like \"90s\", got %q", value)
}
