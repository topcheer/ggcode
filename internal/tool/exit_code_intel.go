package tool

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
)

type exitCodeInfo struct {
	description string
	hint        string
}

var exitCodeIntelMap = map[int]exitCodeInfo{
	126: {
		description: "command found but not executable (permission denied)",
		hint:        "Check file permissions (chmod +x) or whether the file is a valid executable",
	},
	127: {
		description: "command not found",
		hint:        "Verify the command is installed and available in PATH",
	},
	130: {
		description: "interrupted by user (SIGINT / Ctrl+C)",
		hint:        "This was a manual interruption, not a real failure -- the command itself may be fine",
	},
	134: {
		description: "aborted (SIGABRT) -- typically an assertion failure or abort() call",
		hint:        "Look for assertion failures, panic-with-abort, or explicit abort() calls in the code",
	},
	137: {
		description: "killed (SIGKILL) -- frequently the OOM (out of memory) killer",
		hint:        "Likely out of memory. Try reducing parallelism (-p 1 -parallel 1), memory limits (GOMEMLIMIT), or splitting work into smaller batches",
	},
	139: {
		description: "segmentation fault (SIGSEGV) -- memory access violation",
		hint:        "Indicates a memory safety bug (nil pointer dereference, buffer overflow, use-after-free). Check for CGO, unsafe pointers, or C bindings",
	},
	143: {
		description: "terminated (SIGTERM) -- typically sent by process manager or timeout",
		hint:        "Process was terminated externally (timeout, container stop, or manual kill). Consider increasing timeout or checking if the process was stuck",
	},
	136: {
		description: "floating point exception (SIGFPE) -- arithmetic error",
		hint:        "Check for division by zero or integer overflow in the code",
	},
}

func exitCodeFromErr(err error) int {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return -1
	}
	if code := ee.ExitCode(); code >= 0 {
		return code
	}
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return -1
}

func interpretExitCode(exitCode int) string {
	if exitCode <= 1 {
		return ""
	}

	info, ok := exitCodeIntelMap[exitCode]

	if runtime.GOOS == "windows" && exitCode > 127 {
		return ""
	}
	if !ok {

		if runtime.GOOS != "windows" && exitCode > 128 && exitCode < 160 {
			signal := exitCode - 128
			return fmt.Sprintf("[Exit Code] %d (terminated by signal %d)", exitCode, signal)
		}
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[Exit Code] %d: %s", exitCode, info.description))
	if info.hint != "" {
		sb.WriteString("\n  -> ")
		sb.WriteString(info.hint)
	}
	return sb.String()
}
