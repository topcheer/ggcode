package main

import (
	"context"
	"errors"
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

	token, err := pollOpenCodeToken(ctx, consoleURL, dev)
	if err != nil {
		return err
	}
	return storeOpenCodeToken(token)
}

// pollOpenCodeToken polls the device token endpoint at the server-specified
// interval until the user authorizes, denies, or the code expires.
func pollOpenCodeToken(ctx context.Context, consoleURL string, dev *auth.OpenCodeDeviceAuth) (*auth.OpenCodeToken, error) {
	interval := time.Duration(dev.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(dev.ExpiresIn) * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(interval)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		token, err := auth.ExchangeOpenCodeDeviceToken(ctx, consoleURL, dev.DeviceCode)
		if err == nil {
			return token, nil
		}
		if !errors.Is(err, auth.ErrOpenCodePending) && !errors.Is(err, auth.ErrOpenCodeSlowDown) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("opencode login: device code expired before authorization")
}

// storeOpenCodeToken persists the token pair via the same provider auth
// store copilot/anthropic OAuth use (provider_auth.json), so refresh and
// panel flows treat opencode like any OAuth vendor.
func storeOpenCodeToken(token *auth.OpenCodeToken) error {
	info := &auth.Info{
		ProviderID:   "opencode",
		Type:         "oauth",
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(token.ExpiresIn) * time.Second),
	}
	if err := auth.DefaultStore().Save(info); err != nil {
		return fmt.Errorf("storing opencode credential: %w", err)
	}
	fmt.Fprintf(os.Stdout, "Logged in to OpenCode. Credential stored for vendor opencode.\n")
	if token.RefreshToken != "" {
		fmt.Fprintf(os.Stdout, "Session expires in %s.\n", time.Duration(token.ExpiresIn)*time.Second)
	}
	return nil
}
