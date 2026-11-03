package config

var instanceResetManagedKeys = []string{
	"vendor",
	"endpoint",
	"model",
	"language",
	"system_prompt",
	"default_mode",
	"max_iterations",
	"allowed_dirs",

	"hooks",
}

var instanceResetNestedKeys = map[string][]string{
	"knight":    {"trust_level", "daily_token_budget", "idle_delay_sec"},
	"subagents": {"max_concurrent", "timeout"},
	"swarm":     {"max_teammates_per_team", "teammate_timeout", "inbox_size", "poll_interval"},

	"a2a": {"disabled", "port", "host", "max_tasks", "task_timeout"},
}

func applyInstanceResets(existingRaw, delta map[string]interface{}, instanceFields map[string]bool) {
	if len(instanceFields) == 0 {
		return
	}
	for _, key := range instanceResetManagedKeys {
		if !instanceFields[key] {
			continue
		}
		if _, onDisk := existingRaw[key]; !onDisk {
			continue
		}
		if _, inDelta := delta[key]; inDelta {
			continue
		}
		delta[key] = nil
	}

	for key, subKeys := range instanceResetNestedKeys {
		if !instanceFields[key] {
			continue
		}
		diskSection, ok := existingRaw[key].(map[string]interface{})
		if !ok || len(diskSection) == 0 {
			continue
		}
		deltaSection, inDelta := delta[key].(map[string]interface{})
		if !inDelta {

			deltaSection = map[string]interface{}{}
			delta[key] = deltaSection
		}
		for _, sk := range subKeys {
			if _, onDisk := diskSection[sk]; !onDisk {
				continue
			}
			if _, inDeltaSub := deltaSection[sk]; inDeltaSub {
				continue
			}
			deltaSection[sk] = nil
		}
	}

	if instanceFields["vendors"] {
		if diskSection, ok := existingRaw["vendors"].(map[string]interface{}); ok && len(diskSection) > 0 {
			deltaSection, inDelta := delta["vendors"].(map[string]interface{})
			if !inDelta {

				deltaSection = map[string]interface{}{}
				delta["vendors"] = deltaSection
			}
			for k := range diskSection {
				if _, kept := deltaSection[k]; kept {
					continue
				}
				deltaSection[k] = nil
			}
		}
	}
}
