package im

import (
	"errors"
	"strings"
	"testing"
)

// stubDeps is a controllable SlashDeps for registry dispatch tests.
type stubDeps struct {
	cost      string
	usage     string
	mode      string
	tools     string
	files     string
	diff      string
	switched  string
	switchErr error
}

func (s *stubDeps) SessionCostSummary() (string, error)  { return s.cost, nil }
func (s *stubDeps) SessionUsageSummary() (string, error) { return s.usage, nil }
func (s *stubDeps) CurrentMode() string                  { return s.mode }
func (s *stubDeps) SwitchMode(name string) error {
	if s.switchErr != nil {
		return s.switchErr
	}
	s.switched = name
	return nil
}
func (s *stubDeps) ToolList() (string, error)             { return s.tools, nil }
func (s *stubDeps) ModifiedFiles() (string, error)        { return s.files, nil }
func (s *stubDeps) GitDiff(args []string) (string, error) { return s.diff, nil }

var _ SlashDeps = (*stubDeps)(nil)

func TestExecuteRegistrySlashCommand_Dispatch(t *testing.T) {
	d := &stubDeps{cost: "cost-text", usage: "usage-text", tools: "tools-text", files: "files-text", diff: "diff-text"}
	cases := map[string]string{
		"/cost":  "cost-text",
		"/usage": "usage-text",
		"/tools": "tools-text",
		"/files": "files-text",
		"/diff":  "diff-text",
	}
	for cmd, want := range cases {
		resp, handled := ExecuteRegistrySlashCommand(d, cmd)
		if !handled || resp != want {
			t.Fatalf("%s: handled=%v resp=%q want=%q", cmd, handled, resp, want)
		}
	}
	// Case-insensitive.
	if resp, handled := ExecuteRegistrySlashCommand(d, "/COST"); !handled || resp != "cost-text" {
		t.Fatalf("case-insensitive: handled=%v resp=%q", handled, resp)
	}
}

func TestExecuteRegistrySlashCommand_Mode(t *testing.T) {
	d := &stubDeps{mode: "supervised"}
	// Show form.
	resp, handled := ExecuteRegistrySlashCommand(d, "/mode")
	if !handled || resp != "Current permission mode: supervised" {
		t.Fatalf("show: handled=%v resp=%q", handled, resp)
	}
	// Valid switch.
	resp, handled = ExecuteRegistrySlashCommand(d, "/mode bypass")
	if !handled || d.switched != "bypass" || !strings.Contains(resp, "bypass") {
		t.Fatalf("switch: handled=%v switched=%q resp=%q", handled, d.switched, resp)
	}
	// #743: invalid name rejected, no silent supervised fallback.
	resp, handled = ExecuteRegistrySlashCommand(d, "/mode autoo")
	if !handled || !strings.Contains(resp, "unknown mode") || d.switched != "bypass" {
		t.Fatalf("invalid: handled=%v resp=%q switched=%q", handled, resp, d.switched)
	}
}

func TestExecuteRegistrySlashCommand_InteractiveHint(t *testing.T) {
	for _, name := range []string{"/stats", "/status", "/edit", "/copy", "/context"} {
		resp, handled := ExecuteRegistrySlashCommand(&stubDeps{}, name)
		if !handled || !strings.Contains(resp, "TUI") {
			t.Fatalf("%s: handled=%v resp=%q", name, handled, resp)
		}
	}
}

func TestExecuteRegistrySlashCommand_FallThrough(t *testing.T) {
	for _, text := range []string{"/restart", "/provider glm", "/help", "hello", "", "$ ls"} {
		if _, handled := ExecuteRegistrySlashCommand(&stubDeps{}, text); handled {
			t.Fatalf("%q must fall through", text)
		}
	}
}

func TestExecuteRegistrySlashCommand_HandlerError(t *testing.T) {
	d := &stubDeps{switchErr: errors.New("no agent")}
	resp, handled := ExecuteRegistrySlashCommand(d, "/mode auto")
	if !handled || !strings.Contains(resp, "failed") {
		t.Fatalf("error surface: handled=%v resp=%q", handled, resp)
	}
}

func TestIMSlashRegistry_Integrity(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range IMSlashRegistry() {
		if c.Name == "" {
			t.Fatal("registry entry with empty name")
		}
		if seen[c.Name] {
			t.Fatalf("duplicate registry name: %s", c.Name)
		}
		seen[c.Name] = true
		if c.Interactive && c.Handler != nil {
			t.Fatalf("%s: interactive entries must not carry handlers", c.Name)
		}
		if !c.Interactive && c.Handler == nil {
			t.Fatalf("%s: one-shot entry missing handler", c.Name)
		}
		if c.Help == "" {
			t.Fatalf("%s: missing help text", c.Name)
		}
		// Lookup must find every registered command.
		if _, ok := LookupIMSlashCommand(c.Name); !ok {
			t.Fatalf("lookup cannot find %s", c.Name)
		}
		// Help generation must list every one-shot command.
		help := strings.Join(IMSlashHelpLines(), "\n")
		if !c.Interactive && !strings.Contains(help, "/"+c.Name+" ") {
			t.Fatalf("help lines missing one-shot command %s", c.Name)
		}
	}
}

func TestIMSlashHelpLines_TUISummary(t *testing.T) {
	help := strings.Join(IMSlashHelpLines(), "\n")
	if !strings.Contains(help, "TUI-only") {
		t.Fatalf("interactive commands must collapse into a TUI-only summary line: %q", help)
	}
}
