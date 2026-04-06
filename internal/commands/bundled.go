package commands

func bundledSkills() []*Command {
	return []*Command{
		{
			Name:          "verify",
			DisplayName:   "Verify work",
			Description:   "Check whether an implementation is actually complete before declaring success.",
			WhenToUse:     "Use when code changes are done and you need to confirm the result with targeted validation.",
			Template:      "Before concluding, verify the work against the user's actual request. Prefer the smallest existing test/build/check commands that prove the change works, and summarize any real gap instead of assuming success.",
			Source:        SourceBundled,
			LoadedFrom:    LoadedFromBundled,
			UserInvocable: false,
		},
		{
			Name:          "debug",
			DisplayName:   "Debug systematically",
			Description:   "Work backward from symptoms to root cause instead of stacking guesses.",
			WhenToUse:     "Use when tests fail, a runtime error appears, or behavior does not match expectations.",
			Template:      "Debug systematically: reproduce the issue, isolate the failing layer, compare expected versus actual behavior, identify the root cause, and only then implement the narrowest correct fix.",
			Source:        SourceBundled,
			LoadedFrom:    LoadedFromBundled,
			UserInvocable: false,
			Context:       "fork",
		},
		{
			Name:          "simplify",
			DisplayName:   "Simplify design",
			Description:   "Reduce unnecessary complexity before adding more logic.",
			WhenToUse:     "Use when the current approach feels over-engineered or duplicated.",
			Template:      "Simplify the approach before adding more code. Prefer reusing existing helpers, removing duplication, and shrinking the surface area of the solution while preserving correctness.",
			Source:        SourceBundled,
			LoadedFrom:    LoadedFromBundled,
			UserInvocable: false,
		},
	}
}
