package main

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/hooks"
)

// hooksEventEntry pairs an event name with its configured hooks.
type hooksEventEntry struct {
	Event string
	Hooks []hooks.Hook
}

// hooksEventEntries returns all configured hooks grouped by event, in the
// lifecycle order used by internal/hooks documentation (Dispatch order).
func hooksEventEntries(cfg hooks.HookConfig) []hooksEventEntry {
	return []hooksEventEntry{
		{hooks.EventOnUserMessage, cfg.OnUserMessage},
		{hooks.EventPreToolUse, cfg.PreToolUse},
		{hooks.EventPostToolUse, cfg.PostToolUse},
		{hooks.EventOnStreamStop, cfg.OnStreamStop},
		{hooks.EventOnAgentStop, cfg.OnAgentStop},
		{hooks.EventPreCompact, cfg.PreCompact},
		{hooks.EventOnCompaction, cfg.OnCompaction},
	}
}

// hooksCount returns the total number of configured hooks across all events.
func hooksCount(cfg hooks.HookConfig) int {
	n := 0
	for _, e := range hooksEventEntries(cfg) {
		n += len(e.Hooks)
	}
	return n
}

// hookTarget renders the action a hook performs (shell command or HTTP target).
func hookTarget(h hooks.Hook) string {
	if h.HasType() == hooks.HookTypeHTTP {
		method := h.Method
		if method == "" {
			method = "POST"
		}
		return method + " " + h.URL
	}
	return h.Command
}

// effectiveMatchMode returns "glob" when MatchMode is unset.
func effectiveMatchMode(h hooks.Hook) string {
	if h.MatchMode == "" {
		return "glob"
	}
	return h.MatchMode
}

// writeHooksList prints every configured hook grouped by event. Returns the
// number of hooks printed.
func writeHooksList(w io.Writer, cfg hooks.HookConfig) (int, error) {
	total := 0
	for _, e := range hooksEventEntries(cfg) {
		if len(e.Hooks) == 0 {
			continue
		}
		fmt.Fprintf(w, "%s (%d)\n", e.Event, len(e.Hooks))
		tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
		for i, h := range e.Hooks {
			fmt.Fprintf(tw, "  %d\t%s\tmode=%s\ttype=%s\t%s\n",
				i, h.Match, effectiveMatchMode(h), h.HasType(), hookTarget(h))
			total++
		}
		if err := tw.Flush(); err != nil {
			return total, err
		}
	}
	if total == 0 {
		fmt.Fprintln(w, "No hooks configured.")
		return 0, nil
	}
	fmt.Fprintf(w, "\n%d hook(s) configured. Verify with `ggcode hooks validate` and `ggcode hooks test <tool> [input]`.\n", total)
	return total, nil
}

// writeHooksValidate runs the shared hook validator and prints one line per
// problem (or an OK line). Returns the number of problems found.
func writeHooksValidate(w io.Writer, cfg hooks.HookConfig) (int, error) {
	total := hooksCount(cfg)
	errs := hooks.ValidateHooks(cfg)
	if len(errs) == 0 {
		fmt.Fprintf(w, "OK: %d hook(s) validated, no problems found.\n", total)
		return 0, nil
	}
	for _, e := range errs {
		fmt.Fprintf(w, "  %s\n", e)
	}
	fmt.Fprintf(w, "%d problem(s) found across %d hook(s).\n", len(errs), total)
	return len(errs), nil
}

// writeHooksTest evaluates every hook configured for the given tool event
// against a sample tool call, reporting per-hook verdicts. It uses the same
// matcher as the runtime (hooks.TestMatch -> matchAny), so the verdicts are
// exactly what a live session would produce. Returns (matched, patternErrs, err).
func writeHooksTest(w io.Writer, cfg hooks.HookConfig, event, toolName, rawInput string) (int, int, error) {
	var list []hooks.Hook
	switch event {
	case hooks.EventPreToolUse:
		list = cfg.PreToolUse
	case hooks.EventPostToolUse:
		list = cfg.PostToolUse
	default:
		return 0, 0, fmt.Errorf("--event must be %s or %s", hooks.EventPreToolUse, hooks.EventPostToolUse)
	}

	blockNote := "matching hooks can block (exit 2 / HTTP 403)"
	if event == hooks.EventPostToolUse {
		blockNote = "post hooks cannot block; matching hooks may inject output (inject_output)"
	}
	fmt.Fprintf(w, "event=%s tool=%q input=%s\n", event, toolName, truncateForDisplay(rawInput, 80))
	fmt.Fprintf(w, "(%s)\n", blockNote)

	matched, patternErrs := 0, 0
	for i, h := range list {
		fire, err := hooks.TestMatch(h.MatchMode, h.Match, toolName, rawInput)
		verdict := "NO MATCH"
		switch {
		case err != nil:
			verdict = "ERROR"
			patternErrs++
		case fire:
			verdict = "MATCH"
			matched++
		}
		fmt.Fprintf(w, "  %d  %-8s match=%q mode=%s type=%s\n", i, verdict, h.Match, effectiveMatchMode(h), h.HasType())
		if err != nil {
			fmt.Fprintf(w, "     %v\n", err)
		}
	}

	summary := fmt.Sprintf("%d/%d hook(s) fire on this input", matched, len(list))
	if patternErrs > 0 {
		summary += fmt.Sprintf(", %d pattern error(s)", patternErrs)
	}
	fmt.Fprintf(w, "%s.\n", summary)
	if len(list) > 0 && matched == 0 && patternErrs == 0 {
		fmt.Fprintln(w, "No hook fires. If this call was supposed to be blocked, fix the pattern; if it was supposed to pass, record this case as the must-not-fire side of the rule.")
	}
	return matched, patternErrs, nil
}

