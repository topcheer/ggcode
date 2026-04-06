package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	if shouldPrintRootHelpDirectly(os.Args[1:]) {
		_, _ = writeCLIText(os.Stdout, rootHelpText())
		return
	}
	cmd := NewRootCmd()
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func shouldPrintRootHelpDirectly(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if len(args) == 1 {
		switch strings.TrimSpace(args[0]) {
		case "-h", "--help", "help":
			return true
		}
	}
	return false
}

func rootHelpText() string {
	return `ggcode is a terminal-based AI coding agent powered by LLMs.

Usage:
ggcode [flags]
ggcode [command]

Available Commands:
- completion: Generate shell completion script
- help: Help about any command

Flags:
- --allowedTools stringArray: tools to allow in pipe mode (can be repeated)
- --bypass: start in bypass permission mode (auto-approve safe ops, warn on dangerous)
- --config string: config file path
- -h, --help: help for ggcode
- --output string: output file path (default: stdout)
- -p, --prompt string: pipe mode: non-interactive execution with a prompt
- --resume string: resume a previous session by ID

Use "ggcode [command] --help" for more information about a command.
`
}
