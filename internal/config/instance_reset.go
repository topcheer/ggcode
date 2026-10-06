package config

// instanceResetKeys are the top-level scalar/list fields managed by
// marshalInstanceDelta's diffScalar/diffInt/diffStringSlice helpers. When a
// field's current value has been reset back to the global value (or zero),
// the diff omits the key entirely - but if the on-disk instance.yaml still
// carries an override for it, the deep-merge in SaveInstance would keep the
// stale value and MergeInstance would resurrect it on the next load (#2106).
//
// applyInstanceResets closes that hole: for every managed key present on
// disk but absent from the delta, it writes an explicit nil deletion marker
// into the delta. deepMergeYAMLMaps interprets nil as "delete the key",
// so the reset actually reaches the file. When every override is reset the
// merged file becomes empty and SaveInstance removes it altogether.
//
// Only top-level scalar-family keys are handled. Nested structures (ui, im,
// vendors, ...) have partial-reset semantics of their own and are left to
// their dedicated diff helpers.
var instanceResetManagedKeys = []string{
	"vendor",
	"endpoint",
	"model",
	"language",
	"system_prompt",
	"default_mode",
	"max_iterations",
	"allowed_dirs",
}

// instanceResetNestedKeys maps nested top-level keys to the scalar
// sub-keys managed by their dedicated diff helpers (diffKnight/
// diffSubAgents/diffSwarm). When a nested field is reset back to the
// global default and the global default is the zero value (0/""/"0s"),
// current == global so the diff omits the sub-key entirely - the same
// #2106 resurrection hole as the scalar keys above, but one level down.
// applyInstanceResets closes it with a nested nil deletion marker:
// deepMergeYAMLMaps recurses into the section map and interprets nil
// as "delete the key" at that level too (#2936).
var instanceResetNestedKeys = map[string][]string{
	"knight":    {"trust_level", "daily_token_budget", "idle_delay_sec"},
	"subagents": {"max_concurrent", "timeout"},
	"swarm":     {"max_teammates_per_team", "teammate_timeout", "inbox_size", "poll_interval"},
}

// applyInstanceResets adds nil deletion markers to delta for managed keys
// that exist in the on-disk instance map but are absent from the delta
// (i.e. current == global: the user reset them).
//
// Scope guard: only keys listed in instanceFields - the fields this
// session actually loaded FROM the instance file - may be deleted. Other
// on-disk keys are not governed by the delta machinery (e.g. language is
// handled by a dedicated load path and never lands in instanceFields),
// and deleting them would eat live data (#734 regression probe).
func applyInstanceResets(existingRaw, delta map[string]interface{}, instanceFields map[string]bool) {
	if len(instanceFields) == 0 {
		return
	}
	for _, key := range instanceResetManagedKeys {
		if !instanceFields[key] {
			continue // not governed by this session's delta
		}
		if _, onDisk := existingRaw[key]; !onDisk {
			continue
		}
		if _, inDelta := delta[key]; inDelta {
			continue // still overridden - normal write path
		}
		delta[key] = nil // reset back to global - delete from disk
	}

	// Nested sections (#2936): a sub-key reset to the global default is
	// absent from the section delta (current == global), so the deep-merge
	// would keep the stale on-disk sub-key and MergeInstance resurrect it.
	// Mark such sub-keys nil INSIDE the section delta map - the recursive
	// deepMerge honors the deletion marker at that level. Sub-keys still in
	// the delta are explicit overrides (including explicit zeros written by
	// the no-suppression diff when global != 0) and are left untouched.
	for key, subKeys := range instanceResetNestedKeys {
		if !instanceFields[key] {
			continue // section not governed by this session's delta
		}
		diskSection, ok := existingRaw[key].(map[string]interface{})
		if !ok || len(diskSection) == 0 {
			continue // nothing on disk to reset
		}
		deltaSection, inDelta := delta[key].(map[string]interface{})
		if !inDelta {
			// Whole section back at global defaults but stale sub-keys
			// remain on disk - create the section delta to carry deletions.
			deltaSection = map[string]interface{}{}
			delta[key] = deltaSection
		}
		for _, sk := range subKeys {
			if _, onDisk := diskSection[sk]; !onDisk {
				continue
			}
			if _, inDeltaSub := deltaSection[sk]; inDeltaSub {
				continue // explicit override (possibly an explicit zero)
			}
			deltaSection[sk] = nil // nested reset - delete from disk
		}
	}
}
