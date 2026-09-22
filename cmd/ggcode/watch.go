package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/topcheer/ggcode/internal/watchmode"
)

func newWatchCmd(cfgFile *string) *cobra.Command {
	var (
		interval time.Duration
		timeout  time.Duration
		bypass   bool
	)

	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Watch project files and run the agent from @ggcode annotations",
		Long: "Run ggcode in watch mode: it polls the project for files and lets you\n" +
			"trigger agent runs by leaving annotations in any file from your editor:\n\n" +
			"  // @ggcode! fix the nil check and add a test   (act: may edit files)\n" +
			"  // @ggcode? why is this O(n^2)                 (ask: explain only)\n\n" +
			"Each new annotation is dispatched as a non-interactive pipe run\n" +
			"(ggcode -p) in the current directory. Annotations that already exist\n" +
			"when watch starts are ignored; only annotations added afterwards fire,\n" +
			"and each unique instruction fires once.",
		RunE: func(cmd *cobra.Command, args []string) error {
			workDir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("resolving working directory: %w", err)
			}

			// Forward the effective config so spawned pipe runs see the same
			// configuration as this process.
			resolvedCfg := *cfgFile
			if resolvedCfg == "" {
				if r, rerr := resolveConfigFilePath(); rerr == nil {
					resolvedCfg = r
				}
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			return watchmode.Run(ctx, watchmode.Options{
				Root:       workDir,
				Interval:   interval,
				Timeout:    timeout,
				Bypass:     bypass,
				ConfigPath: resolvedCfg,
				Stdout:     os.Stdout,
				Stderr:     os.Stderr,
			})
		},
	}

	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "poll interval for file changes")
	cmd.Flags().DurationVar(&timeout, "timeout", watchmode.DefaultRunTimeout, "per-run timeout")
	cmd.Flags().BoolVar(&bypass, "bypass", false, "forward --bypass to spawned runs for act-mode markers (skip permission prompts)")
	return cmd
}
