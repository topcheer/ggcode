package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/auth"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/harness"
	"github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/lsp"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestRenderMarkdown(t *testing.T) {
	// Should not panic and return non-empty string
	result := RenderMarkdown("# Hello\n\nWorld")
	if result == "" {
		t.Error("expected non-empty markdown output")
	}
}

func TestSlashCommandsOpenInspectorPanels(t *testing.T) {
	tests := []struct {
		command string
		kind    inspectorPanelKind
	}{
		{"/sessions", inspectorPanelSessions},
		{"/resume", inspectorPanelSessions},
		{"/export", inspectorPanelSessions},
		{"/agents", inspectorPanelAgents},
		{"/checkpoints", inspectorPanelCheckpoints},
		{"/memory", inspectorPanelMemory},
		{"/todo", inspectorPanelTodos},
		{"/plugins", inspectorPanelPlugins},
		{"/config", inspectorPanelConfig},
		{"/status", inspectorPanelStatus},
	}

	for _, tt := range tests {
		m := newTestModel()
		if tt.kind == inspectorPanelSessions {
			store, err := session.NewJSONLStore(t.TempDir())
			if err != nil {
				t.Fatalf("new session store: %v", err)
			}
			m.sessionStore = store
		}
		if tt.kind == inspectorPanelPlugins {
			m.pluginMgr = plugin.NewManager()
		}
		nextCmd := m.handleCommand(tt.command)
		if nextCmd != nil {
			t.Fatalf("%s: expected immediate panel open, got command", tt.command)
		}
		if m.inspectorPanel == nil || m.inspectorPanel.kind != tt.kind {
			t.Fatalf("%s: expected inspector panel %q, got %#v", tt.command, tt.kind, m.inspectorPanel)
		}
	}
}

func TestInspectorSessionsEnterSchedulesResumeCommand(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	ses := &session.Session{
		ID:        "sess-1",
		Title:     "Saved chat",
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now(),
		Messages: []provider.Message{
			{Role: "user", Content: []provider.ContentBlock{provider.TextBlock("hello")}},
		},
	}
	if err := store.Save(ses); err != nil {
		t.Fatalf("save session: %v", err)
	}

	m := newTestModel()
	m.sessionStore = store
	m.openInspectorPanel(inspectorPanelSessions)

	next, cmd := m.handleInspectorPanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected resume cmd from sessions panel")
	}
	if next.inspectorPanel != nil {
		t.Fatal("expected sessions panel to close before resuming")
	}
}

func TestInspectorTodosClearActionPersistsEmptyList(t *testing.T) {
	workspace := t.TempDir()
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(prevWD) }()
	ggDir := filepath.Join(workspace, ".ggcode")
	if err := os.MkdirAll(ggDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ggDir, "todos.json"), []byte(`[{"id":"t1","content":"work","status":"pending"}]`), 0o644); err != nil {
		t.Fatalf("write todos: %v", err)
	}

	m := newTestModel()
	m.openInspectorPanel(inspectorPanelTodos)
	_, _ = m.handleInspectorPanelKey(tea.KeyPressMsg{Text: "c"})

	if _, err := os.Stat(filepath.Join(ggDir, "todos.json")); !os.IsNotExist(err) {
		t.Fatalf("expected todos file to be removed, err=%v", err)
	}
}

func gitInitForTUI(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v\n%s", err, out)
	}
}

func asModel(t *testing.T, model tea.Model) Model {
	t.Helper()
	switch typed := model.(type) {
	case Model:
		return typed
	case *Model:
		return *typed
	default:
		t.Fatalf("unexpected tea.Model type %T", model)
		return Model{}
	}
}

func TestRenderMarkdown_PlainText(t *testing.T) {
	result := RenderMarkdown("just plain text")
	if result == "" {
		t.Error("expected non-empty output")
	}
}

func TestFormatDiff(t *testing.T) {
	diff := `@@ -1,3 +1,4 @@
 line1
-line2
+line2_modified
+new line
 line3`
	result := FormatDiff(diff)
	if result == "" {
		t.Error("expected non-empty diff output")
	}
}

func TestMCPServerUpdateRefreshesSkills(t *testing.T) {
	m := newTestModel()
	enabled := false
	mgr := commands.NewManager(t.TempDir())
	mgr.SetExtraProviders(func() []*commands.Command {
		if !enabled {
			return nil
		}
		return []*commands.Command{{
			Name:          "docs:summarize",
			Source:        commands.SourceMCP,
			LoadedFrom:    commands.LoadedFromMCP,
			UserInvocable: true,
			Enabled:       true,
		}}
	})
	m.SetCommandsManager(mgr)
	if _, ok := m.customCmds["docs:summarize"]; ok {
		t.Fatalf("did not expect MCP skill before server update: %+v", m.customCmds)
	}
	enabled = true
	next, cmd := m.Update(mcpServersMsg{Servers: []plugin.MCPServerInfo{{
		Name:        "docs",
		PromptNames: []string{"summarize"},
		Status:      plugin.MCPStatusConnected,
	}}})
	if cmd != nil {
		t.Fatalf("expected mcpServersMsg to complete inline")
	}
	updated := next.(Model)
	if _, ok := updated.customCmds["docs:summarize"]; ok {
		t.Fatalf("did not expect MCP skill to appear in slash commands: %+v", updated.customCmds)
	}
}

func TestFormatDiff_Empty(t *testing.T) {
	result := FormatDiff("")
	if result != "" {
		t.Error("expected empty string for empty diff")
	}
}

func TestIsDiffContent(t *testing.T) {
	if !IsDiffContent("@@ -1,3 +1,4 @@") {
		t.Error("expected true for diff hunk header")
	}
	if IsDiffContent("just some text\nwith no diff") {
		t.Error("expected false for non-diff text")
	}
}

func TestFormatToolStatus(t *testing.T) {
	msg := ToolStatusMsg{ToolName: "read_file", DisplayName: "Read", Detail: "README.md", Running: false, Result: "file content"}
	result := FormatToolStatus(msg)
	if result == "" {
		t.Error("expected non-empty tool status")
	}
}

func TestFormatToolStatus_Error(t *testing.T) {
	msg := ToolStatusMsg{ToolName: "run_command", DisplayName: "Run", Detail: "go test ./...", Running: false, Result: "exit code 1", IsError: true}
	result := FormatToolStatus(msg)
	if result == "" {
		t.Error("expected non-empty error status")
	}
}

func TestFormatToolStatus_HidesReadFileBody(t *testing.T) {
	msg := ToolStatusMsg{
		ToolName: "read_file",
		Running:  false,
		Result:   "package main\n\nfunc main() {}\n",
	}
	result := FormatToolStatus(msg)
	if strings.Contains(result, "package main") {
		t.Error("expected read_file body to be hidden from TUI output")
	}
	if !strings.Contains(result, "lines of content") {
		t.Error("expected read_file summary in TUI output")
	}
}

func TestFormatToolStatus_RunCommandErrorShowsOnlyExitStatus(t *testing.T) {
	msg := ToolStatusMsg{
		ToolName: "run_command",
		Running:  false,
		Result:   "STDERR:\nboom\nCommand failed: exit status 2",
		IsError:  true,
	}
	result := FormatToolStatus(msg)
	if strings.Contains(result, "boom") {
		t.Error("expected stderr body to be hidden from TUI output")
	}
	if !strings.Contains(result, "exit status 2") {
		t.Error("expected exit status summary in TUI output")
	}
}

func TestFormatToolStatus_WaitCommandShowsCompactProgress(t *testing.T) {
	msg := ToolStatusMsg{
		ToolName: "wait_command",
		Running:  false,
		Result:   "Job ID: cmd-1\nStatus: running\nDuration: 2s\nTimeout: 30s\nTotal lines: 4\nRecent output:\nstep 4\n",
	}
	result := FormatToolStatus(msg)
	if !strings.Contains(result, "running") || !strings.Contains(result, "4 lines") || !strings.Contains(result, "step 4") {
		t.Fatalf("expected compact async progress summary, got %q", result)
	}
}

func TestDescribeToolWriteCommandInputUsesJobID(t *testing.T) {
	present := describeTool(LangEnglish, "write_command_input", `{"job_id":"cmd-7","input":"y"}`)
	if present.DisplayName != "Run" {
		t.Fatalf("expected run display name, got %q", present.DisplayName)
	}
	if present.Detail != "cmd-7" {
		t.Fatalf("expected job id detail, got %q", present.Detail)
	}
	if present.Activity != "Running cmd-7" {
		t.Fatalf("expected job id activity, got %q", present.Activity)
	}
}

func TestSkillsPanelPagination(t *testing.T) {
	m := NewModel(nil, nil)
	m.skillsPanel = &skillsPanelState{page: 0}
	m.width = 100
	m.customCmds = map[string]*commands.Command{}
	for i := 1; i <= 11; i++ {
		name := fmt.Sprintf("skill-%02d", i)
		m.customCmds[name] = &commands.Command{
			Name:          name,
			Description:   "Test skill",
			UserInvocable: true,
			Source:        commands.SourceUser,
			LoadedFrom:    commands.LoadedFromSkills,
			Enabled:       true,
		}
	}

	pageOne := m.renderSkillsPanel()
	if !strings.Contains(pageOne, "1/2") {
		t.Fatalf("expected first page footer, got %q", pageOne)
	}
	if !strings.Contains(pageOne, "skill-10") {
		t.Fatalf("expected skill-10 on first page")
	}
	if strings.Contains(pageOne, "skill-11") {
		t.Fatalf("did not expect skill-11 on first page")
	}

	m.skillsPanel.page = 1
	pageTwo := m.renderSkillsPanel()
	if !strings.Contains(pageTwo, "2/2") {
		t.Fatalf("expected second page footer, got %q", pageTwo)
	}
	if !strings.Contains(pageTwo, "skill-11") {
		t.Fatalf("expected skill-11 on second page")
	}
}

func TestDescribeToolReadFile(t *testing.T) {
	present := describeTool(LangEnglish, "read_file", `{"path":"docs/guide.md"}`)
	if present.DisplayName != "Read" {
		t.Fatalf("expected friendly display name, got %q", present.DisplayName)
	}
	if present.Detail != "docs/guide.md" {
		t.Fatalf("expected file detail, got %q", present.Detail)
	}
	if present.Activity != "Reading docs/guide.md" {
		t.Fatalf("expected reading activity, got %q", present.Activity)
	}
}

func TestDescribeToolWriteFileUsesWorkspaceRelativePath(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "internal", "context"), 0755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("chdir workspace: %v", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()

	target := filepath.Join(workspace, "internal", "context", "manager.go")
	present := describeTool(LangEnglish, "write_file", `{"file_path":"`+target+`","content":"x"}`)
	if present.Detail != "internal/context/manager.go" {
		t.Fatalf("expected workspace-relative detail, got %q", present.Detail)
	}
	if present.Activity != "Writing internal/context/manager.go" {
		t.Fatalf("expected workspace-relative path in activity, got %q", present.Activity)
	}
}

func TestDescribeToolSearchLocalized(t *testing.T) {
	present := describeTool(LangZhCN, "grep", `{"pattern":"ContextManager","path":"internal/context"}`)
	if present.DisplayName != "搜索" {
		t.Fatalf("expected localized display name, got %q", present.DisplayName)
	}
	if present.Detail != "ContextManager" {
		t.Fatalf("expected search detail, got %q", present.Detail)
	}
	if present.Activity != "搜索 ContextManager" {
		t.Fatalf("expected localized activity, got %q", present.Activity)
	}
}

func TestDescribeToolRunCommandUsesLeadingCommentAsTitle(t *testing.T) {
	present := describeTool(LangEnglish, "run_command", `{"command":"# Stage the fix\ngit add internal/tui/activity_groups.go\n"}`)
	if present.DisplayName != "Stage the fix" {
		t.Fatalf("expected command title from comment, got %q", present.DisplayName)
	}
	if present.Detail != "" {
		t.Fatalf("expected command title to replace raw command detail, got %q", present.Detail)
	}
	if present.Activity != "Running Stage the fix" {
		t.Fatalf("expected activity to use comment title, got %q", present.Activity)
	}
}

func TestCompactToolArgsPreviewHidesEmptyObject(t *testing.T) {
	if got := compactToolArgsPreview(`{}`); got != "" {
		t.Fatalf("expected empty object args to be hidden, got %q", got)
	}
}

