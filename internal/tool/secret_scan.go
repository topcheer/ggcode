package tool

import (
	"github.com/topcheer/ggcode/internal/security"
)

// secretScanEnabled controls whether post-write secret scanning is active.
// Defaults to true. Can be disabled via configuration.
var secretScanEnabled = true

// SetSecretScanEnabled enables or disables post-write secret scanning globally.
func SetSecretScanEnabled(enabled bool) {
	secretScanEnabled = enabled
}

// scanAndWarn performs a secret scan on the given file content and returns
// a formatted warning string if any secrets are found. Returns empty string
// if scanning is disabled or no secrets are found.
//
// This is called by write_file, edit_file, multi_file_write, and
// multi_file_edit after a successful write to alert the agent and user
// about potential credential leaks in source files.
func scanAndWarn(filePath, content string) string {
	if !secretScanEnabled {
		return ""
	}
	findings := security.ScanForSecrets(filePath, content)
	return security.FormatWarnings(findings)
}
