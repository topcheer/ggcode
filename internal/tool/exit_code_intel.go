package tool

// Exit Code Intelligence -- Process Termination Diagnosis
//
// Research basis: Terminal output parsing/structuring is identified as a
// blue-ocean gap. While cmd_output_parser.go handles test/build result
// extraction and diagnoseCommandFailure handles output-text patterns,
// neither interprets process exit codes. When a command is killed by a
// signal (OOM, segfault, abort), the agent sees only "Command failed:
// signal: killed" with no actionable explanation, wasting iterations on
// trial-and-error.
//
// Competitor analysis:
//   - Claude Code: shows raw exit status, no interpretation
//   - Cursor: shows exit code in UI, no actionable diagnosis
//   - Cline/OpenHands: raw error message, no exit code analysis
//   - Aider: raw error string only
//
// Gap: No deterministic exit code to human-readable interpretation mapping.
// Exit codes 128+N (signal terminations) carry critical diagnostic info:
//   137 (SIGKILL) often means OOM killer -- agent should reduce parallelism
//   139 (SIGSEGV) means segfault -- agent should look for memory safety bugs
//   130 (SIGINT)  means user interrupted -- not a real failure
//   134 (SIGABRT) means abort/assertion -- agent should check assertions
// Without this, the agent treats all signal kills identically.
//
// Design:
//   - Zero LLM cost (deterministic lookup table)
//   - Produces a concise "[Exit Code]" diagnostic appended to error messages
//   - Only fires for non-obvious codes (126+ and signal-based terminations)
//   - Includes actionable hints for the most impactful codes

import (
	"fmt"
	"strings"
)

// exitCodeInfo describes a non-standard exit code with an explanation and
// optional actionable hint for the agent.
type exitCodeInfo struct {
	description string
	hint        string // empty if no specific hint
}

// signalExitCodes maps exit codes to their meanings.
// Only includes codes where the explanation adds value beyond "command failed".
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

// interpretExitCode returns a human-readable diagnostic string for non-trivial
// exit codes. Returns "" for codes that don't need interpretation (0 = success,
// 1 = generic error, or any code not in the intel map).
func interpretExitCode(exitCode int) string {
	if exitCode <= 1 {
		return ""
	}

	info, ok := exitCodeIntelMap[exitCode]
	if !ok {
		// Generic 128+N signal code not in the specific map
		if exitCode > 128 && exitCode < 160 {
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
