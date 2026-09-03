package main

import (
	"fmt"
	"io"
	"os"
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
	// #1444-A: this runs BEFORE Load(), so the migration has NOT happened
	// yet - and with GGCODE_SKIP_AUTOCONFIG=1 it NEVER will (Load skips
	// plaintext migration). The old unconditional "Migrated %d key(s)"
	// was a false safety claim in exactly the scenarios where the user
	// must act manually: migration skipped, or keys.env/YAML write
	// failing (that error only reaches debug.Log in Load). The notice
	// now states what was FOUND and where migration WILL put them, and
	// explicitly tells skip-mode users their keys stay plaintext.
	if os.Getenv("GGCODE_SKIP_AUTOCONFIG") != "" {
		fmt.Fprintf(out, "NOTICE: %d plaintext API key(s) found; auto-migration is DISABLED (GGCODE_SKIP_AUTOCONFIG) - keys remain in plaintext at %s\n", len(findings), cfgFile)
	} else {
		fmt.Fprintf(out, "NOTICE: %d plaintext API key(s) found; they will be migrated to %s during startup\n", len(findings), config.KeysEnvPath())
	}
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
