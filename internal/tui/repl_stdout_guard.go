package tui

import "os"

// stdoutGuardWriter wraps os.Stdout for tea.WithOutput. While the stdout
// health monitor has flagged stdout dead (display sleep / SSH disconnect /
// terminal closed), writes are discarded instead of handed to the kernel:
// a full/dead pty buffer would otherwise block bubbletea's renderer
// goroutine and freeze the whole TUI (#4154). Embedding *os.File keeps
// Fd()/Stat()/Name() interface assertions working, so bubbletea's terminal
// detection and size queries are unaffected.
type stdoutGuardWriter struct {
	*os.File
}

func (w stdoutGuardWriter) Write(p []byte) (int, error) {
	if stdoutDead() {
		// Pretend success so the renderer keeps making progress; the
		// frame is dropped. Recovery (displayWakeMsg) triggers a full
		// repaint, so nothing is permanently lost.
		return len(p), nil
	}
	return w.File.Write(p)
}
