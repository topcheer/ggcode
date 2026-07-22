package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"
)

func newReportCmd() *cobra.Command {
	var (
		output      string
		noOpen      bool
		sessionsDir string
	)
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Generate a session analytics HTML report",
		Long: `Scans all session JSONL files and generates a self-contained HTML report
with token usage trends, per-session details, TTFT analysis, and tool statistics.

Charts are fully offline (Chart.js embedded in the binary).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := sessionsDir
			if dir == "" {
				var err error
				dir, err = findSessionsDir()
				if err != nil {
					return fmt.Errorf("cannot find sessions directory: %w\nIf your sessions are elsewhere, use --sessions-dir flag", err)
				}
			}

			fmt.Fprintf(os.Stderr, "ggcode report: scanning %s ...\n", dir)
			results, err := scanAllSessions(dir)
			if err != nil {
				return fmt.Errorf("scan failed: %w", err)
			}
			if len(results) == 0 {
				return fmt.Errorf("no sessions found in %s", dir)
			}

			report := buildReport(results)
			html, err := generateHTML(report)
			if err != nil {
				return fmt.Errorf("generate HTML: %w", err)
			}

			outPath := output
			if err := os.WriteFile(outPath, []byte(html), 0644); err != nil {
				return fmt.Errorf("write %s: %w", outPath, err)
			}

			sizeStr := "bytes"
			size := int64(len(html))
			if size >= 1_000_000_000 {
				sizeStr = fmt.Sprintf("%.1f GB", float64(size)/1_000_000_000)
			} else if size >= 1_000_000 {
				sizeStr = fmt.Sprintf("%.1f MB", float64(size)/1_000_000)
			} else if size >= 1_000 {
				sizeStr = fmt.Sprintf("%.1f KB", float64(size)/1_000)
			}
			fmt.Fprintf(os.Stderr, "ggcode report: wrote %s (%s, %d sessions)\n", outPath, sizeStr, len(report.Sessions))

			if !noOpen {
				openBrowserOut(outPath)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "ggreport.html", "output HTML file path")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "do not auto-open the report in browser")
	cmd.Flags().StringVar(&sessionsDir, "sessions-dir", "", "override sessions directory (default: ~/.ggcode/sessions)")
	return cmd
}

func openBrowserOut(path string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return
	}
	url := "file://" + abs
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	case "linux":
		c = exec.Command("xdg-open", url)
	case "windows":
		// The empty string is the window title for `start`.
		// Without it, `start` treats the first quoted argument (the URL)
		// as the title when the path contains spaces.
		c = exec.Command("cmd", "/c", "start", "", url)
	default:
		return
	}
	_ = c.Start()
}
