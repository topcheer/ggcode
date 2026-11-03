package tui

import "os"

type stdoutGuardWriter struct {
	*os.File
}

func (w stdoutGuardWriter) Write(p []byte) (int, error) {
	if stdoutDead() {

		return len(p), nil
	}
	return w.File.Write(p)
}