func TestFormatToolItemSummaryHidesTrivialArgsAndSummary(t *testing.T) {
	got := formatToolItemSummary(LangEnglish, ToolStatusMsg{
		ToolName:    "write_file",
		DisplayName: "Write",
		Args:        `{}`,
		Result:      "Write",
	})
	if got != "Write" {
		t.Fatalf("expected trivial tool summary to collapse to display name, got %q", got)
	}
}

func TestFormatToolItemSummaryRunCommandShowsScriptPreview(t *testing.T) {
	got := formatToolItemSummary(LangEnglish, ToolStatusMsg{
		ToolName:    "run_command",
		DisplayName: "Restart metro",
		RawArgs:     `{"command":"# Restart metro\ncd /tmp/app\nrm -rf .metro-cache\nnpm install\nnpm run start -- --reset-cache\nsleep 2\necho ready\n"}`,
		Result:      "booting\nready\n",
	})
	if !strings.Contains(got, "Restart metro") {
		t.Fatalf("expected title in command summary, got %q", got)
	}
	if strings.Contains(got, "# Restart metro") {
		t.Fatalf("expected title comment to be removed from script preview, got %q", got)
	}
	if !strings.Contains(got, "cd /tmp/app") || !strings.Contains(got, "npm run start -- --reset-cache") {
		t.Fatalf("expected script preview lines, got %q", got)
	}
	if !strings.Contains(got, "… 1 more script line") {
		t.Fatalf("expected hidden script line summary, got %q", got)
	}
	if !strings.Contains(got, "2 lines of output") {
		t.Fatalf("expected output line summary, got %q", got)
	}
}

func TestDescribeToolListDirectoryUsesWorkspaceRelativePath(t *testing.T) {
	present := describeTool(LangEnglish, "list_directory", `{"path":"internal/context"}`)
	if present.Detail != "internal/context" {
		t.Fatalf("expected workspace-relative directory, got %q", present.Detail)
	}
	if present.Activity != "Listing internal/context" {
		t.Fatalf("expected workspace-relative listing activity, got %q", present.Activity)
	}
}

func TestFormatToolStartUsesFriendlyDisplay(t *testing.T) {
	result := FormatToolStart(ToolStatusMsg{
		ToolName:    "read_file",
		DisplayName: "Read",
		Detail:      "README.md",
		Running:     true,
	})
	if strings.Contains(result, "read_file") {
		t.Fatalf("expected raw tool name to be hidden, got %q", result)
	}
	if !strings.Contains(result, "Read README.md") {
		t.Fatalf("expected friendly inline label, got %q", result)
	}
}

func TestAssistantAndToolBulletsUseDifferentStyles(t *testing.T) {
	m := newTestModel()
	m.appendStreamChunk("hello")
	stream := m.output.String()
	tool := FormatToolStart(ToolStatusMsg{
		ToolName:    "read_file",
		DisplayName: "Read",
		Detail:      "README.md",
		Running:     true,
	})

	assistantPrefix := assistantBulletStyle.Render("● ")
	toolPrefix := toolBulletStyle.Render("● ")
	if assistantPrefix == toolPrefix {
		t.Fatal("expected assistant and tool bullet styles to differ")
	}
	if !strings.HasPrefix(stream, assistantPrefix) {
		t.Fatalf("expected assistant stream output to use assistant bullet style, got %q", stream)
	}
	if !strings.HasPrefix(tool, toolPrefix) {
		t.Fatalf("expected tool start output to use tool bullet style, got %q", tool)
	}
}

func TestCompactionBulletUsesDedicatedStyle(t *testing.T) {
	m := newTestModel()
	m.appendStreamStatusLine("[compacting conversation to stay within context window]")
	got := m.output.String()

	statusPrefix := compactionBulletStyle.Render("● ")
	if statusPrefix == assistantBulletStyle.Render("● ") || statusPrefix == toolBulletStyle.Render("● ") {
		t.Fatal("expected compaction bullet style to differ from assistant and tool styles")
	}
	if !strings.HasPrefix(got, statusPrefix) {
		t.Fatalf("expected compaction status output to use dedicated bullet style, got %q", got)
	}
}

func TestHelpText(t *testing.T) {
	h := newTestModel().helpText()
	if h == "" {
		t.Error("expected non-empty help text")
	}
	if !strings.Contains(h, "/help, /?") {
		t.Error("expected /? alias in help text")
	}
	if strings.Contains(h, "/cost") {
		t.Error("expected cost command to be removed from help text")
	}
	if !strings.Contains(h, "/init") {
		t.Error("expected /init in help text")
	}
	if !strings.Contains(h, "/harness") {
		t.Error("expected /harness in help text")
	}
}

func TestProviderCommandOpensProviderPanel(t *testing.T) {
	m := newTestModel()
	m.SetConfig(config.DefaultConfig())

	cmd := m.handleCommand("/provider")
	if cmd != nil {
		t.Fatal("expected /provider to open inline panel without async command")
	}
	if m.providerPanel == nil {
		t.Fatal("expected provider panel to be open")
	}
}

func TestModelCommandOpensModelPanel(t *testing.T) {
	m := newTestModel()
	m.SetConfig(config.DefaultConfig())

	cmd := m.handleCommand("/model")
	if cmd == nil {
		t.Fatal("expected /model to open panel and refresh models")
	}
	if m.modelPanel == nil {
		t.Fatal("expected model panel to be open")
	}
}

func TestUpdateCommandWithoutServiceShowsUnavailable(t *testing.T) {
	m := newTestModel()

	cmd := m.handleCommand("/update")
	if cmd != nil {
		t.Fatal("expected /update without service to respond inline")
	}
	if !strings.Contains(m.output.String(), "Update is unavailable") {
		t.Fatalf("expected unavailable message, got %q", m.output.String())
	}
}

func TestSidebarShowsUpdateHintWhenAvailable(t *testing.T) {
	m := newTestModel()
	m.handleResize(140, 40)
	m.updateInfo = updateCheckResultMsg{}.Result
	m.updateInfo.HasUpdate = true
	m.updateInfo.LatestVersion = "v1.2.3"

	sidebar := stripAnsi(m.renderSidebar())
	if !strings.Contains(sidebar, "New release available") || !strings.Contains(sidebar, "/update") {
		t.Fatalf("expected update hint in sidebar, got %q", sidebar)
	}
}

type fakeMCPManager struct {
	retried     []string
	installed   []config.MCPServerConfig
	uninstalled []string
}

func (f *fakeMCPManager) Retry(name string) bool {
	f.retried = append(f.retried, name)
	return true
}

func (f *fakeMCPManager) Install(ctx context.Context, server config.MCPServerConfig) error {
	f.installed = append(f.installed, server)
	return nil
}

func (f *fakeMCPManager) Uninstall(name string) bool {
	f.uninstalled = append(f.uninstalled, name)
	return true
}

func (f *fakeMCPManager) Disconnect(name string) bool { return true }
func (f *fakeMCPManager) Reconnect(name string) bool  { return true }
func (f *fakeMCPManager) PendingOAuth() *plugin.MCPOAuthRequiredError {
	return nil
}
func (f *fakeMCPManager) ClearPendingOAuth() {}

func TestMCPCommandOpensPanel(t *testing.T) {
	m := newTestModel()
	m.mcpServers = []MCPInfo{{
		Name:          "web-reader",
		Transport:     "http",
		Connected:     true,
		ToolNames:     []string{"fetch", "search"},
		PromptNames:   []string{"summarize"},
		ResourceNames: []string{"docs"},
	}}

	cmd := m.handleCommand("/mcp")
	if cmd != nil {
		t.Fatal("expected /mcp to open inline panel without async command")
	}
	if m.mcpPanel == nil {
		t.Fatal("expected MCP panel to be open")
	}
	panel := m.renderContextPanel()
	if !strings.Contains(panel, "web-reader") || !strings.Contains(panel, "fetch") || !strings.Contains(panel, "summarize") || !strings.Contains(panel, "docs") {
		t.Fatal("expected MCP panel to show tools, prompts, and resources")
	}
	if strings.Contains(panel, "claude-user") {
		t.Fatal("expected MCP panel to hide migration source details")
	}
}

func TestMCPPanelReconnectKeyRetriesSelectedServer(t *testing.T) {
	m := newTestModel()
	manager := &fakeMCPManager{}
	m.SetMCPManager(manager)
	m.mcpServers = []MCPInfo{{Name: "web-reader", Transport: "http", Error: "connection timed out"}}
	m.openMCPPanel()

	next, cmd := m.Update(tea.KeyPressMsg{Text: "r"})
	if cmd != nil {
		t.Fatal("expected reconnect to run synchronously")
	}
	m2 := next.(Model)
	if len(manager.retried) != 1 || manager.retried[0] != "web-reader" {
		t.Fatalf("expected reconnect to target selected MCP server, got %v", manager.retried)
	}
	if m2.mcpPanel == nil || !strings.Contains(m2.mcpPanel.message, "Reconnecting web-reader") {
		t.Fatal("expected reconnect status message in MCP panel")
	}
}

func TestMCPPanelCtrlCClosesPanel(t *testing.T) {
	m := newTestModel()
	m.mcpServers = []MCPInfo{{Name: "web-reader", Transport: "http"}}
	m.openMCPPanel()

	next, cmd := m.Update(tea.KeyPressMsg{Text: "ctrl+c"})
	if cmd != nil {
		t.Fatal("expected ctrl-c panel close to be synchronous")
	}
	m2 := next.(Model)
	if m2.exitConfirmPending {
		t.Fatal("expected ctrl-c in MCP panel to clear exit confirmation")
	}
	if m2.mcpPanel != nil {
		t.Fatal("expected MCP panel to close on ctrl-c")
	}
}

func TestMCPPanelInstallPersistsConfigAndCallsManager(t *testing.T) {
	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.FilePath = filepath.Join(t.TempDir(), "ggcode.yaml")
	m.SetConfig(cfg)
	manager := &fakeMCPManager{}
	m.SetMCPManager(manager)
	m.openMCPPanel()

	next, cmd := m.Update(tea.KeyPressMsg{Text: "i"})
	if cmd != nil {
		t.Fatal("expected install mode toggle to be synchronous")
	}
	m = next.(Model)
	for _, key := range []tea.KeyPressMsg{
		{Text: "stdio npx -y 12306-mcp stdio"},
	} {
		next, cmd = m.Update(key)
		if cmd != nil {
			t.Fatal("expected typing in install mode to stay synchronous")
		}
		m = next.(Model)
	}

	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected install submission to return a command")
	}
	msg := cmd()
	next, cmd = next.(Model).Update(msg)
	if cmd != nil {
		t.Fatal("expected install result to update synchronously")
	}
	m2 := next.(Model)
	if len(manager.installed) != 1 || manager.installed[0].Name != "12306-mcp" {
		t.Fatalf("expected MCP manager install call, got %+v", manager.installed)
	}
	if len(m2.config.MCPServers) != 1 || m2.config.MCPServers[0].Command != "npx" {
		t.Fatalf("expected config MCP server to be persisted, got %+v", m2.config.MCPServers)
	}
	if m2.mcpPanel == nil || !strings.Contains(m2.mcpPanel.message, "Installed MCP server 12306-mcp") {
		t.Fatalf("expected install success message, got %+v", m2.mcpPanel)
	}
}

func TestMCPPanelBrowserPresetInstallsPlaywright(t *testing.T) {
	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.FilePath = filepath.Join(t.TempDir(), "ggcode.yaml")
	m.SetConfig(cfg)
	manager := &fakeMCPManager{}
	m.SetMCPManager(manager)
	m.openMCPPanel()

	next, cmd := m.Update(tea.KeyPressMsg{Text: "b"})
	if cmd == nil {
		t.Fatal("expected browser preset install to return a command")
	}
	msg := cmd()
	next, cmd = next.(Model).Update(msg)
	if cmd != nil {
		t.Fatal("expected install result to update synchronously")
	}
	m2 := next.(Model)
	if len(manager.installed) != 1 {
		t.Fatalf("expected preset install call, got %+v", manager.installed)
	}
	got := manager.installed[0]
	if got.Name != "playwright" || got.Command != "npx" || got.Type != "stdio" {
		t.Fatalf("unexpected preset config: %+v", got)
	}
	if len(m2.config.MCPServers) != 1 || m2.config.MCPServers[0].Name != "playwright" {
		t.Fatalf("expected playwright MCP server persisted, got %+v", m2.config.MCPServers)
	}
	if m2.mcpPanel == nil || !strings.Contains(m2.mcpPanel.message, "Installed MCP server playwright") {
		t.Fatalf("expected preset success message, got %+v", m2.mcpPanel)
	}
}

