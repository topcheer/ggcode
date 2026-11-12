package agent

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// #5543: loop-core extraction. The pre-run workspace/environment checks
// (uncommitted-changes note, disk space, env-var drift, monorepo layout
// detection, checkpoint start, session-timeout arm, input-underspec
// advisory) become an explicit method; behavior unchanged (verbatim
// order, context injections only).

// runPreRunEnvironmentChecks captures workspace state and injects
// environment advisories before the stream loop starts.
func (a *Agent) runPreRunEnvironmentChecks(userText string, runStats *RunStats) {
	if workingDir := a.WorkingDir(); workingDir != "" {
		a.changeReconcile.capturePreRunState(workingDir)

		if n := a.changeReconcile.dirtyFileCount(); n > 0 {
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: fmt.Sprintf(
						"[workspace] Note: %d file(s) have uncommitted changes in the working tree "+
							"(your own work before this session). When committing, stage only the files "+
							"you modified during this task - do not use 'git add -A' or 'git commit -a' "+
							"unless the user explicitly asks.",
						n,
					),
				}},
			})
		}
	}

	a.diskSpace.reset()
	if workingDir := a.WorkingDir(); workingDir != "" {
		if diskMsg := a.diskSpace.check(workingDir); diskMsg != "" {
			debug.Log("disk-space", "low disk space detected, injecting advisory")
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: diskMsg}},
			})
		}
	}

	a.envDrift.reset()
	if workingDir := a.WorkingDir(); workingDir != "" {

		a.monorepoScoper.detectMonorepo(workingDir)
		if envMsg := a.envDrift.check(workingDir); envMsg != "" {
			debug.Log("env-drift", "env var drift detected, injecting advisory")
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: envMsg}},
			})
		}
	}

	if a.checkpoints != nil {
		a.checkpoints.StartRun(runStats.RunID())
	}

	a.sessionTimeout.start(a.currentMode().IsCruiseMode())

	if underspecHint := a.maybeWarnInputUnderspec(userText); underspecHint != "" {
		debug.Log("input-underspec", "underspecified user request detected, injecting advisory")
		a.contextManager.Add(provider.Message{
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: underspecHint}},
		})
	}
}
