package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/topcheer/ggcode/internal/commands"
)

// ggcode skill lock pins the sha256 of every loaded skill/command file into
// .ggcode/skills-lock.json (#skill-provenance-lock). The loader then refuses
// any locked file whose content changed without re-pinning, and (with
// GGCODE_SKILL_LOCK_STRICT=1) refuses untracked project-source skills.
func newSkillCmd() *cobra.Command {
	var projectDir string
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Manage skill supply-chain integrity",
	}
	lockCmd := &cobra.Command{
		Use:   "lock",
		Short: "Pin sha256 of all loaded skills into skills-lock.json",
		Long: "Pin the sha256 of every loaded skill/command markdown file into\n" +
			".ggcode/skills-lock.json. On subsequent loads the loader refuses any\n" +
			"locked file whose hash changed (repo-cloned malicious skill\n" +
			"injection defense). With GGCODE_SKILL_LOCK_STRICT=1 untracked\n" +
			"project-source skills are also refused until pinned.",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := strings.TrimSpace(projectDir)
			if dir == "" {
				var err error
				dir, err = os.Getwd()
				if err != nil {
					return err
				}
			}
			loader := commands.NewLoader(dir)
			entries := loader.BuildLockEntries()
			lockPath := commands.LockPathFor(dir)
			lock := &commands.SkillLockFile{Version: commands.SkillLockVersion, Skills: entries}
			if err := commands.WriteSkillLock(lockPath, lock); err != nil {
				return fmt.Errorf("writing lock: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "pinned %d skill files -> %s\n", len(entries), lockPath)
			for _, e := range entries {
				fmt.Fprintf(cmd.OutOrStdout(), "  %.8s  %s\n", e.SHA256, e.Path)
			}
			return nil
		},
	}
	lockCmd.Flags().StringVar(&projectDir, "project", "", "project directory (default: cwd)")
	cmd.AddCommand(lockCmd)
	return cmd
}
