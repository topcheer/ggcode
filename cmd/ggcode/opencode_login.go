package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/topcheer/ggcode/internal/auth"
)

// newLoginCmd builds `ggcode login <vendor>` for interactive vendor OAuth
// flows. Currently supports: opencode (device flow against the OpenCode
// console, impersonating the official opencode CLI).
func newLoginCmd(cfgFile *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login <vendor>",
		Short: "Log in to a vendor via OAuth and store the credential",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			vendor := strings.ToLower(strings.TrimSpace(args[0]))
			switch vendor {
			case "opencode":
				return runOpenCodeLogin(cmd.Context())
			default:
				return fmt.Errorf("unsupported vendor %q (supported: opencode)", vendor)
			}
		},
	}
	return cmd
}

// runOpenCodeLogin drives the OpenCode console device flow: print the
// verification URL + user code, poll until authorized, then persist the
// access token as the opencode vendor API key (it is a valid Zen bearer
// credential, mirroring how the CLI's OPENCODE_CONSOLE_TOKEN is used).
func runOpenCodeLogin(ctx context.Context) error {
	w := os.Stdout
	consoleURL := os.Getenv("OPENCODE_CONSOLE_URL")
	if consoleURL == "" {
		consoleURL = auth.OpenCodeConsoleURL
	}

	dev, err := auth.StartOpenCodeDeviceFlow(ctx, consoleURL)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "OpenCode login\n")
	fmt.Fprintf(w, "  Open in browser: %s\n", dev.VerificationURL(consoleURL))
	fmt.Fprintf(w, "  Enter code:      %s\n", dev.UserCode)
	fmt.Fprintf(w, "  Waiting for authorization (code expires in %s)...\n", time.Duration(dev.ExpiresIn)*time.Second)

	info, err := pollOpenCodeToken(ctx, consoleURL, dev)
	if err != nil {
		return err
	}
	return storeOpenCodeToken(info)
}

// pollOpenCodeToken delegates to the shared auth helper (also used by the
// TUI provider panel) so polling semantics stay in one place.
func pollOpenCodeToken(ctx context.Context, consoleURL string, dev *auth.OpenCodeDeviceAuth) (*auth.Info, error) {
	return auth.PollOpenCodeDeviceFlow(ctx, consoleURL, dev)
}

// storeOpenCodeToken persists the polled Info via the provider auth store,
// the same sink the TUI panel uses.
func storeOpenCodeToken(info *auth.Info) error {
	if err := auth.DefaultStore().Save(info); err != nil {
		return fmt.Errorf("storing opencode credential: %w", err)
	}
	fmt.Fprintf(os.Stdout, "Logged in to OpenCode. Credential stored for vendor opencode.\n")
	return nil
}