func TestMCPPanelUninstallRemovesConfigAndCallsManager(t *testing.T) {
	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.FilePath = filepath.Join(t.TempDir(), "ggcode.yaml")
	cfg.MCPServers = []config.MCPServerConfig{{Name: "web-reader", Type: "http", URL: "https://example.com"}}
	m.SetConfig(cfg)
	manager := &fakeMCPManager{}
	m.SetMCPManager(manager)
	m.mcpServers = []MCPInfo{{Name: "web-reader", Transport: "http", Connected: true}}
	m.openMCPPanel()

	next, cmd := m.Update(tea.KeyPressMsg{Text: "x"})
	if cmd == nil {
		t.Fatal("expected uninstall to return a command")
	}
	msg := cmd()
	next, cmd = next.(Model).Update(msg)
	if cmd != nil {
		t.Fatal("expected uninstall result to update synchronously")
	}
	m2 := next.(Model)
	if len(manager.uninstalled) != 1 || manager.uninstalled[0] != "web-reader" {
		t.Fatalf("expected manager uninstall call, got %+v", manager.uninstalled)
	}
	if len(m2.config.MCPServers) != 0 {
		t.Fatalf("expected config MCP servers to be removed, got %+v", m2.config.MCPServers)
	}
	if m2.mcpPanel == nil || !strings.Contains(m2.mcpPanel.message, "Uninstalled MCP server web-reader") {
		t.Fatalf("expected uninstall success message, got %+v", m2.mcpPanel)
	}
}

func TestProviderPanelCtrlCClosesPanel(t *testing.T) {
	m := newTestModel()
	m.SetConfig(config.DefaultConfig())
	m.openProviderPanel()

	next, cmd := m.Update(tea.KeyPressMsg{Text: "ctrl+c"})
	if cmd != nil {
		t.Fatal("expected ctrl-c panel close to be synchronous")
	}
	m2 := next.(Model)
	if m2.exitConfirmPending {
		t.Fatal("expected ctrl-c in provider panel to clear exit confirmation")
	}
	if m2.providerPanel != nil {
		t.Fatal("expected provider panel to close on ctrl-c")
	}
}

func TestModelPanelCtrlCClosesPanel(t *testing.T) {
	m := newTestModel()
	m.SetConfig(config.DefaultConfig())
	m.modelPanel = &modelPanelState{models: []string{"gpt-4o-mini"}}

	next, cmd := m.Update(tea.KeyPressMsg{Text: "ctrl+c"})
	if cmd != nil {
		t.Fatal("expected ctrl-c panel close to be synchronous")
	}
	m2 := next.(Model)
	if m2.exitConfirmPending {
		t.Fatal("expected ctrl-c in model panel to clear exit confirmation")
	}
	if m2.modelPanel != nil {
		t.Fatal("expected model panel to close on ctrl-c")
	}
}

func TestModelPanelRefreshFallsBackToBuiltInModels(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.FilePath = filepath.Join(t.TempDir(), "ggcode.yaml")
	cfg.Vendors = map[string]config.VendorConfig{
		"google": {
			DisplayName: "Google Gemini",
			APIKey:      "test-key",
			Endpoints: map[string]config.EndpointConfig{
				"api": {
					DisplayName:   "Gemini API",
					Protocol:      "gemini",
					BaseURL:       "https://127.0.0.1.invalid",
					DefaultModel:  "gemini-1.5-flash",
					SelectedModel: "gemini-1.5-flash",
					Models:        []string{"gemini-1.5-flash", "gemini-1.5-pro"},
				},
			},
		},
	}
	cfg.Vendor = "google"
	cfg.Endpoint = "api"
	cfg.Model = "gemini-1.5-flash"

	m := newTestModel()
	m.SetConfig(cfg)
	cmd := m.openModelPanel()
	if cmd == nil {
		t.Fatal("expected openModelPanel to return refresh command")
	}

	next, cmd2 := m.Update(cmd())
	if cmd2 != nil {
		t.Fatal("expected model refresh result to update synchronously")
	}
	m2 := next.(Model)
	if m2.modelPanel == nil {
		t.Fatal("expected model panel to remain open")
	}
	if len(m2.modelPanel.models) != 2 || m2.modelPanel.models[0] != "gemini-1.5-flash" {
		t.Fatalf("expected built-in models fallback, got %#v", m2.modelPanel.models)
	}
	if !strings.Contains(m2.modelPanel.message, "Using built-in models") && !strings.Contains(m2.modelPanel.message, "built-in") {
		t.Fatalf("expected built-in fallback message, got %q", m2.modelPanel.message)
	}
}

func TestModelPanelClosesAfterSelectingModel(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.FilePath = filepath.Join(t.TempDir(), "ggcode.yaml")
	cfg.Vendor = "openai"
	cfg.Endpoint = "api"
	cfg.Model = "gpt-4o-mini"
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	m := newTestModel()
	m.SetConfig(cfg)
	m.modelPanel = &modelPanelState{
		models:   []string{"gpt-4o-mini", "gpt-4.1"},
		selected: 1,
		filter:   newModelFilterInput("en"),
	}

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("expected no follow-up command, got %v", cmd)
	}
	m2 := next.(Model)
	if m2.modelPanel != nil {
		t.Fatal("expected model panel to close after selecting a model")
	}
	if got := m2.config.Model; got != "gpt-4.1" {
		t.Fatalf("config.Model = %q, want %q", got, "gpt-4.1")
	}
}

