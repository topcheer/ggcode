package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/topcheer/ggcode/internal/config"
)

// confirmPlaintextAPIKeysBeforeTUI is kept as a no-op hook. Plaintext API
// keys are now auto-migrated during config.Load() — they are persisted to
// ~/.ggcode/keys.env and the YAML is rewritten to use ${VAR} references.
// This function remains for call-site compatibility.
func confirmPlaintextAPIKeysBeforeTUI(cfgFile string, in io.Reader, out io.Writer, interactive bool) (bool, error) {
	findings, err := config.DetectPlaintextAPIKeys(cfgFile)
	if err != nil {
		return true, nil // already migrated by Load(), ignore errors
	}
	if len(findings) == 0 {
		return true, nil
	}
	// If findings still exist, they were migrated during Load().
	// Print an informational notice.
	fmt.Fprintf(out, "Migrated %d plaintext API key(s) to %s\n", len(findings), config.KeysEnvPath())
	for _, finding := range findings {
		if finding.Section == "vendor" {
			if strings.TrimSpace(finding.Endpoint) != "" {
				fmt.Fprintf(out, "  %s/%s -> ${%s}\n", finding.Vendor, finding.Endpoint, finding.EnvVar)
			} else {
				fmt.Fprintf(out, "  %s -> ${%s}\n", finding.Vendor, finding.EnvVar)
			}
		} else {
			fmt.Fprintf(out, "  %s -> ${%s}\n", finding.KeyPath, finding.EnvVar)
		}
	}
	return true, nil
}
