package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/topcheer/ggcode/internal/config"
)

func confirmPlaintextAPIKeysBeforeTUI(cfgFile string, in io.Reader, out io.Writer, interactive bool) (bool, error) {
	findings, err := config.DetectPlaintextAPIKeys(cfgFile)
	if err != nil {
		return true, nil
	}
	if len(findings) == 0 {
		return true, nil
	}

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