func TestProviderPanelVendorSwitchRefreshesModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/v1/models" {
			t.Fatalf("expected /v1/models, got %s", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("expected bearer auth, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"latest-model"},{"id":"fallback-model"}]}`))
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.FilePath = filepath.Join(t.TempDir(), "ggcode.yaml")
	cfg.Vendors = map[string]config.VendorConfig{
		"alpha": {
			DisplayName: "Alpha",
			Endpoints: map[string]config.EndpointConfig{
				"api": {
					DisplayName:   "Alpha API",
					Protocol:      "openai",
					BaseURL:       server.URL + "/v1",
					DefaultModel:  "alpha-default",
					SelectedModel: "alpha-default",
					Models:        []string{"alpha-default"},
				},
			},
		},
		"beta": {
			DisplayName: "Beta",
			APIKey:      "test-key",
			Endpoints: map[string]config.EndpointConfig{
				"api": {
					DisplayName:   "Beta API",
					Protocol:      "openai",
					BaseURL:       server.URL + "/v1",
					DefaultModel:  "beta-default",
					SelectedModel: "beta-default",
					Models:        []string{"beta-default"},
				},
			},
		},
	}
	cfg.Vendor = "alpha"
	cfg.Endpoint = "api"
	cfg.Model = "alpha-default"

	m := newTestModel()
	m.SetConfig(cfg)
	m.openProviderPanel()

	next, cmd := m.Update(tea.KeyPressMsg{Text: "j"})
	if cmd == nil {
		t.Fatal("expected vendor switch to trigger async model refresh")
	}
	m = next.(Model)
	if m.providerPanel == nil || !m.providerPanel.refreshing {
		t.Fatal("expected provider panel refresh state")
	}

	msg := cmd()
	next, cmd = m.Update(msg)
	if cmd != nil {
		t.Fatal("expected refresh result to update synchronously")
	}
	m2 := next.(Model)
	if got := m2.config.Vendors["beta"].Endpoints["api"].Models; len(got) < 2 || got[0] != "latest-model" {
		t.Fatalf("expected refreshed models to be stored, got %#v", got)
	}
	if m2.providerPanel == nil || !strings.Contains(m2.providerPanel.message, "Refreshed 1 endpoint") {
		t.Fatalf("expected provider refresh success message, got %+v", m2.providerPanel)
	}
}

func TestCtrlVPastesClipboardImage(t *testing.T) {
	m := newTestModel()
	m.clipboardLoader = func() (imageAttachedMsg, error) {
		img := image.Image{Data: []byte{0x89, 0x50, 0x4E, 0x47}, MIME: image.MIMEPNG, Width: 10, Height: 10}
		return imageAttachedMsg{
			placeholder: image.Placeholder("ggcode-image-deadbeef.png", img),
			img:         img,
			filename:    "ggcode-image-deadbeef.png",
		}, nil
	}

	next, cmd := m.Update(tea.KeyPressMsg{Text: "ctrl+v"})
	if cmd == nil {
		t.Fatal("expected ctrl-v to schedule clipboard image loading")
	}
	msg := cmd()
	if msg == nil {
		t.Fatal("expected clipboard load command to return a message")
	}

	next, cmd = next.(Model).Update(msg)
	if cmd != nil {
		t.Fatal("expected image attachment update to be synchronous")
	}
	m2 := next.(Model)
	if m2.pendingImage == nil {
		t.Fatal("expected clipboard image to be attached")
	}
	if m2.pendingImage.filename != "ggcode-image-deadbeef.png" {
		t.Fatalf("unexpected clipboard attachment filename: %q", m2.pendingImage.filename)
	}
	if got := m2.input.Value(); !strings.Contains(got, "ggcode-image-deadbeef.png") {
		t.Fatalf("expected image placeholder in input, got %q", got)
	}
	if strings.Contains(m2.output.String(), "ggcode-image-deadbeef.png") {
		t.Fatal("expected no attachment notice in output")
	}
}

func TestProviderPanelShowsCopilotConnectedState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := auth.DefaultStore().Save(&auth.Info{
		ProviderID:  auth.ProviderGitHubCopilot,
		Type:        "oauth",
		AccessToken: "copilot-token",
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.Vendor = auth.ProviderGitHubCopilot
	cfg.Endpoint = "github.com"
	cfg.Model = "gpt-4o"
	m.SetConfig(cfg)
	m.openProviderPanel()

	rendered := m.renderProviderPanel()
	if !strings.Contains(rendered, "connected") {
		t.Fatalf("expected rendered provider panel to show connected auth state, got %q", rendered)
	}
}

func TestProviderAuthResultUpdatesPanel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := auth.DefaultStore().Save(&auth.Info{
		ProviderID:  auth.ProviderGitHubCopilot,
		Type:        "oauth",
		AccessToken: "copilot-token",
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.Vendor = auth.ProviderGitHubCopilot
	cfg.Endpoint = "github.com"
	cfg.Model = "gpt-4o"
	m.SetConfig(cfg)
	m.openProviderPanel()
	m.providerPanel.authBusy = true

	next, cmd := m.Update(providerAuthResultMsg{
		vendor: auth.ProviderGitHubCopilot,
		info: &auth.Info{
			ProviderID:  auth.ProviderGitHubCopilot,
			Type:        "oauth",
			AccessToken: "copilot-token",
		},
	})
	if cmd == nil {
		t.Fatal("expected auth result to trigger model refresh")
	}
	updated := next.(Model)
	if updated.providerPanel == nil || updated.providerPanel.authBusy {
		t.Fatalf("expected authBusy to be cleared, got %+v", updated.providerPanel)
	}
	if !updated.providerPanel.refreshing || !strings.Contains(updated.providerPanel.message, "Refreshing models") {
		t.Fatalf("expected refresh state after auth success, got %+v", updated.providerPanel)
	}
}

func TestProviderAuthStartShowsClipboardAndBrowserNotes(t *testing.T) {
	m := newTestModel()
	m.SetConfig(config.DefaultConfig())
	m.config.Vendor = auth.ProviderGitHubCopilot
	m.config.Endpoint = "github.com"
	m.config.Model = "gpt-4o"
	m.openProviderPanel()
	next, cmd := m.Update(providerAuthStartMsg{
		vendor: auth.ProviderGitHubCopilot,
		flow: &auth.CopilotDeviceFlow{
			VerificationURI: "https://github.com/login/device",
			UserCode:        "ABCD-EFGH",
		},
	})
	if cmd == nil {
		t.Fatal("expected auth start to schedule polling")
	}
	updated := next.(Model)
	if updated.providerPanel == nil || !strings.Contains(updated.providerPanel.message, "copied to clipboard") || !strings.Contains(updated.providerPanel.message, "Opened the verification page") {
		t.Fatalf("expected copied/opened login message, got %+v", updated.providerPanel)
	}
}

func TestInspectorStatusItemsIncludeLSPInstallHintForJavaWorkspace(t *testing.T) {
	workspace := t.TempDir()
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(prevWD) }()
	if err := os.WriteFile(filepath.Join(workspace, "pom.xml"), []byte("<project/>"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Setenv("PATH", t.TempDir())

	m := newTestModel()
	items := m.inspectorStatusItems()
	found := false
	for _, item := range items {
		if item.ID != "lsp-java" {
			continue
		}
		found = true
		if !strings.Contains(item.Detail, "Install") && !strings.Contains(item.Detail, "安装") {
			t.Fatalf("expected java LSP detail to include install hint, got %#v", item)
		}
		if !strings.Contains(item.Detail, "jdtls") {
			t.Fatalf("expected java LSP detail to mention jdtls, got %#v", item)
		}
		if strings.Contains(item.Detail, "brew install jdtls") {
			t.Fatalf("expected java LSP detail to omit raw install command, got %#v", item)
		}
	}
	if !found {
		t.Fatalf("expected java LSP item in status panel, got %#v", items)
	}
}

func TestInspectorStatusEnterOpensPythonLSPInstallChooser(t *testing.T) {
	workspace := t.TempDir()
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(prevWD) }()
	if err := os.WriteFile(filepath.Join(workspace, "pyproject.toml"), []byte("[project]\nname = 'board'\nversion = '0.1.0'\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "app.py"), []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Setenv("PATH", t.TempDir())

	m := newTestModel()
	m.openInspectorPanel(inspectorPanelStatus)
	items := m.inspectorStatusItems()
	for i, item := range items {
		if item.ID == "lsp-python" {
			m.inspectorPanel.cursor = i
			break
		}
	}

	next, cmd := m.handleInspectorPanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("expected python chooser to open inline")
	}
	if next.inspectorPanel == nil || next.inspectorPanel.kind != inspectorPanelLSPInstall {
		t.Fatalf("expected LSP install chooser, got %#v", next.inspectorPanel)
	}
	if len(next.inspectorPanel.lspInstallOptions) != 2 {
		t.Fatalf("expected 2 python install options, got %#v", next.inspectorPanel.lspInstallOptions)
	}
}

func TestInspectorStatusEnterRunsSingleLSPInstallCommand(t *testing.T) {
	workspace := t.TempDir()
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(prevWD) }()
	if err := os.WriteFile(filepath.Join(workspace, "pom.xml"), []byte("<project/>"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Setenv("PATH", t.TempDir())

	m := newTestModel()
	var gotCommand string
	m.shellCommandSubmitter = func(command string, addToHistory bool) tea.Cmd {
		gotCommand = command
		return func() tea.Msg { return nil }
	}
	m.openInspectorPanel(inspectorPanelStatus)
	items := m.inspectorStatusItems()
	for i, item := range items {
		if item.ID == "lsp-java" {
			m.inspectorPanel.cursor = i
			break
		}
	}

	next, cmd := m.handleInspectorPanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected java install command")
	}
	if next.inspectorPanel != nil {
		t.Fatalf("expected status panel to close before install, got %#v", next.inspectorPanel)
	}
	if !strings.Contains(gotCommand, "jdtls") {
		t.Fatalf("expected jdtls install command, got %q", gotCommand)
	}
}

func TestInspectorLSPInstallEnterRunsSelectedCommand(t *testing.T) {
	m := newTestModel()
	var gotCommand string
	m.shellCommandSubmitter = func(command string, addToHistory bool) tea.Cmd {
		gotCommand = command
		return func() tea.Msg { return nil }
	}
	m.inspectorPanel = &inspectorPanelState{
		kind:            inspectorPanelLSPInstall,
		lspLanguageName: "Python",
		lspInstallOptions: []lsp.InstallOption{
			{ID: "pyright", Label: "pyright-langserver", Binary: "pyright-langserver", Command: "pip install pyright", Recommended: true},
			{ID: "pylsp", Label: "pylsp", Binary: "pylsp", Command: "pip install python-lsp-server"},
		},
	}

	next, cmd := m.handleInspectorPanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected selected install command")
	}
	if next.inspectorPanel != nil {
		t.Fatalf("expected chooser to close before install, got %#v", next.inspectorPanel)
	}
	if gotCommand != "pip install pyright" {
		t.Fatalf("expected pyright install command, got %q", gotCommand)
	}
}

func TestInspectorLSPInstallItemsHideRawCommands(t *testing.T) {
	m := newTestModel()
	m.inspectorPanel = &inspectorPanelState{
		kind:            inspectorPanelLSPInstall,
		lspLanguageName: "Python",
		lspInstallOptions: []lsp.InstallOption{
			{ID: "pyright", Label: "pyright-langserver", Binary: "pyright-langserver", Command: "if [ -x .venv/bin/python ]; then ...", Recommended: true},
			{ID: "pylsp", Label: "pylsp", Binary: "pylsp", Command: "pip install python-lsp-server"},
		},
	}

	items := m.inspectorLSPInstallItems()
	if len(items) != 2 {
		t.Fatalf("expected 2 install items, got %#v", items)
	}
	if strings.Contains(items[0].Detail, ".venv/bin/python") || strings.Contains(items[1].Detail, "pip install") {
		t.Fatalf("expected raw install commands to be hidden, got %#v", items)
	}
	if !strings.Contains(items[0].Detail, "pyright-langserver") {
		t.Fatalf("expected install detail to keep option label, got %#v", items[0])
	}
}

func TestCompactWorkspaceLabelForTUIShortensLongPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".openclaw", "workspace-teamclaw", "projects", "https-github-com-topcheer-cc-git-ggcode-coding-age-s7ccb6")

	got := compactWorkspaceLabelForTUI(path)
	if !strings.HasPrefix(got, "~/") {
		t.Fatalf("expected home-relative workspace label, got %q", got)
	}
	if !strings.Contains(got, "...") {
		t.Fatalf("expected long workspace label to be middle-truncated, got %q", got)
	}
	if lipgloss.Width(got) > 56 {
		t.Fatalf("expected compact workspace label <= 56 cols, got %d for %q", lipgloss.Width(got), got)
	}
}

func TestSavingEndpointAPIKeyTriggersModelRefresh(t *testing.T) {
	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.FilePath = filepath.Join(t.TempDir(), "ggcode.yaml")
	cfg.Vendor = "openai"
	cfg.Endpoint = "api"
	cfg.Model = "gpt-4o"
	m.SetConfig(cfg)
	m.openProviderPanel()
	m.providerPanel.startEditing("endpoint api key", "")
	m.providerPanel.editInput.SetValue("test-token")

	next, cmd := m.handleProviderPanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected saving endpoint api key to trigger model refresh")
	}
	updated := next
	if updated.providerPanel == nil || !updated.providerPanel.refreshing {
		t.Fatalf("expected provider panel refresh state after saving endpoint api key, got %+v", updated.providerPanel)
	}
	if updated.providerPanel.refreshVendor != "openai" {
		t.Fatalf("expected refresh vendor openai, got %+v", updated.providerPanel)
	}
}

func TestSavingEndpointBaseURLTriggersModelRefresh(t *testing.T) {
	m := newTestModel()
	cfg := config.DefaultConfig()
	cfg.FilePath = filepath.Join(t.TempDir(), "ggcode.yaml")
	cfg.Vendor = "openai"
	cfg.Endpoint = "api"
	cfg.Model = "gpt-4o"
	if err := cfg.SetEndpointAPIKey("openai", "api", "test-token", false); err != nil {
		t.Fatalf("SetEndpointAPIKey() error = %v", err)
	}
	m.SetConfig(cfg)
	m.openProviderPanel()
	m.providerPanel.startEditing("endpoint base url", "https://api.openai.com/v1")
	m.providerPanel.editInput.SetValue("https://api.openai.com/v1")

	next, cmd := m.handleProviderPanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected saving endpoint base url to trigger model refresh")
	}
	updated := next
	if updated.providerPanel == nil || !updated.providerPanel.refreshing {
		t.Fatalf("expected provider panel refresh state after saving endpoint base url, got %+v", updated.providerPanel)
	}
	if updated.providerPanel.refreshVendor != "openai" {
		t.Fatalf("expected refresh vendor openai, got %+v", updated.providerPanel)
	}
}

func TestSubmitTextStripsImagePlaceholderButKeepsImageDisplay(t *testing.T) {
	m := newTestModel()
	m.pendingImage = &imageAttachedMsg{
		placeholder: "[Image: ggcode-image-deadbeef.png, 10x10, 4B]",
		img:         image.Image{Data: []byte{0x89, 0x50, 0x4E, 0x47}, MIME: image.MIMEPNG, Width: 10, Height: 10},
		filename:    "ggcode-image-deadbeef.png",
	}

	cmd := m.submitText("[Image: ggcode-image-deadbeef.png, 10x10, 4B] 帮我看看", true)
	if cmd == nil {
		t.Fatal("expected submitText to return a command")
	}
	if len(m.history) != 1 || m.history[0] != "帮我看看" {
		t.Fatalf("expected history to contain text without placeholder, got %#v", m.history)
	}
	if !strings.Contains(m.output.String(), "[Image: ggcode-image-deadbeef.png, 10x10, 4B] 帮我看看") {
		t.Fatal("expected conversation output to show image placeholder with text")
	}
}

func TestHandleCommandShellPrefixEntersModeAndStartsCommand(t *testing.T) {
	m := newTestModel()

	cmd := m.handleCommand("$ echo hi")
	if cmd == nil {
		t.Fatal("expected prefixed shell command to start")
	}
	if !m.shellMode {
		t.Fatal("expected prefixed shell command to enable shell mode")
	}
	if !m.loading {
		t.Fatal("expected prefixed shell command to enter loading state")
	}
	if !strings.Contains(stripAnsi(m.output.String()), "$ echo hi") {
		t.Fatalf("expected shell command in output, got %q", m.output.String())
	}
}

func TestShellCommandMessagesRenderOutputAndKeepMode(t *testing.T) {
	m := newTestModel()
	m.setShellMode(true)
	if cmd := m.submitShellCommand("printf hi", true); cmd == nil {
		t.Fatal("expected shell submit to return a command")
	}

	next, cmd := m.Update(shellCommandStreamMsg{RunID: m.activeShellRunID, Text: "hi\n"})
	if cmd != nil {
		t.Fatal("expected shell stream update inline")
	}
	m = next.(Model)

	next, cmd = m.Update(shellCommandDoneMsg{RunID: m.activeShellRunID, Status: tool.CommandJobCompleted})
	if cmd != nil {
		t.Fatal("expected shell completion update inline")
	}
	m = next.(Model)

	if m.loading {
		t.Fatal("expected shell command to finish loading state")
	}
	if !m.shellMode {
		t.Fatal("expected shell mode to remain enabled after command completion")
	}
	plain := stripAnsi(m.output.String())
	if !strings.Contains(plain, "hi") {
		t.Fatalf("expected shell output in conversation, got %q", plain)
	}
	if strings.Contains(plain, "$ printf hi\n\nhi") {
		t.Fatalf("expected shell output to start immediately on the next line, got %q", plain)
	}
	if !strings.Contains(plain, "$ printf hi\nhi\n\n") {
		t.Fatalf("expected one trailing blank line after shell output, got %q", plain)
	}
}

