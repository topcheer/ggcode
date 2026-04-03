package tool

// RegisterBuiltinTools registers all built-in tools.
func RegisterBuiltinTools(registry *Registry) error {
	tools := []Tool{
		// File operations
		ReadFile{},
		WriteFile{},
		ListDir{},
		EditFile{},

		// Search
		SearchFiles{},
		Glob{},

		// Execution
		RunCommand{},

		// Git
		GitStatus{},
		GitDiff{},
		GitLog{},
	}
	for _, t := range tools {
		if err := registry.Register(t); err != nil {
			return err
		}
	}
	return nil
}