// truncateForDisplay shortens s for single-line display.
func truncateForDisplay(s string, max int) string {
	if s == "" {
		return "(empty)"
	}
	if len(s) <= max {
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprintf("%q...", s[:max])
}

func loadHooksConfig(cfgFile string) (hooks.HookConfig, error) {
	cfg, err := config.LoadWithInstance(cfgFile, "")
	if err != nil {
		return hooks.HookConfig{}, fmt.Errorf("load config: %w", err)
	}
	return cfg.Hooks, nil
}

func newHooksCmd(cfgFile *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "Inspect and verify configured lifecycle hooks",
		Long: `List, validate, and test configured hooks without starting an agent session.

Hooks are deterministic policy and automation rules; verify them like code:
test both that a rule fires when it must fire, and that it does NOT fire when
it must not. Rules that are only ever checked from one side silently drift
towards matching everything.

Examples:
  ggcode hooks list
  ggcode hooks validate
  ggcode hooks test run_command '{"command":"git push --force"}'
  ggcode hooks test run_command 'git status' --event post_tool_use
  ggcode hooks test run_command 'rm -rf /' --expect-match=true`,
	}
	cmd.AddCommand(newHooksListCmd(cfgFile))
	cmd.AddCommand(newHooksValidateCmd(cfgFile))
	cmd.AddCommand(newHooksTestCmd(cfgFile))
	return cmd
}

func newHooksListCmd(cfgFile *string) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all configured hooks by event",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadHooksConfig(*cfgFile)
			if err != nil {
				return err
			}
			_, err = writeHooksList(os.Stdout, cfg)
			return err
		},
	}
}

func newHooksValidateCmd(cfgFile *string) *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate hook configuration and report problems",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadHooksConfig(*cfgFile)
			if err != nil {
				return err
			}
			n, err := writeHooksValidate(os.Stdout, cfg)
			if err != nil {
				return err
			}
			if n > 0 {
				return fmt.Errorf("hook configuration has %d problem(s)", n)
			}
			return nil
		},
	}
}

func newHooksTestCmd(cfgFile *string) *cobra.Command {
	var event string
	var expectMatch string

	cmd := &cobra.Command{
		Use:   "test <tool> [rawInput]",
		Short: "Evaluate hook match patterns against a sample tool call",
		Long: `Evaluate hook match patterns against a sample tool call without executing anything.

For each hook configured on the selected event, prints MATCH / NO MATCH / ERROR
using the same matcher the agent runtime uses. Use --expect-match to turn the
probe into an assertion for scripted two-sided testing:

  --expect-match=true   exit 1 if no hook fires
  --expect-match=false  exit 1 if any hook fires

rawInput is the raw tool argument text as seen by hooks (GGCODE_RAW_INPUT,
typically the JSON-encoded arguments, e.g. '{"command":"git push --force"}').`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadHooksConfig(*cfgFile)
			if err != nil {
				return err
			}
			toolName := args[0]
			rawInput := ""
			if len(args) > 1 {
				rawInput = args[1]
			}
			matched, _, err := writeHooksTest(os.Stdout, cfg, event, toolName, rawInput)
			if err != nil {
				return err
			}
			switch expectMatch {
			case "":
			case "true":
				if matched == 0 {
					return fmt.Errorf("expected at least one hook to fire, but 0 fired")
				}
			case "false":
				if matched > 0 {
					return fmt.Errorf("expected no hook to fire, but %d fired", matched)
				}
			default:
				return fmt.Errorf("--expect-match must be true or false")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&event, "event", hooks.EventPreToolUse,
		fmt.Sprintf("tool event to test: %s or %s", hooks.EventPreToolUse, hooks.EventPostToolUse))
	cmd.Flags().StringVar(&expectMatch, "expect-match", "",
		"assert the outcome: true = at least one hook must fire, false = none may fire")
	return cmd
}