func TestInitCommandStartsRepoKnowledgeCollection(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	subDir := filepath.Join(repoDir, "internal", "tui")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.Chdir(subDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()

	m := newTestModel()
	cmd := m.handleCommand("/init")
	if cmd == nil {
		t.Fatal("expected /init to start an async agent flow")
	}
	if !m.loading {
		t.Fatal("expected /init to enter loading state")
	}
	if m.statusActivity != "Collecting project knowledge..." {
		t.Fatalf("expected init collection activity, got %q", m.statusActivity)
	}
	if !strings.Contains(m.output.String(), "/init") {
		t.Fatalf("expected init command to appear in output, got %q", m.output.String())
	}
}

func TestInitCommandStartsEvenWhenOtherProjectMemoryExists(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.WriteFile(filepath.Join(repoDir, "AGENTS.md"), []byte("existing"), 0644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()

	m := newTestModel()
	cmd := m.handleCommand("/init")
	if cmd == nil {
		t.Fatal("expected /init to start an async agent flow")
	}
	if !m.loading {
		t.Fatal("expected /init to stay in loading state")
	}
}

func TestBuildInitPromptRequestsRepositoryInspection(t *testing.T) {
	prompt := buildInitPrompt("/tmp/repo/GGCODE.md", true, "# GGCODE.md\n\nbootstrap")
	if !strings.Contains(prompt, "inspect the repository with tools") {
		t.Fatalf("expected repo inspection requirement, got %q", prompt)
	}
	if !strings.Contains(prompt, "update the project memory file") {
		t.Fatalf("expected update action, got %q", prompt)
	}
	if !strings.Contains(prompt, "/tmp/repo/GGCODE.md") {
		t.Fatalf("expected target path in prompt, got %q", prompt)
	}
}

func TestHarnessCommandOpensPanel(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if _, err := harness.Init(repoDir, harness.InitOptions{}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	m := newTestModel()
	m.width = 120
	if cmd := m.handleCommand("/harness"); cmd != nil {
		t.Fatal("expected /harness without args to complete inline")
	}
	if m.harnessPanel == nil {
		t.Fatal("expected /harness to open harness panel")
	}
	panel := m.renderContextPanel()
	if !strings.Contains(panel, "/harness") || !strings.Contains(panel, "Check") || !strings.Contains(panel, "Monitor") || !strings.Contains(panel, "Queue") || !strings.Contains(panel, "Run queued") || !strings.Contains(panel, "Rollouts") {
		t.Fatalf("expected harness panel content, got %q", panel)
	}
}

func TestBusyEnterAllowsHarnessPanelCommand(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if _, err := harness.Init(repoDir, harness.InitOptions{}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	m := newTestModel()
	m.width = 120
	m.loading = true
	m.input.SetValue("/harness")

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("expected busy /harness to open inline")
	}
	updated := next.(Model)
	if updated.harnessPanel == nil {
		t.Fatal("expected /harness to open while busy")
	}
	if len(updated.pending.items) != 0 {
		t.Fatalf("expected /harness not to be queued, got %+v", updated.pending.items)
	}
}

func TestBusyEnterStillQueuesNonHarnessCommands(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.input.SetValue("/run_command echo hi")

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("expected busy /run_command to stay queued")
	}
	updated := next.(Model)
	if len(updated.pending.items) != 1 || updated.pending.items[0] != "/run_command echo hi" {
		t.Fatalf("expected /run_command to remain queued, got %+v", updated.pending.items)
	}
	if updated.harnessPanel != nil {
		t.Fatal("did not expect harness panel to open for /run_command")
	}
}

func TestHarnessUnavailablePanelCanInitProject(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()

	origSuggest := suggestHarnessContextsForTUI
	defer func() { suggestHarnessContextsForTUI = origSuggest }()
	suggestHarnessContextsForTUI = func(ctx context.Context, cfg *config.Config, root, goal string, elements []string) ([]harness.ContextConfig, error) {
		return []harness.ContextConfig{{Name: "core"}}, nil
	}

	m := newTestModel()
	m.width = 120
	if cmd := m.handleCommand("/harness"); cmd != nil {
		t.Fatal("expected /harness without args to complete inline")
	}
	if m.harnessPanel == nil || m.harnessPanel.loadErr == "" {
		t.Fatalf("expected unavailable harness panel, got %+v", m.harnessPanel)
	}

	next, cmd := m.handleHarnessPanelKey(tea.KeyPressMsg{Text: "i"})
	if cmd != nil {
		t.Fatal("expected panel init to open prompt first")
	}
	updated := next
	if updated.harnessContextPrompt == nil {
		t.Fatal("expected harness init prompt from panel")
	}
	nextModel, cmd := updated.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected init prompt to request suggestions")
	}
	nextModel, _ = nextModel.Update(cmd())
	updated = asModel(t, nextModel)
	nextModel, cmd = updated.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected init prompt to execute harness init")
	}
	nextModel, _ = nextModel.Update(cmd())
	updated = asModel(t, nextModel)
	if updated.harnessPanel == nil || updated.harnessPanel.loadErr != "" {
		t.Fatalf("expected initialized harness panel, got %+v", updated.harnessPanel)
	}
	for _, rel := range []string{"AGENTS.md", filepath.Join(".ggcode", "harness.yaml")} {
		if _, err := os.Stat(filepath.Join(repoDir, rel)); err != nil {
			t.Fatalf("expected %s to exist: %v", rel, err)
		}
	}
}

func TestHarnessInitCreatesScaffold(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()

	origSuggest := suggestHarnessContextsForTUI
	defer func() { suggestHarnessContextsForTUI = origSuggest }()
	suggestHarnessContextsForTUI = func(ctx context.Context, cfg *config.Config, root, goal string, elements []string) ([]harness.ContextConfig, error) {
		return []harness.ContextConfig{{Name: "core", Description: "Core platform"}}, nil
	}

	m := newTestModel()
	cmd := m.handleCommand("/harness init Build ERP system")
	if cmd != nil {
		t.Fatal("expected /harness init to open prompt first")
	}
	if m.harnessContextPrompt == nil {
		t.Fatal("expected harness init prompt to open")
	}
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected init prompt to request suggestions")
	}
	next, _ = next.Update(cmd())
	m = asModel(t, next)
	if m.harnessContextPrompt == nil || m.harnessContextPrompt.step != harnessContextPromptStepSelect {
		t.Fatalf("expected init prompt to move to select step, got %+v", m.harnessContextPrompt)
	}
	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected init selection to execute harness init")
	}
	next, _ = next.Update(cmd())
	m = asModel(t, next)
	if m.harnessContextPrompt != nil {
		t.Fatalf("expected harness init prompt to close, got %+v", m.harnessContextPrompt)
	}
	for _, rel := range []string{"AGENTS.md", filepath.Join(".ggcode", "harness.yaml")} {
		if _, err := os.Stat(filepath.Join(repoDir, rel)); err != nil {
			t.Fatalf("expected %s to exist: %v", rel, err)
		}
	}
	if !strings.Contains(m.output.String(), "Harness initialized") {
		t.Fatalf("expected harness init output, got %q", m.output.String())
	}
}

func TestHarnessInitRerunPromptsForUpgradeChoice(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if _, err := harness.Init(repoDir, harness.InitOptions{}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	origSuggest := suggestHarnessContextsForTUI
	defer func() { suggestHarnessContextsForTUI = origSuggest }()
	suggestHarnessContextsForTUI = func(ctx context.Context, cfg *config.Config, root, goal string, elements []string) ([]harness.ContextConfig, error) {
		return []harness.ContextConfig{{Name: "core"}}, nil
	}
	origInit := executeHarnessInit
	defer func() { executeHarnessInit = origInit }()
	forced := false
	executeHarnessInit = func(root string, opts harness.InitOptions) (*harness.InitResult, error) {
		forced = opts.Force
		return harness.Init(root, opts)
	}

	m := newTestModel()
	if cmd := m.handleCommand("/harness init"); cmd != nil {
		t.Fatal("expected /harness init to open prompt")
	}
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected suggestion request")
	}
	next, _ = next.Update(cmd())
	m = asModel(t, next)
	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("expected rerun init to show upgrade choice before executing")
	}
	m = asModel(t, next)
	if m.harnessContextPrompt == nil || m.harnessContextPrompt.step != harnessContextPromptStepUpgrade {
		t.Fatalf("expected upgrade step, got %+v", m.harnessContextPrompt)
	}
	next, cmd = m.Update(tea.KeyPressMsg{Text: "y"})
	if cmd != nil {
		t.Fatal("did not expect immediate execution on force toggle")
	}
	m = asModel(t, next)
	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected upgrade choice to execute init")
	}
	m = asModel(t, next)
	if m.harnessContextPrompt == nil || m.harnessContextPrompt.step != harnessContextPromptStepApplying {
		t.Fatalf("expected applying step, got %+v", m.harnessContextPrompt)
	}
	next, _ = next.Update(cmd())
	m = asModel(t, next)
	if !forced {
		t.Fatal("expected rerun init to pass Force after confirmation")
	}
}

func TestHarnessInitApplyingIgnoresCancelKey(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()

	origSuggest := suggestHarnessContextsForTUI
	defer func() { suggestHarnessContextsForTUI = origSuggest }()
	suggestHarnessContextsForTUI = func(ctx context.Context, cfg *config.Config, root, goal string, elements []string) ([]harness.ContextConfig, error) {
		return []harness.ContextConfig{{Name: "core"}}, nil
	}
	origInit := executeHarnessInit
	defer func() { executeHarnessInit = origInit }()
	executeHarnessInit = func(root string, opts harness.InitOptions) (*harness.InitResult, error) {
		return &harness.InitResult{Project: harness.Project{RootDir: root}}, nil
	}

	m := newTestModel()
	if cmd := m.handleCommand("/harness init"); cmd != nil {
		t.Fatal("expected /harness init to open prompt")
	}
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	next, _ = next.Update(cmd())
	m = asModel(t, next)
	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected init execution")
	}
	m = asModel(t, next)
	if m.harnessContextPrompt == nil || m.harnessContextPrompt.step != harnessContextPromptStepApplying {
		t.Fatalf("expected applying step, got %+v", m.harnessContextPrompt)
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = asModel(t, next)
	if m.harnessContextPrompt == nil || m.harnessContextPrompt.step != harnessContextPromptStepApplying {
		t.Fatalf("expected applying step to ignore cancel, got %+v", m.harnessContextPrompt)
	}
}

