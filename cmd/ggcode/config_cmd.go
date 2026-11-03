package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/security"
	"github.com/topcheer/ggcode/internal/version"
)

func newConfigCmd(cfgFile *string) *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Print effective configuration (secrets masked)",
		Long:  "Print the version, the config file in effect, and the merged (global + instance) configuration as YAML. Secret-looking values (api keys, tokens, passwords) are masked before printing.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := *cfgFile
			if path == "" {
				resolved, err := resolveConfigFilePath()
				if err != nil {
					return fmt.Errorf("resolving config path: %w", err)
				}
				path = resolved
			}
			wd, _ := os.Getwd()
			cfg, err := config.LoadWithInstance(path, wd)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			out, err := yaml.Marshal(cfg)
			if err != nil {
				return fmt.Errorf("marshaling config: %w", err)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "ggcode %s\n", version.Display())
			fmt.Fprintf(w, "config file: %s\n", path)
			fmt.Fprintln(w, "---")
			fmt.Fprint(w, maskConfigSecrets(string(out)))
			return nil
		},
	}
}

var secretKeyRe = regexp.MustCompile(`(?i)^(\s*)([\w.-]*(api_keys|api_key|apikey|token|secret|password|private_key)[\w.-]*):\s*(.*)$`)

func maskConfigSecrets(s string) string {
	lines := strings.Split(s, "\n")
	maskListUntil := -1
	for i, ln := range lines {
		trimmedRight := strings.TrimRight(ln, " \t")
		ind := len(trimmedRight) - len(strings.TrimLeft(trimmedRight, " "))
		if maskListUntil >= 0 {
			item := strings.TrimSpace(trimmedRight)
			isItem := strings.HasPrefix(item, "- ")
			if ind > maskListUntil && (isItem || item == "" || item == "-") {
				if isItem {
					lines[i] = maskListItem(trimmedRight)
				}
				continue
			}
			maskListUntil = -1
		}
		m := secretKeyRe.FindStringSubmatch(ln)
		if m == nil {
			continue
		}

		if strings.HasSuffix(strings.ToLower(m[2]), "budget") {
			continue
		}
		val := strings.TrimSpace(m[4])
		switch {
		case val == "":

			maskListUntil = len(m[1])
		case strings.HasPrefix(val, "|") || strings.HasPrefix(val, ">"):

			lines[i] = m[1] + m[2] + ": <masked>"
			maskListUntil = -1
		default:
			lines[i] = m[1] + m[2] + ": " + maskSecret(val)
		}
	}
	return security.RedactForDisplay(strings.Join(lines, "\n"))
}

func maskListItem(ln string) string {
	idx := strings.Index(ln, "- ")
	if idx < 0 {
		return ln
	}
	return ln[:idx+2] + maskSecret(strings.TrimSpace(ln[idx+2:]))
}
