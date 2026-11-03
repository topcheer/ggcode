package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	convergenceEditThreshold = 3

	convergenceEscalationThreshold = 6

	convergenceCmdDisplayLen = 60
)

type convergenceLockState struct {
	verified bool

	verifiedCommand string

	postVerifyEdits int

	postVerifyErrors int

	warned bool

	escalated bool
}

func newConvergenceLockState() *convergenceLockState {
	return &convergenceLockState{}
}

func (s *convergenceLockState) reset() {
	s.verified = false
	s.verifiedCommand = ""
	s.postVerifyEdits = 0
	s.postVerifyErrors = 0
	s.warned = false
	s.escalated = false
}

func (s *convergenceLockState) recordVerifyResult(command string, isError bool) {
	if isError {

		if s.verified {
			debug.Log("convergence_lock", "verification failed, resetting convergence state (was verified by %q)", s.verifiedCommand)
			s.verified = false
			s.postVerifyEdits = 0
			s.postVerifyErrors = 0
			s.warned = false
			s.escalated = false
		}
		return
	}

	wasVerified := s.verified
	s.verified = true
	s.verifiedCommand = command

	s.postVerifyEdits = 0
	s.postVerifyErrors = 0
	s.warned = false
	s.escalated = false
	if !wasVerified {
		debug.Log("convergence_lock", "verification succeeded: %q — convergence lock armed", truncateCmd(command))
	}
}

func (s *convergenceLockState) recordEdit() {
	if s.verified {
		s.postVerifyEdits++
	}
}

func (s *convergenceLockState) recordEditError() {
	if s.verified {
		s.postVerifyErrors++
	}
}

func (s *convergenceLockState) check() string {
	if !s.verified {
		return ""
	}

	if s.postVerifyEdits >= convergenceEscalationThreshold && !s.escalated {
		s.escalated = true
		debug.Log("convergence_lock", "post-verification drift ESCALATION: %d edits after verify (%q), %d errors",
			s.postVerifyEdits, truncateCmd(s.verifiedCommand), s.postVerifyErrors)

		msg := fmt.Sprintf(
			"Convergence alert: %d file edits made after successful verification (\"%s\"). "+
				"Your changes were verified and working. Each additional edit risks breaking what was confirmed working. "+
				"Stop making changes unless you are fixing a specific, identified issue. "+
				"If the task is complete, provide a summary of what was done.",
			s.postVerifyEdits, truncateCmd(s.verifiedCommand),
		)
		if s.postVerifyErrors > 0 {
			msg += fmt.Sprintf(
				" Warning: %d of these post-verification edits resulted in errors — strong signal to stop and re-examine your approach.",
				s.postVerifyErrors,
			)
		}
		return msg
	}

	if s.postVerifyEdits > convergenceEditThreshold && !s.warned {
		s.warned = true
		debug.Log("convergence_lock", "post-verification drift detected: %d edits after verify (%q)",
			s.postVerifyEdits, truncateCmd(s.verifiedCommand))

		return fmt.Sprintf(
			"Convergence check: You have made %d edits since your changes verified successfully (\"%s\"). "+
				"Consider whether these additional changes are truly necessary for the task. "+
				"If the core work is complete, finalize and summarize instead of continuing to edit.",
			s.postVerifyEdits, truncateCmd(s.verifiedCommand),
		)
	}

	return ""
}

func truncateCmd(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if len(cmd) <= convergenceCmdDisplayLen {
		return cmd
	}
	return cmd[:convergenceCmdDisplayLen-3] + "..."
}

func (a *Agent) convergenceRecordVerify(toolName string, args []byte, resultErr bool) {
	if a.convergenceLock == nil {
		return
	}
	if toolName != "run_command" {
		return
	}
	cmd := extractCommandFromArgs(args)
	if cmd == "" || !isConvergenceVerifyCommand(cmd) {
		return
	}
	a.convergenceLock.recordVerifyResult(cmd, resultErr)
}

var convergenceVerifyMakeTargets = map[string]bool{
	"test":   true,
	"ci":     true,
	"build":  true,
	"lint":   true,
	"check":  true,
	"verify": true,
}

func isConvergenceVerifyCommand(cmd string) bool {
	cmdLower := strings.ToLower(strings.TrimSpace(cmd))
	if cmdLower == "" {
		return false
	}
	words := strings.Fields(cmdLower)
	if len(words) > 0 {
		switch words[0] {
		case "make", "just", "task":
			if len(words) < 2 {
				return false
			}
			target := words[1]
			for prefix := range convergenceVerifyMakeTargets {
				if target == prefix || strings.HasPrefix(target, prefix+"-") || strings.HasPrefix(target, prefix+"_") {
					return true
				}
			}
			return false
		case "gofmt", "goimports", "prettier", "eslint", "ruff", "rustfmt",
			"flake8", "black", "isort", "rubocop", "stylelint", "clang-format":

			return false
		case "cargo":
			if len(words) > 1 && words[1] == "fmt" {
				return false
			}
		}
	}
	return isVerifyCommand(cmd)
}

func (a *Agent) convergenceRecordEdit(toolName string) {
	if a.convergenceLock == nil {
		return
	}
	if !productiveEditTools[toolName] {
		return
	}
	a.convergenceLock.recordEdit()
}

func (a *Agent) convergenceRecordEditError() {
	if a.convergenceLock == nil {
		return
	}
	a.convergenceLock.recordEditError()
}

func (a *Agent) convergenceCheck() string {
	if a.convergenceLock == nil {
		return ""
	}
	return a.convergenceLock.check()
}

func (a *Agent) resetConvergenceLock() {
	if a.convergenceLock != nil {
		a.convergenceLock.reset()
	}
}
