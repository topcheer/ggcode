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
}
