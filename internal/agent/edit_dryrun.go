package agent

import (
	"fmt"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

func countConflictMarkers(content string) int {
	n := 0
	for _, marker := range []string{"<<<<<<<", ">>>>>>>"} {
		n += countConflictMarkerLines(content, marker)
	}
	return n
}

func dryRunValidate(filePath, oldContent, newContent string) string {
	var fatalErrors []string

	if filepath.Ext(filePath) == ".go" && strings.TrimSpace(newContent) != "" {
		fset := token.NewFileSet()
		_, parseErr := parser.ParseFile(fset, filePath, newContent, 0)
		if parseErr != nil {
			if syntaxErrs := goSyntaxWarnings(filePath, parseErr); len(syntaxErrs) > 0 {
				maxReport := 2
				if len(syntaxErrs) < maxReport {
					maxReport = len(syntaxErrs)
				}
				for i := 0; i < maxReport; i++ {
					fatalErrors = append(fatalErrors, syntaxErrs[i])
				}
				if len(syntaxErrs) > maxReport {
					fatalErrors = append(fatalErrors,
						fmt.Sprintf("...and %d more syntax error(s)", len(syntaxErrs)-maxReport))
				}
			}
		}
	}

	if strings.ContainsRune(newContent, 0) {
		count := strings.Count(newContent, "\x00")
		fatalErrors = append(fatalErrors,
			fmt.Sprintf("Content contains %d null byte(s) (\\x00) - file would be corrupted.", count))
	}

	if strings.TrimSpace(oldContent) != "" && strings.TrimSpace(newContent) == "" {
		fatalErrors = append(fatalErrors,
			fmt.Sprintf("This edit would result in an EMPTY file (was %d bytes). "+
				"The old_text match likely consumed the entire file content.", len(oldContent)))
	}

	oldMarkers := countConflictMarkers(oldContent)
	newMarkers := countConflictMarkers(newContent)
	if newMarkers > oldMarkers {
		if marker := checkMergeConflictMarkers(filePath, newContent); marker != "" {
			fatalErrors = append(fatalErrors, marker)
		}
	}

	if len(fatalErrors) == 0 {
		return ""
	}

	debug.Log("dryrun", "pre-write validation blocked %s: %d fatal issue(s)", filePath, len(fatalErrors))

	var b strings.Builder
	b.WriteString("[Edit blocked by pre-write validation]\n")
	b.WriteString("The following fatal issue(s) were detected BEFORE writing. ")
	b.WriteString("Fix these in your edit and retry - the file has NOT been modified.\n\n")
	for _, e := range fatalErrors {
		b.WriteString("  - ")
		b.WriteString(e)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func dryRunValidateBatch(plans []fileEditPlan) map[string]string {
	blockers := make(map[string]string)
	for _, p := range plans {
		if msg := dryRunValidate(p.Path, p.OldContent, p.NewContent); msg != "" {
			blockers[p.Path] = msg
		}
	}
	if len(blockers) > 0 {
		debug.Log("dryrun", "batch pre-write validation blocked %d/%d file(s)", len(blockers), len(plans))
	}
	return blockers
}

type fileEditPlan struct {
	Path       string
	OldContent string
	NewContent string
}
