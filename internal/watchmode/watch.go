package watchmode

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Options configures the watch loop.
type Options struct {
	// Root is the project directory to watch (usually the cwd).
	Root string
	// Interval is the poll period; values below 250ms are clamped.
	Interval time.Duration
	// Timeout is passed to the Runner; zero means DefaultRunTimeout.
	Timeout time.Duration
	// Bypass, when true, is forwarded to spawned pipe-mode runs so they can
	// act without interactive permission prompts ("act" markers only).
	Bypass bool
	// ConfigPath, when non-empty, is forwarded to spawned runs as --config.
	ConfigPath string
	// Stdout/Stderr receive status output.
	Stdout, Stderr io.Writer
	// Exec overrides process spawning (tests).
	Exec ExecFunc
}

// maxQueue bounds pending markers; when full the oldest is dropped.
const maxQueue = 16

// Run drives the watch loop until ctx is cancelled (Ctrl+C). It never
// returns an error for context cancellation.
func Run(ctx context.Context, opts Options) error {
	interval := opts.Interval
	if interval < 250*time.Millisecond {
		interval = 250 * time.Millisecond
	}
	stdout := opts.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = io.Discard
	}

	scanner := NewScanner(opts.Root)
	scanner.PrimeBaseline(ctx)
	fmt.Fprintf(stdout, "ggcode watch: polling %s every %s\n", opts.Root, interval)
	fmt.Fprintf(stdout, "  add an annotation in any file to trigger a run:\n")
	fmt.Fprintf(stdout, "    %s! <what to do>   — act (implement, edit files)\n", MarkerToken)
	fmt.Fprintf(stdout, "    %s? <question>     — ask (explain only, no edits)\n", MarkerToken)
	fmt.Fprintf(stdout, "  Ctrl+C to stop.\n")

	runner := &Runner{Root: opts.Root, Timeout: opts.Timeout, Exec: opts.Exec}
	runner.ExtraArgs = func(m Marker) []string {
		var extra []string
		if opts.ConfigPath != "" {
			extra = append(extra, "--config", opts.ConfigPath)
		}
		if opts.Bypass && m.Mode == ModeAct {
			extra = append(extra, "--bypass")
		}
		return extra
	}

	var queue []Marker
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(stdout, "\nggcode watch: stopped.")
			return nil
		case <-ticker.C:
		}
		markers, err := scanner.Scan(ctx)
		if err != nil {
			if ctx.Err() != nil {
				fmt.Fprintln(stdout, "\nggcode watch: stopped.")
				return nil
			}
			fmt.Fprintf(stderr, "ggcode watch: scan error: %v\n", err)
			continue
		}
		for _, m := range markers {
			if len(queue) >= maxQueue {
				dropped := queue[0]
				queue = queue[1:]
				fmt.Fprintf(stderr, "ggcode watch: queue full, dropped oldest: %s\n", dropped)
			}
			queue = append(queue, m)
		}
		for len(queue) > 0 {
			m := queue[0]
			queue = queue[1:]
			dispatchOne(ctx, runner, m, opts, stdout, stderr)
			if ctx.Err() != nil {
				fmt.Fprintln(stdout, "\nggcode watch: stopped.")
				return nil
			}
		}
	}
}

func dispatchOne(ctx context.Context, runner *Runner, m Marker, opts Options, stdout, stderr io.Writer) {
	fmt.Fprintf(stdout, "ggcode watch: %s\n", m)
	lines, err := readFileLines(opts.Root, m.Path)
	if err != nil {
		lines = nil // proceed without a snippet
	}
	if err := runner.Dispatch(ctx, m, lines); err != nil {
		if ctx.Err() != nil {
			return
		}
		fmt.Fprintf(stderr, "ggcode watch: run failed: %v\n", err)
	} else {
		fmt.Fprintf(stdout, "ggcode watch: done: %s:%d\n", m.Path, m.Line)
	}
}

// readFileLines loads a snippet source for the prompt, resolving the path
// relative to root.
func readFileLines(root, path string) ([]string, error) {
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(root, path)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	return strings.Split(string(data), "\n"), nil
}
