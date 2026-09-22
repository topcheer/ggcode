package watchmode

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// DefaultRunTimeout bounds a single dispatched run.
const DefaultRunTimeout = 10 * time.Minute

// ExecFunc spawns the agent process for one marker. It is a seam for tests.
// name is the executable, args the full argument list, dir the working
// directory; combined output should be written to out.
type ExecFunc func(ctx context.Context, name string, args []string, dir string, out io.Writer) error

// Runner dispatches one marker at a time by spawning ggcode pipe mode
// (`ggcode -p <prompt>`), which runs the agent non-interactively.
type Runner struct {
	// Root is the working directory for spawned runs.
	Root string
	// Timeout bounds each run; zero means DefaultRunTimeout.
	Timeout time.Duration
	// Exec overrides process spawning (tests). nil uses os/exec.
	Exec ExecFunc
	// ExtraArgs, when set, appends caller-supplied arguments after the
	// prompt (e.g. --bypass for act-mode markers).
	ExtraArgs func(m Marker) []string
	// OnStart / OnDone observe the run lifecycle.
	OnStart func(m Marker)
	OnDone  func(m Marker, err error)
}

// Dispatch runs one marker synchronously and returns the run error, if any.
func (r *Runner) Dispatch(ctx context.Context, m Marker, fileLines []string) error {
	prompt := BuildPrompt(m, fileLines)
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultRunTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating executable: %w", err)
	}
	args := []string{"-p", prompt}
	if r.ExtraArgs != nil {
		args = append(args, r.ExtraArgs(m)...)
	}

	if r.OnStart != nil {
		r.OnStart(m)
	}
	execFn := r.Exec
	if execFn == nil {
		execFn = defaultExec
	}
	err = execFn(runCtx, exe, args, r.Root, newPrefixWriter("  | "))
	if r.OnDone != nil {
		r.OnDone(m, err)
	}
	return err
}

func defaultExec(ctx context.Context, name string, args []string, dir string, out io.Writer) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}

// prefixWriter indents every line of the child process output so it stays
// visually nested under the watch-mode banner.
type prefixWriter struct {
	prefix string
	w      io.Writer
}

func newPrefixWriter(prefix string) io.Writer { return &prefixWriter{prefix: prefix, w: io.Discard} }

func (p *prefixWriter) Write(b []byte) (int, error) {
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		fmt.Fprintln(p.w, p.prefix+sc.Text())
	}
	return len(b), nil
}
