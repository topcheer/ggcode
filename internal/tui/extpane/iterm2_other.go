//go:build !darwin

package extpane

func newITerm2Backend() Backend { return nil }

func sanitizeAS(s string) string { return s }
