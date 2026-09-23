package tui

import (
	"fmt"

	runewidth "github.com/mattn/go-runewidth"
	"github.com/topcheer/ggcode/internal/util"
)

// describeJobsFamilyTool renders background command job tools
// (start/write/read/wait/stop/list).
func describeJobsFamilyTool(lang Language, toolName string, args map[string]any, fileTarget string) toolPresentation {
	switch toolName {
	case "write_command_input":
		// The input text being sent to the process is the most important detail
		inputText := argString(args, "input")
		jobID := argString(args, "job_id")
		if inputText != "" {
			// Truncate long input for display
			if runewidth.StringWidth(inputText) > 60 {
				runes := []rune(inputText)
				w := 0
				cut := len(runes)
				for i, r := range runes {
					rw := runewidth.RuneWidth(r)
					if w+rw > 57 {
						cut = i
						break
					}
					w += rw
				}
				inputText = string(runes[:cut]) + "…"
			}
			detail := fmt.Sprintf("→ %s", inputText)
			if jobID != "" {
				shortID := shortenJobID(jobID)
				detail = fmt.Sprintf("[%s] → %s", shortID, inputText)
			}
			return toolPresentationFor(lang, "input", detail)
		}
		return toolPresentationFor(lang, "input", displayToolTarget(util.FirstNonEmpty(
			jobID,
			"background command",
		)))
	case "read_command_output":
		jobID := argString(args, "job_id")
		return toolPresentationFor(lang, "output", displayToolTarget(shortenJobID(jobID)))
	case "wait_command":
		jobID := argString(args, "job_id")
		waitSec := argString(args, "wait_seconds")
		detail := shortenJobID(jobID)
		if waitSec != "" {
			detail = fmt.Sprintf("%s (%ss)", detail, waitSec)
		}
		return toolPresentationFor(lang, "wait", displayToolTarget(detail))
	case "stop_command":
		jobID := argString(args, "job_id")
		return toolPresentationFor(lang, "stop", displayToolTarget(shortenJobID(jobID)))
	case "list_commands":
		return toolPresentationFor(lang, "list_jobs", "")
	default:
		return describeUnknownFamilyTool(lang, toolName, args, fileTarget)
	}
}
