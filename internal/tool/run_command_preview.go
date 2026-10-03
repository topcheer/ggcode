package tool

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/permission"
)

// dryRunPreview renders the r451 command rehearsal for the run_command
// dry_run path. Pure lexical analysis - the command is never spawned.
func (t RunCommand) dryRunPreview(command string) Result {
	rep := permission.PreviewCommand(command)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Dry-run rehearsal of %d segment(s); nothing was executed.\n", len(rep.Subcommands)))
	if len(rep.WriteTargets) == 0 && !rep.Destructive {
		b.WriteString("No write targets detected and no destructive segments; command appears read-only.\n")
		b.WriteString("Note: the preview is lexical and under-approximates shell semantics (expansions, eval, scripted writes).\n")
		return Result{Content: b.String()}
	}
	if len(rep.WriteTargets) > 0 {
		b.WriteString("Write targets (create/overwrite/delete):\n")
		for _, tgt := range rep.WriteTargets {
			b.WriteString("  - " + tgt + "\n")
		}
	}
	if rep.Destructive {
		b.WriteString("DESTRUCTIVE segment(s) present.\n")
	}
	for i, sc := range rep.Subcommands {
		if len(sc.Notes) == 0 && !sc.Destructive && len(sc.WriteTargets) == 0 {
			continue
		}
		b.WriteString(fmt.Sprintf("segment %d [%s]:", i+1, sc.Verb))
		if sc.Destructive {
			b.WriteString(" destructive")
		}
		if len(sc.WriteTargets) > 0 {
			b.WriteString(" targets=" + strings.Join(sc.WriteTargets, ","))
		}
		for _, n := range sc.Notes {
			b.WriteString(" note: " + n)
		}
		b.WriteString("\n")
	}
	b.WriteString("Note: the preview is lexical and under-approximates shell semantics (expansions, eval, scripted writes).\n")
	return Result{Content: b.String()}
}
