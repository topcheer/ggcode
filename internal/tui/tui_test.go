package tui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/image"
)

func TestRenderMarkdown(t *testing.T) {
	// Should not panic and return non-empty string
	result := RenderMarkdown("# Hello\n\nWorld")
	if result == "" {
		t.Error("expected non-empty markdown output")
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
		}
	}

	pageOne := m.renderSkillsPanel()
	if !strings.Contains(pageOne, "page 1/2") {
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
	if !strings.Contains(pageTwo, "page 2/2") {
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

	sidebar := m.renderSidebar(30)
	if !strings.Contains(sidebar, "Run /update") {
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

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
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

func TestMCPPanelCtrlCUsesGlobalExitFlow(t *testing.T) {
	m := newTestModel()
	m.mcpServers = []MCPInfo{{Name: "web-reader", Transport: "http"}}
	m.openMCPPanel()

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil {
		t.Fatal("expected first ctrl-c to arm exit confirmation")
	}
	m2 := next.(Model)
	if !m2.exitConfirmPending {
		t.Fatal("expected ctrl-c in MCP panel to arm exit confirmation")
	}
	if m2.mcpPanel == nil {
		t.Fatal("expected MCP panel to remain open until explicit quit or esc")
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

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd != nil {
		t.Fatal("expected install mode toggle to be synchronous")
	}
	m = next.(Model)
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("stdio npx -y 12306-mcp stdio")},
	} {
		next, cmd = m.Update(key)
		if cmd != nil {
			t.Fatal("expected typing in install mode to stay synchronous")
		}
		m = next.(Model)
	}

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
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

func TestProviderPanelCtrlCUsesGlobalExitFlow(t *testing.T) {
	m := newTestModel()
	m.SetConfig(config.DefaultConfig())
	m.openProviderPanel()

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil {
		t.Fatal("expected first ctrl-c to arm exit confirmation")
	}
	m2 := next.(Model)
	if !m2.exitConfirmPending {
		t.Fatal("expected ctrl-c in provider panel to arm exit confirmation")
	}
	if m2.providerPanel == nil {
		t.Fatal("expected provider panel to remain open until explicit quit or esc")
	}
}

func TestModelPanelCtrlCUsesGlobalExitFlow(t *testing.T) {
	m := newTestModel()
	m.SetConfig(config.DefaultConfig())
	m.modelPanel = &modelPanelState{models: []string{"gpt-4o-mini"}}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil {
		t.Fatal("expected first ctrl-c to arm exit confirmation")
	}
	m2 := next.(Model)
	if !m2.exitConfirmPending {
		t.Fatal("expected ctrl-c in model panel to arm exit confirmation")
	}
	if m2.modelPanel == nil {
		t.Fatal("expected model panel to remain open until explicit quit or esc")
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

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
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

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
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

func TestInitCommandStartsRepoKnowledgeCollection(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoDir := t.TempDir()
	subDir := filepath.Join(repoDir, "internal", "tui")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(repoDir, ".git"), 0755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
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
	if err := os.Mkdir(filepath.Join(repoDir, ".git"), 0755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
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
