package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/platform"
)

// platformDir returns ~/.ggcode/platform.
func platformDir() string {
	return filepath.Join(config.ConfigDir(), "platform")
}

// platformPath joins name under the platform dir.
func platformPath(name string) string {
	return filepath.Join(platformDir(), name)
}

// platformConfig is the server-side knobs persisted in config.json.
type platformConfig struct {
	Listen string `json:"listen,omitempty"`
	Bypass bool   `json:"bypass,omitempty"`
}

func loadPlatformConfig(dir string) platformConfig {
	var cfg platformConfig
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	return cfg
}

func newServeCmd(cfgFile *string) *cobra.Command {
	listen := ""
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the platform job server (multi-user headless agent API)",
		Long: `Start ggcode's platform server: a JWT-authenticated HTTP API that runs
headless agent jobs inside whitelisted workspace directories on this machine.

Endpoints:
  POST   /api/v1/auth/login     {username,password} -> {token}
  POST   /api/v1/jobs           {workspace,prompt}  -> job (queued)
  GET    /api/v1/jobs           list own jobs (admins: all)
  GET    /api/v1/jobs/{id}      full job detail incl. output
  DELETE /api/v1/jobs/{id}      cancel a queued/running job
  GET    /api/v1/healthz        liveness (no auth)

Manage users and workspaces with: ggcode platform user add / platform workspace add`,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := platformDir()
			users, err := platform.LoadUserStore(filepath.Join(dir, "users.json"))
			if err != nil {
				return err
			}
			ws, err := platform.LoadWorkspaceSet(filepath.Join(dir, "workspaces.json"))
			if err != nil {
				return err
			}
			if len(ws.List()) == 0 {
				return fmt.Errorf("no workspaces registered; run 'ggcode platform workspace add <dir>' first")
			}
			jobs, err := platform.NewJobStore(filepath.Join(dir, "jobs"))
			if err != nil {
				return err
			}
			secret, err := platform.LoadOrCreateSecret(dir)
			if err != nil {
				return err
			}
			fileCfg := loadPlatformConfig(dir)
			if listen == "" {
				if fileCfg.Listen != "" {
					listen = fileCfg.Listen
				} else {
					listen = "127.0.0.1:8420"
				}
			}
			selfExe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("resolving ggcode binary: %w", err)
			}
			exec := platform.NewExecutor(jobs, platform.DefaultRunFunc(selfExe, *cfgFile, fileCfg.Bypass))
			srv := platform.NewServer(users, jobs, ws, exec, secret)

			// Graceful shutdown on SIGINT/SIGTERM.
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			go func() {
				<-ctx.Done()
				debug.Log("platform", "serve: shutting down")
				shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = srv.Shutdown(shutCtx)
			}()
			fmt.Fprintf(cmd.OutOrStdout(), "ggcode platform listening on %s (%d workspace(s))\n", listen, len(ws.List()))
			return srv.ListenAndServe(listen)
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "", "listen address (default config.json listen or 127.0.0.1:8420)")
	return cmd
}
