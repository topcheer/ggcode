package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/topcheer/ggcode/internal/session"
)

// newDaemonSessionCmd implements `ggcode daemon session export|import`:
// portable session bundles for resuming a conversation on another machine
// (session handoff). Export writes a self-contained, gzip-compressed bundle;
// import installs it into the local store, remapping the workspace to the
// current directory so the session is immediately resumable.
func newDaemonSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Export or import session bundles for transfer between devices",
		Long: "Export a session to a portable bundle file or import one into the local store.\n" +
			"Bundles carry the full message history and session metadata, enabling a\n" +
			"conversation to be resumed on a different machine.",
	}

	var exportOut string
	exportCmd := &cobra.Command{
		Use:   "export <session-id>",
		Short: "Export a session to a portable bundle file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := session.NewDefaultStore()
			if err != nil {
				return err
			}
			out, err := store.ExportBundle(args[0], exportOut)
			if err != nil {
				if errors.Is(err, session.ErrSessionNotFound) {
					return fmt.Errorf("session %q not found in local store", args[0])
				}
				return err
			}
			fmt.Printf("exported session %s -> %s\n", args[0], out)
			return nil
		},
	}
	exportCmd.Flags().StringVarP(&exportOut, "out", "o", "", "output bundle path (default: <session-id>.ggsession)")

	var importWorkspace string
	var importKeepWorkspace bool
	importCmd := &cobra.Command{
		Use:   "import <bundle-file>",
		Short: "Import a session bundle into the local store",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := session.NewDefaultStore()
			if err != nil {
				return err
			}
			if _, err := os.Stat(args[0]); err != nil {
				return fmt.Errorf("reading bundle: %w", err)
			}
			ses, err := store.ImportBundleFile(args[0], session.ImportOptions{
				Workspace:     importWorkspace,
				KeepWorkspace: importKeepWorkspace,
			})
			if err != nil {
				return err
			}
			fmt.Printf("imported session %s (title: %s, workspace: %s)\n", ses.ID, ses.Title, ses.Workspace)
			fmt.Println("resume it with: ggcode --resume " + ses.ID)
			return nil
		},
	}
	importCmd.Flags().StringVar(&importWorkspace, "workspace", "", "target workspace directory (default: current directory)")
	importCmd.Flags().BoolVar(&importKeepWorkspace, "keep-workspace", false, "keep the bundle's original workspace path")

	cmd.AddCommand(exportCmd)
	cmd.AddCommand(importCmd)
	return cmd
}
