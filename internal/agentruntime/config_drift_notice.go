package agentruntime

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ConfigDriftNotice describes a detected change to a watched config file and
// its effect on the running session.
//
// ConfigHotReload has always applied a conservative field-level refresh
// (#763), but the only feedback was a debug log: a user editing ggcode.yaml
// mid-session got no signal that (a) the edit was seen, (b) which parts took
// effect immediately, and (c) which parts silently keep the startup snapshot
// until restart. Frontier agent harnesses (2025-2026 "harness engineering"
// practice) treat config-as-code drift as a first-class observability event;
// this notice closes that UX gap without changing reload semantics.
type ConfigDriftNotice struct {
	At          time.Time
	File        string   // watched file whose edit triggered this notice
	ChangedKeys []string // top-level YAML keys differing from the baseline
	AppliedKeys []string // changed keys refreshed on the live config already
	RestartKeys []string // changed keys that need a session restart to apply
	InvalidYAML string   // non-empty when the reload was skipped (parse error)
}

// Message renders a user-facing line for the TUI system area.
func (n ConfigDriftNotice) Message() string {
	if n.InvalidYAML != "" {
		file := n.File
		if file == "" {
			file = "config"
		}
		return fmt.Sprintf("Config change detected in %s but NOT applied (invalid YAML: %s). Last good config stays active - fix the file to reload.", file, n.InvalidYAML)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Config drift detected in %s:", n.File)
	if len(n.AppliedKeys) > 0 {
		fmt.Fprintf(&b, " applied now: %s;", strings.Join(n.AppliedKeys, ", "))
	}
	if len(n.RestartKeys) > 0 {
		fmt.Fprintf(&b, " needs session restart: %s.", strings.Join(n.RestartKeys, ", "))
	}
	if len(n.AppliedKeys) == 0 && len(n.RestartKeys) == 0 {
		fmt.Fprintf(&b, " no key-level differences detected.")
	}
	return strings.TrimRight(b.String(), " ")
}

// hotReloadAppliedKeys lists the top-level ggcode.yaml keys applyFreshConfig
// refreshes in place. Any other changed key is reported as restart-required,
// matching the field-conservative policy documented on ConfigHotReload.
var hotReloadAppliedKeys = map[string]struct{}{
	"vendors":              {},
	"fallback":             {},
	"fallbacks":            {},
	"knight":               {},
	"max_iterations":       {},
	"session_token_budget": {},
	"tool_call_budget":     {},
}

// externalVendorDefsFile is the suffix of the watched external vendor-define
// file. Its content feeds Vendors wholesale, so every key change is applied
// live.
const externalVendorDefsFile = "vendors.yaml"

// yamlTopLevelMap parses YAML into its top-level key/value map. An empty or
// scalar-only document yields an empty map, not an error.
func yamlTopLevelMap(data []byte) (map[string]any, error) {
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// diffTopLevelKeys returns sorted top-level keys whose value differs between
// the two snapshots (added, removed, or changed). Comment-only rewrites
// produce an empty diff.
func diffTopLevelKeys(old, cur map[string]any) []string {
	var changed []string
	for k, ov := range old {
		cv, ok := cur[k]
		if !ok || !reflect.DeepEqual(ov, cv) {
			changed = append(changed, k)
		}
	}
	for k := range cur {
		if _, ok := old[k]; !ok {
			changed = append(changed, k)
		}
	}
	sort.Strings(changed)
	return changed
}

// classifyDriftKeys splits changed keys into applied-now vs restart-required
// based on which file they live in.
func classifyDriftKeys(file string, changed []string) (applied, restart []string) {
	if strings.HasSuffix(file, externalVendorDefsFile) {
		return append([]string{}, changed...), nil
	}
	for _, k := range changed {
		if _, ok := hotReloadAppliedKeys[k]; ok {
			applied = append(applied, k)
		} else {
			restart = append(restart, k)
		}
	}
	return applied, restart
}
