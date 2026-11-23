package agent

// isEditingTool reports whether a tool mutates source files. It used to
// live in error_cascade.go (removed with the "[Error Cascade]" guidance
// injection); it is still used by the tool loop and the verify hint.
func isEditingTool(name string) bool {
	return sourceMutatingTools[name]
}
