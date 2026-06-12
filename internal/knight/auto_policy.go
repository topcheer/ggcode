package knight

import (
	"fmt"
	"strings"
)

type AutoPolicy struct {
	Name        string
	Mode        string
	Description string
	Guardrail   string
	// Effective is true if this policy is currently active given the running
	// trust level and capability set. Disabled policies still appear so users
	// can audit what Knight could do under a different config.
	Effective bool
	Reason    string
}

func (k *Knight) AutoPolicies() []AutoPolicy {
	trust := strings.ToLower(strings.TrimSpace(k.cfg.TrustLevel))
	if trust == "" {
		trust = "staged"
	}
	hasCap := func(name string) bool {
		for _, c := range k.cfg.Capabilities {
			if strings.EqualFold(c, name) {
				return true
			}
		}
		return false
	}

	policies := []AutoPolicy{
		{
			Name:        "skill metadata sync",
			Mode:        "automatic",
			Description: "Sync usage, prompt exposure, outcome, and feedback counters into active Knight skill frontmatter.",
			Guardrail:   "Only metadata fields are updated; skill instructions are not rewritten by this policy.",
		},
		{
			Name:        "prompt-signal skill tuning",
			Mode:        "staged",
			Description: "Use prompt exposure/outcome signals to draft revised trigger guidance for low-signal skills.",
			Guardrail:   "Revisions are written to staging and active skill updates always require explicit review.",
		},
		{
			Name:        "new project skill auto-promotion",
			Mode:        "gated auto",
			Description: "Auto-promote only new Knight-created project skills that pass static validation, scenario replay, saved-scenario replay, baseline replay, and a deterministic rule-based overlap check.",
			Guardrail:   "Blocked for global skills, external command requirements, validation warnings, existing-skill revisions, saved replay mismatches, active baseline overlap, or rule-based fingerprint similarity above threshold.",
		},
		{
			Name:        "project improvement proposal",
			Mode:        "manual proposal",
			Description: "Draft reviewable project-improvement proposals under .ggcode/project-proposals.",
			Guardrail:   "Proposal generation must not modify project source/config/test/docs files; implementation remains a separate user-triggered task.",
		},
		{
			Name:        "project code writes",
			Mode:        "never automatic",
			Description: "Knight does not silently modify user project code in the background.",
			Guardrail:   "Any project code change must go through normal user-visible agent execution, permissions, and validation.",
		},
	}

	for i := range policies {
		p := &policies[i]
		switch p.Name {
		case "skill metadata sync":
			p.Effective = trust != "readonly"
			if !p.Effective {
				p.Reason = fmt.Sprintf("disabled because trust_level=%s; metadata sync requires write access", trust)
			}
		case "prompt-signal skill tuning":
			p.Effective = trust != "readonly" && hasCap("skill_creation")
			if !p.Effective {
				if trust == "readonly" {
					p.Reason = "disabled because trust_level=readonly"
				} else if !hasCap("skill_creation") {
					p.Reason = "disabled because skill_creation capability is not enabled"
				}
			}
		case "new project skill auto-promotion":
			p.Effective = trust == "auto" && hasCap("skill_creation") && hasCap("skill_validation")
			if !p.Effective {
				switch {
				case trust != "auto":
					p.Reason = fmt.Sprintf("disabled because trust_level=%s (auto required for promotion)", trust)
				case !hasCap("skill_creation"):
					p.Reason = "disabled because skill_creation capability is not enabled"
				case !hasCap("skill_validation"):
					p.Reason = "disabled because skill_validation capability is not enabled"
				}
			}
		case "project improvement proposal":
			p.Effective = trust != "readonly"
			if !p.Effective {
				p.Reason = "disabled because trust_level=readonly cannot persist proposals"
			}
		case "project code writes":
			p.Effective = false
			p.Reason = "intentionally never automatic"
		}
	}
	return policies
}
