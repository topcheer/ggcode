//go:build windows

package update

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"
)

var (
	modShell32       = syscall.NewLazyDLL("shell32.dll")
	procShellExecute = modShell32.NewProc("ShellExecuteW")
)

const swShownormal = 1

// launchElevated re-runs the helper command with UAC elevation (runas verb).
// The original command's stdin/stdout/stderr cannot be inherited across the
// UAC boundary, so the elevated helper runs in a new console window.
func launchElevated(cmd *exec.Cmd) error {
	if len(cmd.Args) < 1 {
		return fmt.Errorf("elevate: empty command")
	}
	exe := cmd.Args[0]
	args := quoteArgs(cmd.Args[1:])

	// ShellExecuteW with "runas" verb triggers the UAC prompt.
	ret, _, err := procShellExecute.Call(
		0, // hwnd
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("runas"))),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(exe))),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(args))),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(cmd.Dir))),
		swShownormal,
	)
	if ret <= 32 {
		return fmt.Errorf("elevation request failed (code %d): %w\n"+
			"If you denied the UAC prompt, try running the update from an "+
			"elevated terminal (Run as Administrator).", ret, err)
	}
	return nil
}

// quoteArgs joins args into a command-line string suitable for ShellExecute.
// Arguments containing spaces or special characters are quoted, and embedded
// double quotes are escaped with backslash per the CommandLineToArgvW convention.
func quoteArgs(args []string) string {
	var b []byte
	for i, a := range args {
		if i > 0 {
			b = append(b, ' ')
		}
		if needsQuote(a) {
			b = append(b, '"')
			// Escape backslashes that precede a quote, and the quote itself.
			for _, c := range a {
				switch c {
				case '"':
					b = append(b, '\\', '"')
				case '\\':
					b = append(b, '\\', '\\')
				default:
					b = append(b, byte(c))
				}
			}
			b = append(b, '"')
		} else {
			b = append(b, a...)
		}
	}
	return string(b)
}

func needsQuote(s string) bool {
	for _, c := range s {
		if c == ' ' || c == '\t' || c == '"' {
			return true
		}
	}
	return false
}
