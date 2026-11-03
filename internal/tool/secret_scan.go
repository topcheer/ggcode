package tool

import (
	"github.com/topcheer/ggcode/internal/security"
)

var secretScanEnabled = true

func scanAndWarn(filePath, content string) string {
	if !secretScanEnabled {
		return ""
	}
	findings := security.ScanForSecrets(filePath, content)
	return security.FormatWarnings(findings)
}
