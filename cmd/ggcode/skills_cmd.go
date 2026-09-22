package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/topcheer/ggcode/internal/commands"
)

// newSkillsCmd creates the "ggcode skills" subcommand for validating skills
// against the Agent Skills open standard (agentskills.io, 2025-12).
//
// Usage:
//
//	ggcode skills validate            - validate all skills visible to the
//	                                    current project (user + project scopes)
//	ggcode skills validate <path>     - validate one skill folder, a
//	                                    SKILL.md file, or a directory of
//	                                    skill folders
//	ggcode skills validate --json     - machine-readable report
//
// Exit code is 1 when any error-severity issue is found, so CI and skill
// release scripts can gate on spec compliance.
func newSkillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Validate Agent Skills against the open standard",
		Long: `Validate skills against the Agent Skills open standard (agentskills.io, December 2025).

The standard defines six portable frontmatter fields (name, description,
license, compatibility, metadata, allowed-tools). Strict consumers such as
claude.ai skill upload and Skills API packaging reject any other field, while
ggcode silently accepts extensions - this command makes the difference visible,
and also checks name/description limits, scope shadowing, dependency
resolution, and requires-tools PATH availability.`,
	}

	validateCmd := &cobra.Command{
		Use:   "validate [path]",
		Short: "Validate skills and report spec issues",
		Args:  cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			asJSON, _ := cmd.Flags().GetBool("json")
			var reports []*commands.SkillValidationReport
			if len(args) > 0 {
				reports = commands.ValidateSkillPath(args[0])
			} else {
				cwd, err := os.Getwd()
				if err != nil {
					fmt.Fprintf(os.Stderr, "skills validate: %v\n", err)
					os.Exit(1)
				}
				reports = commands.ValidateSkillScopes(cwd)
			}

			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				errs, warns := commands.CountSevere(reports)
				if err := enc.Encode(map[string]any{
					"skills":  reports,
					"summary": map[string]int{"errors": errs, "warnings": warns},
				}); err != nil {
					fmt.Fprintf(os.Stderr, "skills validate: %v\n", err)
					os.Exit(1)
				}
			} else {
				printSkillValidationReports(reports)
			}

			errs, _ := commands.CountSevere(reports)
			if errs > 0 {
				os.Exit(1)
			}
		},
	}
	validateCmd.Flags().Bool("json", false, "output machine-readable JSON")

	cmd.AddCommand(validateCmd)
	return cmd
}

func printSkillValidationReports(reports []*commands.SkillValidationReport) {
	if len(reports) == 0 {
		fmt.Println("No skills found in any scope.")
		return
	}
	errs, warns := 0, 0
	for _, report := range reports {
		fmt.Printf("%s  [%s]  %s\n", report.Skill, report.Source, report.Path)
		if report.OverriddenBy != "" {
			fmt.Printf("  info: shadowed by %s\n", report.OverriddenBy)
		}
		if len(report.Issues) == 0 {
			fmt.Println("  ok")
			continue
		}
		for _, issue := range report.Issues {
			switch issue.Severity {
			case commands.SkillError:
				errs++
				fmt.Printf("  error:   %s\n", formatSkillIssue(issue))
			case commands.SkillWarning:
				warns++
				fmt.Printf("  warning: %s\n", formatSkillIssue(issue))
			default:
				fmt.Printf("  info:    %s\n", formatSkillIssue(issue))
			}
		}
	}
	fmt.Printf("\n%d skill(s) checked: %d error(s), %d warning(s)\n", len(reports), errs, warns)
}

func formatSkillIssue(issue commands.SkillIssue) string {
	if issue.Field != "" {
		return fmt.Sprintf("%s: %s", issue.Field, issue.Message)
	}
	return issue.Message
}
