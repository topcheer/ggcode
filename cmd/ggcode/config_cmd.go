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

// newConfigCmd builds `ggcode config`: print the effective main configuration
// with secrets masked. The existing root --config flag (which SELECTS the
// config file path) composes with this subcommand:
//
//	ggcode config                      # resolved default config
//	ggcode --config=foo.yaml config    # explicit file's effective view
//
// Printing is display-only: the loaded *config.Config is never mutated and
// nothing is written back to disk.
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

// secretKeyRe matches YAML mapping keys whose name marks the value as a
// secret regardless of shape: api_key(s), token, secret, password,
// private_key (case-insensitive, underscores/dashes tolerated).
var secretKeyRe = regexp.MustCompile(`(?i)^(\s*)([\w.-]*(api_keys|api_key|apikey|token|secret|password|private_key)[\w.-]*):\s*(.*)$`)

// maskConfigSecrets makes marshaled config YAML safe to print. Two passes:
//
//  1. Key-name pass: any line whose key marks a secret has its inline value
//     masked (maskSecret), and list items directly under such a key are
//     masked item by item. Empty values stay empty (unset ≠ hidden).
//  2. Value-shape pass: security.RedactForDisplay catches well-formed secret
//     VALUES under innocent key names (e.g. an "Authorization: Bearer sk-..."
//     header entry in an MCP server block), which the key-name pass cannot
//     see. Env references like ${VAR} contain no secret material and pass
//     through both passes untouched.
func maskConfigSecrets(s string) string {
	lines := strings.Split(s, "\n")
	maskListUntil := -1 // indent of the secret key whose list items we are masking
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
			maskListUntil = -1 // dedent or non-list line ends the list
		}
		m := secretKeyRe.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		// "token_budget"/"session_token_budget" are budget knobs, not
		// secrets (Go regexp has no lookahead, so exclude by suffix).
		if strings.HasSuffix(strings.ToLower(m[2]), "budget") {
			continue
		}
		val := strings.TrimSpace(m[4])
		switch {
		case val == "":
			// Value on following line(s): a list of secret items ("api_keys:\n
			// - sk-..."). Arm list masking at this key's indent level.
			maskListUntil = len(m[1])
		case strings.HasPrefix(val, "|") || strings.HasPrefix(val, ">"):
			// Block scalar under a secret key: mask conservatively by
			// dropping to a placeholder (block content is multi-line and
			// cannot be masked line-wise without re-parsing).
			lines[i] = m[1] + m[2] + ": <masked>"
			maskListUntil = -1
		default:
			lines[i] = m[1] + m[2] + ": " + maskSecret(val)
		}
	}
	return security.RedactForDisplay(strings.Join(lines, "\n"))
}

// maskListItem masks the value of a "- value" list entry, preserving indent.
func maskListItem(ln string) string {
	idx := strings.Index(ln, "- ")
	if idx < 0 {
		return ln
	}
	return ln[:idx+2] + maskSecret(strings.TrimSpace(ln[idx+2:]))
}
