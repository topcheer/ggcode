package agent

// CounterfactualRepairStep is the deterministic "repair leg" of failure
// attribution (CausalFlow arXiv:2605.25338): the store already answers
// WHICH step was failure-inducing (suspect tool/file/CRS); this answers
// HOW the next run should act differently. Tool-class keyed, short by
// design so FailureHintsForPrompt can afford it inside its budget.
func CounterfactualRepairStep(suspectTool string) string {
	switch suspectTool {
	case "edit_file", "multi_edit_file", "write_file", "multi_file_write", "notebook_edit", "apply_patch":
		return "re-read the file immediately before editing; verify with the narrowest test"
	case "run_command", "start_command":
		return "capture the full error output before retrying; change one variable at a time"
	case "browser", "browser_navigate":
		return "snapshot the DOM before interacting; retry with a fresh selector after navigation"
	}
	switch {
	case containsFold(suspectTool, "test"):
		return "run only the failing test with -run first, not the whole suite"
	case containsFold(suspectTool, "search") || containsFold(suspectTool, "grep"):
		return "narrow the query with a path or type filter before broadening"
	case containsFold(suspectTool, "lsp_"):
		return "re-resolve the symbol position; it may have shifted after earlier edits"
	}
	return "re-read the failing output and change approach"
}

func containsFold(s, sub string) bool {
	n := len(sub)
	if n == 0 {
		return true
	}
	for i := 0; i+n <= len(s); i++ {
		if equalFoldByte(s[i:i+n], sub) {
			return true
		}
	}
	return false
}

func equalFoldByte(a, b string) bool {
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