func TestHarnessContextsShowsReport(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.MkdirAll(filepath.Join(repoDir, "internal", "inventory"), 0755); err != nil {
		t.Fatalf("mkdir inventory: %v", err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if _, err := harness.Init(repoDir, harness.InitOptions{}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	m := newTestModel()
	cmd := m.handleCommand("/harness contexts")
	if cmd != nil {
		t.Fatal("expected /harness contexts to complete inline")
	}
	if !strings.Contains(m.output.String(), "Harness contexts:") {
		t.Fatalf("expected contexts output, got %q", m.output.String())
	}
}

func TestHarnessMonitorShowsSnapshotReport(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	result, err := harness.Init(repoDir, harness.InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	task, err := harness.NewTask("Monitor inventory context", "tui")
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	task.Status = harness.TaskRunning
	task.WorkerID = "worker-1"
	task.WorkerStatus = "running"
	if err := harness.SaveTask(result.Project, task); err != nil {
		t.Fatalf("SaveTask() error = %v", err)
	}

	m := newTestModel()
	cmd := m.handleCommand("/harness monitor")
	if cmd != nil {
		t.Fatal("expected /harness monitor to complete inline")
	}
	if !strings.Contains(m.output.String(), "Harness monitor") || !strings.Contains(m.output.String(), task.ID) {
		t.Fatalf("expected monitor output, got %q", m.output.String())
	}
}

func TestHarnessRunCommandCreatesTrackedTask(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	result, err := harness.Init(repoDir, harness.InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	origRun := executeHarnessRun
	defer func() { executeHarnessRun = origRun }()
	executeHarnessRun = func(ctx context.Context, project harness.Project, cfg *harness.Config, goal string, opts harness.RunTaskOptions) (*harness.RunSummary, error) {
		if opts.ContextName != "core" {
			t.Fatalf("expected run context core, got %+v", opts)
		}
		task, err := harness.EnqueueTask(project, goal, "tui")
		if err != nil {
			return nil, err
		}
		task.Status = harness.TaskCompleted
		task.ReviewStatus = harness.ReviewPending
		if err := harness.SaveTask(project, task); err != nil {
			return nil, err
		}
		return &harness.RunSummary{Task: task, Result: &harness.RunResult{Output: "ok"}}, nil
	}

	m := newTestModel()
	cmd := m.handleCommand("/harness run Build ERP backend")
	if cmd != nil {
		t.Fatal("expected /harness run to open context prompt first")
	}
	if m.harnessContextPrompt == nil {
		t.Fatal("expected harness run prompt to open")
	}
	m.harnessContextPrompt.input.SetValue("core")
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("expected custom run context to ask for persistence before starting")
	}
	m = asModel(t, next)
	if m.harnessContextPrompt == nil || m.harnessContextPrompt.step != harnessContextPromptStepPersist {
		t.Fatalf("expected persist prompt, got %+v", m.harnessContextPrompt)
	}
	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected /harness run to start after confirming context persistence")
	}
	m = asModel(t, next)
	if !m.loading {
		t.Fatal("expected harness run to set loading state")
	}
	if !strings.Contains(m.statusActivity, "Starting harness run") {
		t.Fatalf("expected harness run start activity, got %q", m.statusActivity)
	}
	next, followup := m.Update(cmd())
	if followup != nil {
		t.Fatal("expected harness run result to update synchronously")
	}
	m = next.(Model)
	tasks, err := harness.ListTasks(result.Project)
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(tasks) != 1 || tasks[0].Goal != "Build ERP backend" {
		t.Fatalf("expected tracked task from /harness run, got %+v", tasks)
	}
	if !strings.Contains(m.output.String(), "Starting tracked harness run") || !strings.Contains(m.output.String(), "Harness run") {
		t.Fatalf("expected harness run summary output, got %q", m.output.String())
	}
}

func TestBusyHarnessPanelBlocksRunAction(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if _, err := harness.Init(repoDir, harness.InitOptions{}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	m := newTestModel()
	m.loading = true
	m.openHarnessPanel()
	m.harnessPanel.selectedSection = harnessSectionRun
	m.updateHarnessPanelInputState()
	m.harnessPanel.actionInput.SetValue("Ship billing")
	m.harnessPanel.focus = harnessPanelFocusInput

	next, cmd := m.handleHarnessPanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("expected busy panel run action to be blocked")
	}
	updated := next
	if !strings.Contains(updated.harnessPanel.message, "read-only") {
		t.Fatalf("expected busy panel message, got %+v", updated.harnessPanel.message)
	}
	if updated.harnessContextPrompt != nil {
		t.Fatalf("did not expect run prompt while busy, got %+v", updated.harnessContextPrompt)
	}
}

func TestBusyHarnessPanelAllowsQueueAction(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	result, err := harness.Init(repoDir, harness.InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	m := newTestModel()
	m.loading = true
	m.openHarnessPanel()
	m.harnessPanel.project = &result.Project
	m.harnessPanel.selectedSection = harnessSectionQueue
	m.updateHarnessPanelInputState()
	m.harnessPanel.actionInput.SetValue("Queue inventory reconciliation work")
	m.harnessPanel.focus = harnessPanelFocusInput

	next, cmd := m.handleHarnessPanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("expected busy panel queue action to complete synchronously")
	}
	updated := next
	if updated.harnessPanel == nil || !strings.Contains(updated.harnessPanel.message, "Queued harness task") {
		t.Fatalf("expected queue success message, got %+v", updated.harnessPanel)
	}
	tasks, err := harness.ListTasks(result.Project)
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(tasks) != 1 || tasks[0].Goal != "Queue inventory reconciliation work" {
		t.Fatalf("expected queued task during busy run, got %+v", tasks)
	}
}

func TestHarnessRerunCommandCreatesTrackedTask(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	result, err := harness.Init(repoDir, harness.InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	task, err := harness.EnqueueTask(result.Project, "Retry ERP backend", "tui")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}
	task.Status = harness.TaskFailed
	task.Attempt = 1
	if err := harness.SaveTask(result.Project, task); err != nil {
		t.Fatalf("SaveTask() error = %v", err)
	}
	origRerun := executeHarnessRerun
	defer func() { executeHarnessRerun = origRerun }()
	executeHarnessRerun = func(ctx context.Context, project harness.Project, cfg *harness.Config, taskID string, opts harness.RunTaskOptions) (*harness.RunSummary, error) {
		reloaded, err := harness.LoadTask(project, taskID)
		if err != nil {
			return nil, err
		}
		reloaded.Status = harness.TaskCompleted
		reloaded.Attempt = 2
		reloaded.ReviewStatus = harness.ReviewPending
		if err := harness.SaveTask(project, reloaded); err != nil {
			return nil, err
		}
		return &harness.RunSummary{Task: reloaded, Result: &harness.RunResult{Output: "ok"}}, nil
	}

	m := newTestModel()
	cmd := m.handleCommand("/harness rerun " + task.ID)
	if cmd == nil {
		t.Fatal("expected /harness rerun to start asynchronously")
	}
	if !m.loading {
		t.Fatal("expected harness rerun to set loading state")
	}
	if !strings.Contains(m.statusActivity, "Starting harness rerun") {
		t.Fatalf("expected harness rerun start activity, got %q", m.statusActivity)
	}
	next, followup := m.Update(cmd())
	if followup != nil {
		t.Fatal("expected harness rerun result to update synchronously")
	}
	m = next.(Model)
	reloaded, err := harness.LoadTask(result.Project, task.ID)
	if err != nil {
		t.Fatalf("LoadTask() error = %v", err)
	}
	if reloaded.Status != harness.TaskCompleted || reloaded.Attempt != 2 {
		t.Fatalf("expected rerun task completion, got %+v", reloaded)
	}
	if !strings.Contains(m.output.String(), "Starting tracked harness rerun") || !strings.Contains(m.output.String(), "Harness run") {
		t.Fatalf("expected harness rerun summary output, got %q", m.output.String())
	}
}

func TestHarnessPanelApprovesReviewTask(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	result, err := harness.Init(repoDir, harness.InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	task, err := harness.NewTask("Review inventory slice", "tui")
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	task.Status = harness.TaskCompleted
	task.VerificationStatus = harness.VerificationPassed
	if err := harness.SaveTask(result.Project, task); err != nil {
		t.Fatalf("SaveTask() error = %v", err)
	}

	m := newTestModel()
	m.openHarnessPanel()
	if m.harnessPanel == nil {
		t.Fatal("expected harness panel to open")
	}
	m.harnessPanel.selectedSection = harnessSectionReview
	m.harnessPanel.focus = harnessPanelFocusItem
	m.syncHarnessPanelSelection()

	next, cmd := m.handleHarnessPanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("expected review approval to complete inline")
	}
	updated := next
	loaded, err := harness.LoadTask(result.Project, task.ID)
	if err != nil {
		t.Fatalf("LoadTask() error = %v", err)
	}
	if loaded.ReviewStatus != harness.ReviewApproved {
		t.Fatalf("expected review approved, got %q", loaded.ReviewStatus)
	}
	if updated.harnessPanel == nil || !strings.Contains(updated.harnessPanel.message, task.ID) {
		t.Fatalf("expected success message for approved task, got %+v", updated.harnessPanel)
	}
}

func TestHarnessPanelQueuesInputDraft(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	result, err := harness.Init(repoDir, harness.InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	m := newTestModel()
	m.openHarnessPanel()
	m.harnessPanel.selectedSection = harnessSectionQueue
	m.updateHarnessPanelInputState()
	m.harnessPanel.actionInput.SetValue("Queue inventory reconciliation work")

	next, cmd := m.handleHarnessPanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("expected queue action to complete inline")
	}
	updated := next
	tasks, err := harness.ListTasks(result.Project)
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(tasks) != 1 || tasks[0].Goal != "Queue inventory reconciliation work" {
		t.Fatalf("expected queued task from input draft, got %+v", tasks)
	}
	if updated.harnessPanel == nil || !strings.Contains(updated.harnessPanel.message, "Queued harness task") {
		t.Fatalf("expected queue success message, got %+v", updated.harnessPanel)
	}
}

func TestHarnessPanelShowsDedicatedInputForQueueAndRun(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if _, err := harness.Init(repoDir, harness.InitOptions{}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	m := newTestModel()
	m.width = 120
	m.openHarnessPanel()
	m.harnessPanel.selectedSection = harnessSectionQueue
	m.updateHarnessPanelInputState()
	queuePanel := m.renderContextPanel()
	if !strings.Contains(queuePanel, "Action") || !strings.Contains(queuePanel, "queued harness goal") || !strings.Contains(queuePanel, "Details") {
		t.Fatalf("expected queue input guidance, got %q", queuePanel)
	}

	m.harnessPanel.selectedSection = harnessSectionRun
	m.updateHarnessPanelInputState()
	runPanel := m.renderContextPanel()
	if !strings.Contains(runPanel, "Action") || !strings.Contains(runPanel, "harness run goal") || !strings.Contains(runPanel, "Details") {
		t.Fatalf("expected run input guidance, got %q", runPanel)
	}
}

func TestHarnessPanelRunClosesPanelAndCreatesTrackedTask(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.MkdirAll(filepath.Join(repoDir, "internal", "inventory"), 0755); err != nil {
		t.Fatalf("mkdir inventory: %v", err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if _, err := harness.Init(repoDir, harness.InitOptions{}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	origRun := executeHarnessRun
	defer func() { executeHarnessRun = origRun }()
	executeHarnessRun = func(ctx context.Context, project harness.Project, cfg *harness.Config, goal string, opts harness.RunTaskOptions) (*harness.RunSummary, error) {
		if strings.TrimSpace(opts.ContextName) == "" {
			t.Fatalf("expected panel run to pass context, got %+v", opts)
		}
		task, err := harness.EnqueueTask(project, goal, "tui")
		if err != nil {
			return nil, err
		}
		task.Status = harness.TaskCompleted
		task.ReviewStatus = harness.ReviewPending
		if err := harness.SaveTask(project, task); err != nil {
			return nil, err
		}
		return &harness.RunSummary{Task: task, Result: &harness.RunResult{Output: "ok"}}, nil
	}

	m := newTestModel()
	m.openHarnessPanel()
	m.harnessPanel.selectedSection = harnessSectionRun
	m.updateHarnessPanelInputState()
	m.harnessPanel.actionInput.SetValue("Fix inventory sync failures")

	next, cmd := m.handleHarnessPanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("expected run panel to open context prompt first")
	}
	updated := next
	if updated.harnessContextPrompt == nil {
		t.Fatal("expected harness context prompt from panel run")
	}
	nextModel, cmd := updated.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected tracked harness run to start after context selection")
	}
	updated = asModel(t, nextModel)
	if updated.harnessPanel != nil {
		t.Fatalf("expected harness panel to close after run, got %+v", updated.harnessPanel)
	}
	if !updated.loading {
		t.Fatal("expected tracked harness run to enter loading state")
	}
	if updated.streamBuffer == nil {
		t.Fatal("expected harness run to initialize streaming buffer")
	}
	nextModel, followup := updated.Update(cmd())
	if followup != nil {
		t.Fatal("expected harness run result update to complete synchronously")
	}
	updated = nextModel.(Model)
	tasks, err := harness.ListTasks(harness.Project{
		RootDir:      repoDir,
		ConfigPath:   filepath.Join(repoDir, ".ggcode", "harness.yaml"),
		StateDir:     filepath.Join(repoDir, ".ggcode", "harness"),
		TasksDir:     filepath.Join(repoDir, ".ggcode", "harness", "tasks"),
		LogsDir:      filepath.Join(repoDir, ".ggcode", "harness", "logs"),
		ArchiveDir:   filepath.Join(repoDir, ".ggcode", "harness", "archive"),
		WorktreesDir: filepath.Join(repoDir, ".ggcode", "harness", "worktrees"),
		EventLogPath: filepath.Join(repoDir, ".ggcode", "harness", "events.jsonl"),
		SnapshotPath: filepath.Join(repoDir, ".ggcode", "harness", "snapshot.db"),
	})
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(tasks) != 1 || tasks[0].Goal != "Fix inventory sync failures" {
		t.Fatalf("expected tracked harness task, got %+v", tasks)
	}
	if !strings.Contains(updated.output.String(), "Starting tracked harness run") || !strings.Contains(updated.output.String(), "Harness run") {
		t.Fatalf("expected harness run summary in output, got %q", updated.output.String())
	}
}

func TestHarnessRunProgressStreamsTrackedLogIntoConversation(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.harnessRunProject = &harness.Project{}
	m.streamBuffer = &bytes.Buffer{}

	next, cmd := m.Update(harnessRunProgressMsg{
		TaskID:    "task-1",
		Activity:  "Harness running • task-1",
		Detail:    "running read_file README.md",
		LogPath:   "/tmp/task-1.log",
		LogChunk:  "Drafting inventory sync patch",
		LogOffset: int64(len("Drafting inventory sync patch")),
	})
	if cmd == nil {
		t.Fatal("expected follow-up poll command")
	}
	updated := next.(Model)
	if !strings.Contains(updated.output.String(), "Drafting inventory sync patch") {
		t.Fatalf("expected streamed harness log output, got %q", updated.output.String())
	}
	if !strings.Contains(updated.output.String(), "📖 Read file · README.md") {
		t.Fatalf("expected deduplicated harness progress detail in output, got %q", updated.output.String())
	}
	if updated.streamBuffer == nil || !strings.Contains(updated.streamBuffer.String(), "📖 Read file · README.md") {
		t.Fatalf("expected harness stream buffer to capture formatted progress detail, got %+v", updated.streamBuffer)
	}
	if updated.harnessRunLiveTail != "Drafting inventory sync patch" {
		t.Fatalf("expected partial harness output to stay visible as live tail, got %q", updated.harnessRunLiveTail)
	}
	if updated.harnessRunTaskID != "task-1" || updated.harnessRunLogPath != "/tmp/task-1.log" {
		t.Fatalf("expected harness run state to track task/log path, got task=%q log=%q", updated.harnessRunTaskID, updated.harnessRunLogPath)
	}
}

func TestHarnessRunProgressDeduplicatesMainPanelDetail(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.harnessRunProject = &harness.Project{}
	m.streamBuffer = &bytes.Buffer{}

	next, _ := m.Update(harnessRunProgressMsg{Detail: "✅ Result · worktree"})
	updated := next.(Model)
	next, _ = updated.Update(harnessRunProgressMsg{Detail: "✅ Result · worktree"})
	updated = next.(Model)
	if strings.Count(updated.output.String(), "✅ Result · worktree") != 1 {
		t.Fatalf("expected harness progress detail to be deduplicated, got %q", updated.output.String())
	}
}

func TestHarnessRunProgressDoesNotReplayDetailAlreadyPresentInLogChunk(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.harnessRunProject = &harness.Project{}
	m.streamBuffer = &bytes.Buffer{}

	next, _ := m.Update(harnessRunProgressMsg{
		Detail:   "running run_command cd client && npm run build 2>&1",
		LogChunk: "tool: run_command cd client && npm run build 2>&1\n",
	})
	updated := next.(Model)
	if strings.Count(updated.output.String(), "⚙️ Run command · cd client && npm run build 2>&1") != 1 {
		t.Fatalf("expected harness detail to render once, got %q", updated.output.String())
	}
}

func TestFormatHarnessRunLogChunkPrettifiesToolCallsAndPaths(t *testing.T) {
	project := &harness.Project{RootDir: "/repo"}
	chunk := "tool: read_file /repo/docs/runbooks/harness.md\n" +
		"tool result: Successfully wrote 299 bytes to /repo/internal/tui/model.go\n" +
		"Working through remaining diagnostics\n"
	rendered := formatHarnessRunLogChunk(LangEnglish, project, chunk)
	if !strings.Contains(rendered, "📖 Read file · docs/runbooks/harness.md") {
		t.Fatalf("expected pretty tool line, got %q", rendered)
	}
	if !strings.Contains(rendered, "✅ wrote 299 bytes to internal/tui/model.go") {
		t.Fatalf("expected shortened result path, got %q", rendered)
	}
	if !strings.Contains(rendered, "Working through remaining diagnostics") {
		t.Fatalf("expected plain prose to remain, got %q", rendered)
	}
}

func TestFormatHarnessRunLogChunkLocalizesToolLabels(t *testing.T) {
	project := &harness.Project{RootDir: "/repo"}
	chunk := "tool: read_file /repo/docs/runbooks/harness.md\n"
	rendered := formatHarnessRunLogChunk(LangZhCN, project, chunk)
	if !strings.Contains(rendered, "📖 读取文件 · docs/runbooks/harness.md") {
		t.Fatalf("expected localized tool label, got %q", rendered)
	}
}

func TestHarnessRunResultDoesNotRedumpStreamedOutput(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.harnessRunLogOffset = 7
	m.streamBuffer = &bytes.Buffer{}
	m.streamBuffer.WriteString("working")
	m.streamStartPos = m.output.Len()
	m.streamPrefixWritten = true
	m.output.WriteString("● working")

	next, cmd := m.Update(harnessRunResultMsg{
		Summary: &harness.RunSummary{
			Task: &harness.Task{
				ID:                 "task-1",
				Status:             harness.TaskCompleted,
				ReviewStatus:       harness.ReviewPending,
				VerificationStatus: harness.VerificationPassed,
			},
			Result: &harness.RunResult{Output: "working"},
		},
	})
	if cmd != nil {
		t.Fatal("expected harness result not to schedule extra work")
	}
	updated := next.(Model)
	if strings.Contains(updated.output.String(), "\nOutput:\n") {
		t.Fatalf("expected streamed harness result to suppress bulk Output section, got %q", updated.output.String())
	}
	if strings.Count(updated.output.String(), "working") != 1 {
		t.Fatalf("expected streamed output to appear once, got %q", updated.output.String())
	}
}

func TestHarnessPanelUsesTwoColumnLayoutAcrossSections(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if _, err := harness.Init(repoDir, harness.InitOptions{}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	m := newTestModel()
	m.width = 140
	m.openHarnessPanel()

	m.harnessPanel.selectedSection = harnessSectionDoctor
	m.updateHarnessPanelInputState()
	doctorPanel := m.renderContextPanel()
	if !strings.Contains(doctorPanel, "Views") || !strings.Contains(doctorPanel, "Action") || !strings.Contains(doctorPanel, "Details") {
		t.Fatalf("expected doctor panel to render navigation and main content sections, got %q", doctorPanel)
	}
	if strings.Contains(doctorPanel, "Selection") || strings.Contains(doctorPanel, "Action input") || strings.Contains(doctorPanel, "Preview") {
		t.Fatalf("expected redesigned doctor panel to avoid the old stacked subsections, got %q", doctorPanel)
	}

	m.harnessPanel.selectedSection = harnessSectionQueue
	m.updateHarnessPanelInputState()
	queuePanel := m.renderContextPanel()
	if !strings.Contains(queuePanel, "Views") || !strings.Contains(queuePanel, "Action") || !strings.Contains(queuePanel, "Details") {
		t.Fatalf("expected queue panel to render navigation and main content sections, got %q", queuePanel)
	}
}

func TestHarnessPanelUsesCompactLeftColumn(t *testing.T) {
	m := newTestModel()
	m.width = 140
	m.harnessPanel = &harnessPanelState{}

	leftWidth := m.harnessPanelLeftWidth(m.boxInnerWidth(m.mainColumnWidth()))
	if leftWidth > 24 {
		t.Fatalf("expected compact left column, got width %d", leftWidth)
	}
	if leftWidth < len("Run queued")+4 {
		t.Fatalf("expected left column to fit command labels, got width %d", leftWidth)
	}
}

func TestHarnessDoctorPanelUsesCompactPaths(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if _, err := harness.Init(repoDir, harness.InitOptions{}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	m := newTestModel()
	m.width = 140
	m.openHarnessPanel()
	m.harnessPanel.selectedSection = harnessSectionDoctor
	m.updateHarnessPanelInputState()

	panel := m.renderContextPanel()
	if strings.Contains(panel, repoDir) {
		t.Fatalf("expected doctor panel to avoid full repo path, got %q", panel)
	}
	if !strings.Contains(panel, "config: .ggcode/harness.yaml") {
		t.Fatalf("expected doctor panel to show relative harness config path, got %q", panel)
	}
	if !strings.Contains(panel, "repo: "+filepath.Base(repoDir)) {
		t.Fatalf("expected doctor panel to show repo basename, got %q", panel)
	}
}

func TestHarnessPanelLocalizesZhNavigation(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if _, err := harness.Init(repoDir, harness.InitOptions{}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	m := newTestModel()
	m.setLanguage(string(LangZhCN))
	m.openHarnessPanel()
	m.harnessPanel.selectedSection = harnessSectionQueue
	m.updateHarnessPanelInputState()

	panel := m.renderContextPanel()
	if !strings.Contains(panel, "视图") || !strings.Contains(panel, "排队") || !strings.Contains(panel, "操作") || !strings.Contains(panel, "详情") {
		t.Fatalf("expected localized harness panel chrome, got %q", panel)
	}
}

func TestHarnessPanelQueueMessageLocalizesToChinese(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	result, err := harness.Init(repoDir, harness.InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	m := newTestModel()
	m.setLanguage(string(LangZhCN))
	m.openHarnessPanel()
	m.harnessPanel.project = &result.Project
	m.harnessPanel.selectedSection = harnessSectionQueue
	m.updateHarnessPanelInputState()
	m.harnessPanel.actionInput.SetValue("整理库存对账流程")
	m.harnessPanel.focus = harnessPanelFocusInput

	next, cmd := m.handleHarnessPanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("expected queue action to complete inline")
	}
	updated := next
	if updated.harnessPanel == nil || !strings.Contains(updated.harnessPanel.message, "已加入 harness 队列") {
		t.Fatalf("expected localized queue success message, got %+v", updated.harnessPanel)
	}
}

func TestWrapHarnessPanelTextHardWrapsLongTokens(t *testing.T) {
	lines := wrapHarnessPanelText("goal: /very/long/path/without/any/spaces/that/used/to/overflow/the/right/panel", 20, 10)
	if len(lines) < 2 {
		t.Fatalf("expected long token to wrap, got %+v", lines)
	}
	for _, line := range lines {
		if lipgloss.Width(line) > 20 {
			t.Fatalf("expected wrapped line width <= 20, got %d for %q", lipgloss.Width(line), line)
		}
	}
}

func TestHarnessTasksPanelClipsDetailsToRightColumn(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	result, err := harness.Init(repoDir, harness.InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	task, err := harness.EnqueueTask(result.Project, "Fix /very/long/path/without/any/spaces/that/previously/overflowed/the/tasks/panel/when/rendered/in/details", "tui")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}
	task.Attempt = 1
	task.Status = harness.TaskFailed
	task.WorkspacePath = filepath.Join(result.Project.WorktreesDir, task.ID)
	task.BranchName = "harness-" + task.ID
	task.WorkerID = "sa-1"
	task.WorkerStatus = "completed"
	task.WorkerProgress = "worker failed with exit code 1"
	task.VerificationStatus = "skipped"
	task.LogPath = filepath.Join(result.Project.LogsDir, "nested", "path", "with", "many", "segments", "task.log")
	task.VerificationReportPath = filepath.Join(result.Project.LogsDir, task.ID+"-delivery.json")
	task.Error = "ggcode exited with code 1"
	if err := harness.SaveTask(result.Project, task); err != nil {
		t.Fatalf("SaveTask() error = %v", err)
	}

	m := newTestModel()
	m.width = 100
	m.openHarnessPanel()
	m.harnessPanel.selectedSection = harnessSectionTasks
	m.harnessPanel.selectedItem = 0
	m.updateHarnessPanelInputState()

	totalWidth := m.boxInnerWidth(m.mainColumnWidth())
	leftWidth := m.harnessPanelLeftWidth(totalWidth)
	rightWidth := max(1, totalWidth-leftWidth-2)
	rightLines := m.renderHarnessPanelMainLines(rightWidth, 20)
	for _, line := range rightLines {
		if lipgloss.Width(line) > rightWidth {
			t.Fatalf("expected right column width <= %d, got %d for %q", rightWidth, lipgloss.Width(line), line)
		}
	}
	panel := strings.Join(rightLines, "\n")
	if strings.Contains(panel, repoDir) {
		t.Fatalf("expected task details to avoid full repo path, got %q", panel)
	}
	if !strings.Contains(panel, "goal:") || !strings.Contains(panel, "workspace:") {
		t.Fatalf("expected structured task details, got %q", panel)
	}
}

func TestHarnessTasksPanelAutoRefreshesActiveTask(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	result, err := harness.Init(repoDir, harness.InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	task, err := harness.EnqueueTask(result.Project, "Watch this active task refresh itself", "tui")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}
	task.Status = harness.TaskRunning
	task.WorkerID = "sa-1"
	task.WorkerStatus = "running"
	task.WorkerProgress = "tool: read_file README.md"
	if err := harness.SaveTask(result.Project, task); err != nil {
		t.Fatalf("SaveTask() error = %v", err)
	}

	m := newTestModel()
	m.openHarnessPanel()
	m.harnessPanel.selectedSection = harnessSectionTasks
	m.harnessPanel.selectedItem = 0
	m.updateHarnessPanelInputState()
	m.syncHarnessPanelSelection()
	if !m.shouldAutoRefreshHarnessTask() {
		t.Fatal("expected running selected task to enable auto refresh")
	}

	task.Status = harness.TaskCompleted
	task.WorkerStatus = "completed"
	task.WorkerProgress = "done"
	if err := harness.SaveTask(result.Project, task); err != nil {
		t.Fatalf("SaveTask() error = %v", err)
	}

	next, cmd := m.Update(harnessPanelAutoRefreshMsg{})
	updated := next.(Model)
	if updated.harnessPanel == nil || len(updated.harnessPanel.tasks) == 0 {
		t.Fatal("expected harness panel tasks to remain loaded")
	}
	if updated.harnessPanel.tasks[0].Status != harness.TaskCompleted {
		t.Fatalf("expected auto refresh to load completed status, got %s", updated.harnessPanel.tasks[0].Status)
	}
	if cmd != nil {
		t.Fatal("expected auto refresh polling to stop once the task is no longer active")
	}
}

func TestHarnessTasksPanelAutoRefreshSkipsInactiveSelection(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	result, err := harness.Init(repoDir, harness.InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	task, err := harness.EnqueueTask(result.Project, "Inactive task should not poll", "tui")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}
	task.Status = harness.TaskFailed
	if err := harness.SaveTask(result.Project, task); err != nil {
		t.Fatalf("SaveTask() error = %v", err)
	}

	m := newTestModel()
	m.openHarnessPanel()
	m.harnessPanel.selectedSection = harnessSectionTasks
	m.harnessPanel.selectedItem = 0
	m.updateHarnessPanelInputState()
	m.syncHarnessPanelSelection()

	if m.shouldAutoRefreshHarnessTask() {
		t.Fatal("expected failed task selection not to auto refresh")
	}
	if cmd := m.pollHarnessPanelAutoRefresh(); cmd != nil {
		t.Fatal("expected no polling command for inactive task selection")
	}
}

func TestHarnessTasksPanelEnterRerunsFailedTask(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	result, err := harness.Init(repoDir, harness.InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	task, err := harness.EnqueueTask(result.Project, "Retry failed panel task", "tui")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}
	task.Status = harness.TaskFailed
	task.Attempt = 1
	if err := harness.SaveTask(result.Project, task); err != nil {
		t.Fatalf("SaveTask() error = %v", err)
	}
	origRerun := executeHarnessRerun
	defer func() { executeHarnessRerun = origRerun }()
	executeHarnessRerun = func(ctx context.Context, project harness.Project, cfg *harness.Config, taskID string, opts harness.RunTaskOptions) (*harness.RunSummary, error) {
		reloaded, err := harness.LoadTask(project, taskID)
		if err != nil {
			return nil, err
		}
		reloaded.Status = harness.TaskCompleted
		reloaded.Attempt = 2
		reloaded.ReviewStatus = harness.ReviewPending
		if err := harness.SaveTask(project, reloaded); err != nil {
			return nil, err
		}
		return &harness.RunSummary{Task: reloaded, Result: &harness.RunResult{Output: "ok"}}, nil
	}

	m := newTestModel()
	m.openHarnessPanel()
	m.harnessPanel.selectedSection = harnessSectionTasks
	m.harnessPanel.focus = harnessPanelFocusItem
	m.harnessPanel.selectedItem = 0
	m.updateHarnessPanelInputState()
	m.syncHarnessPanelSelection()

	next, cmd := m.handleHarnessPanelKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected Enter on failed task to start rerun")
	}
	updated := next
	if updated.harnessPanel != nil {
		t.Fatal("expected harness panel to close when launching tracked rerun")
	}
	if !updated.loading {
		t.Fatal("expected tracked rerun to set loading state")
	}
	nextModel, followup := updated.Update(cmd())
	if followup != nil {
		t.Fatal("expected harness rerun result to update synchronously")
	}
	updated = nextModel.(Model)
	reloaded, err := harness.LoadTask(result.Project, task.ID)
	if err != nil {
		t.Fatalf("LoadTask() error = %v", err)
	}
	if reloaded.Status != harness.TaskCompleted || reloaded.Attempt != 2 {
		t.Fatalf("expected rerun task completion, got %+v", reloaded)
	}
	if !strings.Contains(updated.output.String(), "/harness rerun "+task.ID) {
		t.Fatalf("expected rerun command in output, got %q", updated.output.String())
	}
}

func TestHarnessMonitorPanelUsesCompactPaths(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if _, err := harness.Init(repoDir, harness.InitOptions{}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	m := newTestModel()
	m.width = 140
	m.openHarnessPanel()
	m.harnessPanel.selectedSection = harnessSectionMonitor
	m.updateHarnessPanelInputState()

	panel := m.renderContextPanel()
	if strings.Contains(panel, repoDir) {
		t.Fatalf("expected monitor panel to avoid full repo path, got %q", panel)
	}
	if !strings.Contains(panel, "snapshot: .ggcode/harness/snapshot.db") {
		t.Fatalf("expected monitor panel to show relative snapshot path, got %q", panel)
	}
	if !strings.Contains(panel, "events: .ggcode/harness/events.jsonl") {
		t.Fatalf("expected monitor panel to show relative event log path, got %q", panel)
	}
}

func TestHarnessPanelHintsRenderInFooter(t *testing.T) {
	m := newTestModel()
	m.width = 140
	m.harnessPanel = &harnessPanelState{
		selectedSection: harnessSectionRollouts,
	}

	rightLines := m.renderHarnessPanelMainLines(80, 26)
	for _, line := range rightLines {
		if strings.Contains(line, "j/k move") {
			t.Fatalf("expected shortcut hints to stay out of the main content pane, got %q", line)
		}
	}

	footer := m.renderHarnessPanelFooterLines(100)
	if len(footer) == 0 || !strings.Contains(footer[len(footer)-1], "j/k move") {
		t.Fatalf("expected footer to carry shortcut hints, got %+v", footer)
	}
}

func TestHarnessReleaseRolloutsShowsPersistedWaves(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	for _, rel := range []string{
		filepath.Join(repoDir, "internal", "inventory"),
		filepath.Join(repoDir, "internal", "pricing"),
	} {
		if err := os.MkdirAll(rel, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	result, err := harness.Init(repoDir, harness.InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for i := range result.Config.Contexts {
		switch result.Config.Contexts[i].Path {
		case filepath.Join("internal", "inventory"):
			result.Config.Contexts[i].Owner = "inventory-team"
		case filepath.Join("internal", "pricing"):
			result.Config.Contexts[i].Owner = "pricing-team"
		}
	}
	for _, item := range []string{filepath.Join("internal", "inventory"), filepath.Join("internal", "pricing")} {
		task, err := harness.NewTask("Ship "+item, "tui")
		if err != nil {
			t.Fatalf("NewTask() error = %v", err)
		}
		task.ContextPath = item
		task.ContextName = strings.ReplaceAll(item, string(filepath.Separator), "-")
		task.Status = harness.TaskCompleted
		task.VerificationStatus = harness.VerificationPassed
		task.ReviewStatus = harness.ReviewApproved
		task.PromotionStatus = harness.PromotionApplied
		data, err := json.MarshalIndent(task, "", "  ")
		if err != nil {
			t.Fatalf("marshal task: %v", err)
		}
		if err := os.WriteFile(filepath.Join(result.Project.TasksDir, task.ID+".json"), data, 0644); err != nil {
			t.Fatalf("write task: %v", err)
		}
	}
	waves, err := harness.BuildReleaseWavePlan(result.Project, result.Config, harness.ReleasePlanOptions{}, harness.ReleaseGroupByOwner)
	if err != nil {
		t.Fatalf("BuildReleaseWavePlan() error = %v", err)
	}
	if _, err := harness.ApplyReleaseWavePlan(result.Project, waves, "", "rollout-tui"); err != nil {
		t.Fatalf("ApplyReleaseWavePlan() error = %v", err)
	}
	m := newTestModel()
	cmd := m.handleCommand("/harness release rollouts")
	if cmd != nil {
		t.Fatal("expected /harness release rollouts to complete inline")
	}
	if !strings.Contains(m.output.String(), "Harness release rollouts") || !strings.Contains(m.output.String(), "rollout=rollout-tui") {
		t.Fatalf("expected rollouts output, got %q", m.output.String())
	}
}

func TestHarnessReleaseRolloutControls(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	gitInitForTUI(t, repoDir)
	for _, rel := range []string{
		filepath.Join(repoDir, "internal", "inventory"),
		filepath.Join(repoDir, "internal", "pricing"),
	} {
		if err := os.MkdirAll(rel, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	result, err := harness.Init(repoDir, harness.InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for i := range result.Config.Contexts {
		switch result.Config.Contexts[i].Path {
		case filepath.Join("internal", "inventory"):
			result.Config.Contexts[i].Owner = "inventory-team"
		case filepath.Join("internal", "pricing"):
			result.Config.Contexts[i].Owner = "pricing-team"
		}
	}
	for _, item := range []string{filepath.Join("internal", "inventory"), filepath.Join("internal", "pricing")} {
		task, err := harness.NewTask("Ship "+item, "tui")
		if err != nil {
			t.Fatalf("NewTask() error = %v", err)
		}
		task.ContextPath = item
		task.ContextName = strings.ReplaceAll(item, string(filepath.Separator), "-")
		task.Status = harness.TaskCompleted
		task.VerificationStatus = harness.VerificationPassed
		task.ReviewStatus = harness.ReviewApproved
		task.PromotionStatus = harness.PromotionApplied
		data, err := json.MarshalIndent(task, "", "  ")
		if err != nil {
			t.Fatalf("marshal task: %v", err)
		}
		if err := os.WriteFile(filepath.Join(result.Project.TasksDir, task.ID+".json"), data, 0644); err != nil {
			t.Fatalf("write task: %v", err)
		}
	}
	waves, err := harness.BuildReleaseWavePlan(result.Project, result.Config, harness.ReleasePlanOptions{}, harness.ReleaseGroupByOwner)
	if err != nil {
		t.Fatalf("BuildReleaseWavePlan() error = %v", err)
	}
	if _, err := harness.ApplyReleaseWavePlan(result.Project, waves, "", "rollout-controls-tui"); err != nil {
		t.Fatalf("ApplyReleaseWavePlan() error = %v", err)
	}
	m := newTestModel()
	if cmd := m.handleCommand("/harness release reject rollout-controls-tui 2 waiting for policy"); cmd != nil {
		t.Fatal("expected /harness release reject to complete inline")
	}
	if !strings.Contains(m.output.String(), "gate=rejected") || !strings.Contains(m.output.String(), "waiting for policy") {
		t.Fatalf("expected rejected gate output, got %q", m.output.String())
	}
	m.output.Reset()
	if cmd := m.handleCommand("/harness release approve rollout-controls-tui 2 policy approved"); cmd != nil {
		t.Fatal("expected /harness release approve to complete inline")
	}
	if !strings.Contains(m.output.String(), "gate=approved") || !strings.Contains(m.output.String(), "policy approved") {
		t.Fatalf("expected approved gate output, got %q", m.output.String())
	}
	m.output.Reset()
	if cmd := m.handleCommand("/harness release pause rollout-controls-tui waiting for signoff"); cmd != nil {
		t.Fatal("expected /harness release pause to complete inline")
	}
	if !strings.Contains(m.output.String(), "status=paused") || !strings.Contains(m.output.String(), "waiting for signoff") {
		t.Fatalf("expected paused rollout output, got %q", m.output.String())
	}
	m.output.Reset()
	if cmd := m.handleCommand("/harness release resume rollout-controls-tui signoff received"); cmd != nil {
		t.Fatal("expected /harness release resume to complete inline")
	}
	if !strings.Contains(m.output.String(), "status=active") || !strings.Contains(m.output.String(), "signoff received") {
		t.Fatalf("expected resumed rollout output, got %q", m.output.String())
	}
	m.output.Reset()
	if cmd := m.handleCommand("/harness release abort rollout-controls-tui freeze window"); cmd != nil {
		t.Fatal("expected /harness release abort to complete inline")
	}
	if !strings.Contains(m.output.String(), "status=aborted") || !strings.Contains(m.output.String(), "freeze window") {
		t.Fatalf("expected aborted rollout output, got %q", m.output.String())
	}
}
