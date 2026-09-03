package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/session"
)

func TestRootHelpUsesCompactLayout(t *testing.T) {
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Help(); err != nil {
		t.Fatalf("Help() error = %v", err)
	}

	help := out.String()
	want := []string{
		"Usage:\nggcode [flags]\nggcode [command]\n",
		"Flags:\n- --allowedTools stringArray: tools to allow in pipe mode (can be repeated)\n",
		"- -h, --help: help for ggcode\n",
	}
	for _, snippet := range want {
		if !strings.Contains(help, snippet) {
			t.Fatalf("expected help to contain %q, got:\n%s", snippet, help)
		}
	}
	if strings.Contains(help, "\t") {
		t.Fatalf("expected help to avoid tab-based alignment, got:\n%s", help)
	}
}

func gitInitForRootTest(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v\n%s", err, out)
	}
}

func TestResolveConfigFilePathPrefersProjectConfig(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ggcode.yaml"), []byte("max_iterations: 0\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()

	got, err := resolveConfigFilePath()
	if err != nil {
		t.Fatalf("resolveConfigFilePath() error = %v", err)
	}
	gotEval, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("EvalSymlinks(got) error = %v", err)
	}
	want := filepath.Join(dir, "ggcode.yaml")
	wantEval, err := filepath.EvalSymlinks(want)
	if err != nil {
		t.Fatalf("EvalSymlinks(want) error = %v", err)
	}
	if gotEval != wantEval {
		t.Fatalf("resolveConfigFilePath() = %q, want %q", got, want)
	}
}

func TestResolveConfigFilePathFallsBackToUserConfig(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()

	got, err := resolveConfigFilePath()
	if err != nil {
		t.Fatalf("resolveConfigFilePath() error = %v", err)
	}
	if want := filepath.Join(home, ".ggcode", "ggcode.yaml"); got != want {
		t.Fatalf("resolveConfigFilePath() = %q, want %q", got, want)
	}
}

func TestConfirmPlaintextAPIKeysBeforeTUIContinue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	content := `
vendors:
  zai:
    api_key: secret
    endpoints:
      cn-coding-openai:
        protocol: openai
        base_url: https://example.com
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	var out bytes.Buffer
	proceed, err := confirmPlaintextAPIKeysBeforeTUI(path, strings.NewReader("y\n"), &out, true)
	if err != nil {
		t.Fatalf("confirmPlaintextAPIKeysBeforeTUI() error = %v", err)
	}
	if !proceed {
		t.Fatal("expected startup to continue after migration")
	}
	// #1444-A: the notice runs BEFORE Load() - migration has NOT happened
	// yet (and never does under GGCODE_SKIP_AUTOCONFIG). The wording must
	// say found/will-migrate, never a false 'Migrated' claim.
	if !strings.Contains(out.String(), "will be migrated") {
		t.Fatalf("expected found/will-migrate notice, got %q", out.String())
	}
}

func TestConfirmPlaintextAPIKeysBeforeTUIIgnoreForever(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	content := `
vendors:
  zai:
    api_key: secret
    endpoints:
      cn-coding-openai:
        protocol: openai
        base_url: https://example.com
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	var out bytes.Buffer
	proceed, err := confirmPlaintextAPIKeysBeforeTUI(path, strings.NewReader("i\n"), &out, true)
	if err != nil {
		t.Fatalf("confirmPlaintextAPIKeysBeforeTUI() error = %v", err)
	}
	if !proceed {
		t.Fatal("expected migration to continue startup")
	}
	// The second call still detects plaintext (migration happens in Load(),
	// not in this function). But it always returns true to allow startup.
	var second bytes.Buffer
	proceed, err = confirmPlaintextAPIKeysBeforeTUI(path, strings.NewReader("n\n"), &second, true)
	if err != nil {
		t.Fatalf("confirmPlaintextAPIKeysBeforeTUI() second call error = %v", err)
	}
	if !proceed {
		t.Fatal("expected second call to continue (always allows startup)")
	}
}

