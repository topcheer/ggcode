package agent

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// #5136: #5118 fixed the WRITE side (Task field redaction), but pre-fix
// artifacts still carry raw prompts at rest:
//   - trajectory-learnings.jsonl lines written before the fix (project +
//     global stores), which TrajMergeInto would propagate into new projects
//   - project auto-memory insight entries containing "Task: <raw prompt>",
//     which are re-injected into future session prompts
// The scrub helpers below cure those at-rest carriers on read, idempotently.

// ScrubPromptLeakedMemoryEntries rewrites project auto-memory entries whose
// content still embeds an unredacted "Task:" insight line (pre-#5118 shape).
// Clean or already-redacted entries are left untouched (read-only check), so
// running this every session is cheap and idempotent.
func ScrubPromptLeakedMemoryEntries(workingDir string) {
	if workingDir == "" {
		return
	}
	dir := filepath.Join(workingDir, ".ggcode", "memory")
	des, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, de := range des {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, de.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := string(data)
		if !strings.Contains(content, "Task: ") {
			continue
		}
		scrubbed := redactTrainText(content)
		if scrubbed == content {
			continue
		}
		if err := os.WriteFile(path, []byte(scrubbed), 0o644); err != nil {
			debug.Log("memory", "#5136 scrub: failed to rewrite %s: %v", path, err)
		}
	}
}
