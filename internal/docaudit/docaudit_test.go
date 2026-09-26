package docaudit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const bt = "`"
const fence = "```"

func writeDoc(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAuditFindsStalePathsAndCommands(t *testing.T) {
	dir := t.TempDir()
	content := strings.Join([]string{
		"# Project",
		"",
		"Run " + bt + "/compact" + bt + " before finishing.",
		"Use " + bt + "/nosuchcmd" + bt + " to deploy.",
		"See " + bt + "internal/tool/tool.go" + bt + " for tools.",
		"See " + bt + "internal/tool/missing.go" + bt + " for more.",
		"A path like " + bt + "/tmp" + bt + " or " + bt + "/usr/bin" + bt + " is not a command.",
		"Bare identifiers like " + bt + "pattern" + bt + " are ignored.",
		"",
		"[guide](docs/guide.md) and [broken](docs/gone.md) links.",
		"",
		fence,
		"inside a fence " + bt + "also/missing.go" + bt + " and " + bt + "/fencecmd" + bt + " are ignored",
		fence,
		"",
		"    indented " + bt + "code/missing.go" + bt + " ignored too",
		"",
	}, "\n")
	writeDoc(t, dir, "AGENTS.md", content)
	writeDoc(t, dir, "internal/tool/tool.go", "package tool\n")
	writeDoc(t, dir, "docs/guide.md", "guide\n")
	writeDoc(t, dir, ".cursorrules", "no references here\n")

	known := map[string]bool{"compact": true}
	rep := Audit(dir, known)

	scanned := strings.Join(rep.Scanned, ",")
	if !strings.Contains(scanned, "AGENTS.md") || !strings.Contains(scanned, ".cursorrules") {
		t.Fatalf("scanned docs = %v", rep.Scanned)
	}
	var gotCmd, gotPathFile, gotPathLink int
	for _, f := range rep.Findings {
		switch {
		case f.Kind == KindStaleCommand:
			gotCmd++
			if f.Severity != SeverityError {
				t.Errorf("stale command severity = %q, want error", f.Severity)
			}
			if !strings.Contains(f.Quote, "nosuchcmd") {
				t.Errorf("unexpected stale command %q", f.Quote)
			}
			if f.Line != 4 {
				t.Errorf("stale command line = %d, want 4", f.Line)
			}
		case f.Quote == "internal/tool/missing.go":
			gotPathFile++
			if f.Line != 6 {
				t.Errorf("stale path line = %d, want 6", f.Line)
			}
		case f.Quote == "docs/gone.md":
			gotPathLink++
		}
	}
	if gotCmd != 1 {
		t.Errorf("got %d stale-command findings, want 1 (all: %+v)", gotCmd, rep.Findings)
	}
	if gotPathFile != 1 {
		t.Errorf("got %d missing.go findings, want 1", gotPathFile)
	}
	if gotPathLink != 1 {
		t.Errorf("got %d broken-link findings, want 1", gotPathLink)
	}
	if len(rep.Findings) != 3 {
		t.Errorf("total findings = %d, want 3: %+v", len(rep.Findings), rep.Findings)
	}
}

func TestAuditSkipsSystemPathsAndAmbiguousTokens(t *testing.T) {
	dir := t.TempDir()
	content := "Paths " + bt + "/private/var" + bt + " and " + bt + "/Volumes/data" + bt + " are skipped.\n" +
		"So is a bare " + bt + "identifier" + bt + ", a flag " + bt + "-verbose" + bt +
		", a wildcard " + bt + "src/**/*.go" + bt + ", and " + bt + "a b c" + bt + ".\n"
	writeDoc(t, dir, "CLAUDE.md", content)
	rep := Audit(dir, nil)
	if len(rep.Findings) != 0 {
		t.Errorf("findings = %+v, want none", rep.Findings)
	}
}

func TestAuditCustomCommandNamesAccepted(t *testing.T) {
	dir := t.TempDir()
	writeDoc(t, dir, "AGENTS.md", "Run "+bt+"/my-skill"+bt+" when done. "+bt+"/still-missing"+bt+" is unknown.\n")
	rep := Audit(dir, map[string]bool{"my-skill": true})
	if len(rep.Findings) != 1 || rep.Findings[0].Kind != KindStaleCommand || rep.Findings[0].Quote != "/still-missing" {
		t.Errorf("findings = %+v, want exactly one stale-command for /still-missing", rep.Findings)
	}
}

func TestAuditEmptyProject(t *testing.T) {
	rep := Audit(t.TempDir(), nil)
	if len(rep.Scanned) != 0 || len(rep.Findings) != 0 {
		t.Errorf("report = %+v, want empty", rep)
	}
	out := rep.String()
	if !strings.Contains(out, "scanned 0") {
		t.Errorf("String() = %q", out)
	}
}

func TestAuditCopilotInstructionsRelative(t *testing.T) {
	dir := t.TempDir()
	writeDoc(t, dir, ".github/copilot-instructions.md", "See [root doc](../AGENTS.md) and [bad](../missing.md).\n")
	writeDoc(t, dir, "AGENTS.md", "root\n")
	rep := Audit(dir, nil)
	if len(rep.Findings) != 1 || rep.Findings[0].Quote != "../missing.md" {
		t.Errorf("findings = %+v, want one ../missing.md warn", rep.Findings)
	}
}

func TestReportStringGroupsByFile(t *testing.T) {
	rep := &Report{
		Scanned:  []string{"AGENTS.md"},
		Findings: []Finding{{File: "AGENTS.md", Line: 3, Kind: KindStaleCommand, Severity: SeverityError, Detail: "x"}},
	}
	out := rep.String()
	if !strings.Contains(out, "AGENTS.md") || !strings.Contains(out, "3: [error]") {
		t.Errorf("String() = %q", out)
	}
}
