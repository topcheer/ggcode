//go:build !darwin

package extpane

// newITerm2Backend returns nil on non-macOS platforms.
func newITerm2Backend() Backend { return nil }

// sanitizeAS is a no-op stub for testing on non-darwin.
func sanitizeAS(s string) string { return s }