func TestConfirmPlaintextAPIKeysBeforeTUIRejectsNonInteractive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	content := `
vendors:
  zai:
    api_key: secret
    endpoints:
      cn-coding-openai:
        protocol: openai
        base_url: https://example.com
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	var out bytes.Buffer
	// Auto-migration always succeeds — the function no longer blocks startup.
	proceed, err := confirmPlaintextAPIKeysBeforeTUI(path, strings.NewReader(""), &out, false)
	if err != nil {
		t.Fatalf("confirmPlaintextAPIKeysBeforeTUI() unexpected error = %v", err)
	}
	if !proceed {
		t.Fatal("expected auto-migration to allow startup in non-interactive mode")
	}
}

func TestResumeFlagWithValueIsParsedCorrectly(t *testing.T) {
	cmd := NewRootCmd()
	if err := cmd.ParseFlags([]string{"--resume", "session-abc"}); err != nil {
		t.Fatalf("ParseFlags() error = %v", err)
	}
	flag := cmd.Flags().Lookup("resume")
	if flag == nil {
		t.Fatal("expected resume flag")
	}
	if flag.Value.String() != "session-abc" {
		t.Fatalf("resume flag value = %q, want %q", flag.Value.String(), "session-abc")
	}
}

func TestResumeFlagPickerKeywordTriggersPicker(t *testing.T) {
	cmd := NewRootCmd()
	if err := cmd.ParseFlags([]string{"--resume-picker"}); err != nil {
		t.Fatalf("ParseFlags() error = %v", err)
	}
	pickerFlag, _ := cmd.Flags().GetBool("resume-picker")
	if !pickerFlag {
		t.Fatal("expected --resume-picker to be true")
	}
}

func TestGroupResumePickerSessionsGroupsCurrentWorkspaceFirst(t *testing.T) {
	current := filepath.Join(string(filepath.Separator), "work", "current")
	other := filepath.Join(string(filepath.Separator), "work", "other")
	sessions := []*session.Session{
		{ID: "current-newest", Title: "Current newest", Workspace: current},
		{ID: "other-newest", Title: "Other newest", Workspace: other},
		{ID: "current-older", Title: "Current older", Workspace: current},
	}
	currentGroup, otherGroup := groupResumePickerSessions(sessions, current)
	if len(currentGroup) != 2 || currentGroup[0].ID != "current-newest" || currentGroup[1].ID != "current-older" {
		t.Fatalf("unexpected current workspace grouping: %+v", currentGroup)
	}
	if len(otherGroup) != 1 || otherGroup[0].ID != "other-newest" {
		t.Fatalf("unexpected other workspace grouping: %+v", otherGroup)
	}
}

func TestResumePickerPaginatesEachGroupSeparately(t *testing.T) {
	current := filepath.Join(string(filepath.Separator), "work", "current")
	other := filepath.Join(string(filepath.Separator), "work", "other")
	var sessions []*session.Session
	for i := 0; i < 7; i++ {
		sessions = append(sessions, &session.Session{ID: fmt.Sprintf("current-%d", i), Title: fmt.Sprintf("Current %d", i), Workspace: current})
	}
	for i := 0; i < 6; i++ {
		sessions = append(sessions, &session.Session{ID: fmt.Sprintf("other-%d", i), Title: fmt.Sprintf("Other %d", i), Workspace: other})
	}
	currentGroup, otherGroup := groupResumePickerSessions(sessions, current)
	model := newResumePickerModel(currentGroup, otherGroup, current)
	if got := len(model.visibleItemsForGroup(resumePickerCurrentWorkspace)); got != 5 {
		t.Fatalf("current first page count = %d, want 5", got)
	}
	if got := len(model.visibleItemsForGroup(resumePickerOtherWorkspaces)); got != 5 {
		t.Fatalf("other first page count = %d, want 5", got)
	}
	model.turnPage(1)
	currentItems := model.visibleItemsForGroup(resumePickerCurrentWorkspace)
	if len(currentItems) != 2 || currentItems[0].session.ID != "current-5" {
		t.Fatalf("unexpected current second page: %+v", currentItems)
	}
	model.cursorGroup = resumePickerOtherWorkspaces
	model.cursorIndex = 0
	model.turnPage(1)
	otherItems := model.visibleItemsForGroup(resumePickerOtherWorkspaces)
	if len(otherItems) != 1 || otherItems[0].session.ID != "other-5" {
		t.Fatalf("unexpected other second page: %+v", otherItems)
	}
}

func TestMCPInstallCommandPersistsServer(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/ggcode.yaml"

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--config", path, "mcp", "install", "stdio", "npx", "-y", "12306-mcp", "stdio"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(out.String(), "Installed MCP server 12306-mcp") {
		t.Fatalf("expected install output, got:\n%s", out.String())
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	found := false
	hasBogusConfig := false
	for _, server := range cfg.MCPServers {
		if server.Name == "12306-mcp" && server.Command == "npx" {
			found = true
		}
		if server.Name == "config" || server.Command == "--config" {
			hasBogusConfig = true
		}
	}
	if !found {
		t.Fatalf("expected installed 12306-mcp server in config, got %+v", cfg.MCPServers)
	}
	if hasBogusConfig {
		t.Fatalf("did not expect leaked --config MCP server, got %+v", cfg.MCPServers)
	}
}

func TestMCPInstallCommandPersistsServerEnv(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/ggcode.yaml"

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--config", path, "mcp", "install", "z-ai", "--env", "ZAI_AI_API_KEY=xxxx", "--", "npx", "-y", "@z_ai/mcp-server"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(out.String(), "Installed MCP server z-ai") {
		t.Fatalf("expected install output, got:\n%s", out.String())
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	found := false
	for _, server := range cfg.MCPServers {
		if server.Name == "z-ai" && server.Command == "npx" && server.Env["ZAI_AI_API_KEY"] == "xxxx" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected installed z-ai server with env in config, got %+v", cfg.MCPServers)
	}
}

func TestMCPListCommandShowsConfiguredServers(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/ggcode.yaml"

	cfg := config.DefaultConfig()
	cfg.FilePath = path
	cfg.MCPServers = []config.MCPServerConfig{
		{Name: "12306-mcp", Type: "stdio", Command: "npx", Args: []string{"-y", "12306-mcp", "stdio"}},
		{Name: "web-reader", Type: "http", URL: "https://mcp.example.com/api"},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--config", path, "mcp", "list"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := out.String()
	if !strings.Contains(output, "12306-mcp [stdio]\n  target: npx -y 12306-mcp stdio") {
		t.Fatalf("expected stdio MCP entry, got:\n%s", output)
	}
	if !strings.Contains(output, "web-reader [http]\n  target: https://mcp.example.com/api") {
		t.Fatalf("expected http MCP entry, got:\n%s", output)
	}
}

func TestMCPListCommandRepairsMalformedFlagEntry(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/ggcode.yaml"

	cfg := config.DefaultConfig()
	cfg.FilePath = path
	cfg.MCPServers = []config.MCPServerConfig{
		{Name: "config", Type: "stdio", Command: "--config", Args: []string{"/tmp/test.yaml", "stdio", "npx", "-y", "12306-mcp"}},
		{Name: "12306-mcp", Type: "stdio", Command: "npx", Args: []string{"-y", "12306-mcp"}},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--config", path, "mcp", "list"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := out.String()
	if strings.Contains(output, "config [stdio] --config") {
		t.Fatalf("expected malformed MCP entry to be hidden, got:\n%s", output)
	}
	if !strings.Contains(output, "12306-mcp [stdio]\n  target: npx -y 12306-mcp") {
		t.Fatalf("expected valid MCP entry, got:\n%s", output)
	}

	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, server := range reloaded.MCPServers {
		if server.Command == "--config" || server.Name == "config" {
			t.Fatalf("expected malformed MCP entry to be removed from config, got %+v", reloaded.MCPServers)
		}
	}
}

func TestMCPInstallCommandPersistsHTTPHeaders(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/ggcode.yaml"
	const name = "header-reader"

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--config", path, "mcp", "install", name, "-t", "http", "https://mcp.example.com/api", "--header", "Authorization: Bearer xxx"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(out.String(), "Installed MCP server "+name) {
		t.Fatalf("expected install output, got:\n%s", out.String())
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	found := false
	for _, server := range cfg.MCPServers {
		if server.Name == name && server.Type == "http" && server.URL == "https://mcp.example.com/api" && server.Headers["Authorization"] == "Bearer xxx" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected installed %s server with headers in config, got %+v", name, cfg.MCPServers)
	}
}

func TestMCPUninstallCommandRemovesServer(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/ggcode.yaml"

	cfg := config.DefaultConfig()
	cfg.FilePath = path
	cfg.MCPServers = []config.MCPServerConfig{{Name: "12306-mcp", Type: "stdio", Command: "npx", Args: []string{"-y", "12306-mcp", "stdio"}}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--config", path, "mcp", "uninstall", "12306-mcp"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(out.String(), "Uninstalled MCP server 12306-mcp") {
		t.Fatalf("expected uninstall output, got:\n%s", out.String())
	}

	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, server := range reloaded.MCPServers {
		if server.Name == "12306-mcp" {
			t.Fatalf("expected 12306-mcp to be removed, got %+v", reloaded.MCPServers)
		}
	}
}

func TestRootUsageUsesCompactFlagLayout(t *testing.T) {
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Usage(); err != nil {
		t.Fatalf("Usage() error = %v", err)
	}

	usage := out.String()
	if !strings.Contains(usage, "Flags:\n") ||
		!strings.Contains(usage, "- --config string: config file path\n") ||
		!strings.Contains(usage, "- -h, --help: help for ggcode\n") {
		t.Fatalf("expected compact flag layout in usage, got:\n%s", usage)
	}
}

// TestParseA2AMaxTasks pins #1422-A: a zero/negative MaxTasks (first-run
// --bypass install where Validate's early return skips the default fill)
// falls back to 5 instead of overriding the handler default and rejecting
// the first task.
func TestParseA2AMaxTasks(t *testing.T) {
	for _, in := range []int{0, -1} {
		if got := parseA2AMaxTasks(in); got != 5 {
			t.Fatalf("parseA2AMaxTasks(%d) = %d, want 5", in, got)
		}
	}
	if got := parseA2AMaxTasks(7); got != 7 {
		t.Fatalf("positive value not preserved: %d", got)
	}
}
