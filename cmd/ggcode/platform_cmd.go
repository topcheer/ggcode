package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/topcheer/ggcode/internal/platform"
	"golang.org/x/term"
)

func newPlatformCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "platform",
		Short: "Manage the platform server registry (users, workspaces)",
	}
	cmd.AddCommand(newPlatformUserCmd(), newPlatformWorkspaceCmd())
	return cmd
}

// --- users ---

func newPlatformUserCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "user", Short: "Manage platform users"}
	var admin, passwordStdin bool
	add := &cobra.Command{
		Use:   "add <name>",
		Short: "Register a user (password via --password-stdin or interactive prompt)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			password, err := readPassword(passwordStdin)
			if err != nil {
				return err
			}
			store, err := platform.LoadUserStore(userStorePath())
			if err != nil {
				return err
			}
			if err := store.Add(args[0], password, admin); err != nil {
				return err
			}
			if err := store.Save(); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "user %q added\n", args[0])
			return nil
		},
	}
	add.Flags().BoolVar(&admin, "admin", false, "grant admin (sees and cancels all jobs)")
	add.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read password from stdin instead of prompting")

	list := &cobra.Command{
		Use:   "list",
		Short: "List registered users",
		RunE: func(c *cobra.Command, _ []string) error {
			store, err := platform.LoadUserStore(userStorePath())
			if err != nil {
				return err
			}
			for _, u := range store.Users {
				role := ""
				if u.Admin {
					role = " admin"
				}
				fmt.Fprintf(c.OutOrStdout(), "%s%s\n", u.Name, role)
			}
			return nil
		},
	}

	remove := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a user",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			store, err := platform.LoadUserStore(userStorePath())
			if err != nil {
				return err
			}
			if err := store.Remove(args[0]); err != nil {
				return err
			}
			if err := store.Save(); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "user %q removed\n", args[0])
			return nil
		},
	}

	cmd.AddCommand(add, list, remove)
	return cmd
}

// --- workspaces ---

func newPlatformWorkspaceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "workspace", Short: "Manage whitelisted workspace roots"}
	add := &cobra.Command{
		Use:   "add <dir>",
		Short: "Whitelist an absolute directory for jobs",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			ws, err := platform.LoadWorkspaceSet(workspaceSetPath())
			if err != nil {
				return err
			}
			if err := ws.Add(args[0]); err != nil {
				return err
			}
			if err := ws.Save(); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "workspace %q registered\n", args[0])
			return nil
		},
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "List whitelisted workspace roots",
		RunE: func(c *cobra.Command, _ []string) error {
			ws, err := platform.LoadWorkspaceSet(workspaceSetPath())
			if err != nil {
				return err
			}
			for _, r := range ws.List() {
				fmt.Fprintln(c.OutOrStdout(), r)
			}
			return nil
		},
	}
	remove := &cobra.Command{
		Use:   "remove <dir>",
		Short: "Remove a whitelisted workspace root",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			ws, err := platform.LoadWorkspaceSet(workspaceSetPath())
			if err != nil {
				return err
			}
			if err := ws.Remove(args[0]); err != nil {
				return err
			}
			if err := ws.Save(); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "workspace %q removed\n", args[0])
			return nil
		},
	}
	cmd.AddCommand(add, list, remove)
	return cmd
}

func userStorePath() string    { return platformPath("users.json") }
func workspaceSetPath() string { return platformPath("workspaces.json") }

// readPassword reads the password from stdin (--password-stdin) or via an
// interactive no-echo prompt.
func readPassword(fromStdin bool) (string, error) {
	if fromStdin {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("reading password from stdin: %w", err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprint(os.Stderr, "password: ")
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("reading password: %w", err)
	}
	return string(b), nil
}
