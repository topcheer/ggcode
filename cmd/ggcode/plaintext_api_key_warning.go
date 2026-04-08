package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/topcheer/ggcode/internal/config"
)

func confirmPlaintextAPIKeysBeforeTUI(cfgFile string, in io.Reader, out io.Writer, interactive bool) (bool, error) {
	findings, err := config.DetectPlaintextAPIKeys(cfgFile)
	if err != nil {
		return false, err
	}
	if len(findings) == 0 {
		return true, nil
	}
	ignored, err := config.IsPlaintextAPIKeyWarningIgnored(cfgFile)
	if err != nil {
		return false, err
	}
	if ignored {
		return true, nil
	}
	if !interactive {
		return false, fmt.Errorf("plaintext api keys detected in %s; migrate them to environment variables or start interactively to confirm once", cfgFile)
	}

	fmt.Fprintf(out, "Warning: plaintext API keys detected in %s.\n", cfgFile)
	fmt.Fprintln(out, "These entries should use environment variables instead:")
	for _, finding := range findings {
		if strings.TrimSpace(finding.Endpoint) != "" {
			fmt.Fprintf(out, "- %s/%s -> ${%s}\n", finding.Vendor, finding.Endpoint, finding.EnvVar)
			continue
		}
		fmt.Fprintf(out, "- %s -> ${%s}\n", finding.Vendor, finding.EnvVar)
	}
	fmt.Fprintln(out, "Continue startup? [y]es / [n]o / [i]gnore forever")

	reader := bufio.NewReader(in)
	for {
		fmt.Fprint(out, "> ")
		line, err := reader.ReadString('\n')
		if err != nil && len(line) == 0 {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return true, nil
		case "n", "no", "":
			fmt.Fprintln(out, "Startup cancelled.")
			return false, nil
		case "i", "ignore":
			if err := config.IgnorePlaintextAPIKeyWarning(cfgFile); err != nil {
				return false, err
			}
			return true, nil
		default:
			fmt.Fprintln(out, "Please enter y, n, or i.")
		}
	}
}
