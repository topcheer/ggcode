package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/session"
)

func TestShouldSyntaxHighlightPreviewPath_CoversLSPLanguages(t *testing.T) {
	for _, path := range []string{
		"main.go",
		"main.rs",
		"main.c",
		"main.cpp",
		"main.mm",
		"main.lua",
		"main.swift",
		"main.tf",
		"terraform.tfvars",
		"terragrunt.hcl",
		"main.zig",
		"Main.java",
		"main.ts",
		"main.mjs",
		"main.py",
		"Program.cs",
		"demo.csproj",
		"demo.sln",
		"demo.slnx",
		"config.jsonc",
		"script.ksh",
		"Dockerfile",
		"Containerfile",
		".env.local",
	} {
		if !shouldSyntaxHighlightPreviewPath(path) {
			t.Fatalf("expected syntax highlighting to be enabled for %q", path)
		}
	}
}

func TestShouldSyntaxHighlightPreviewPath_KeepsMarkdownOnMarkdownRenderer(t *testing.T) {
	for _, path := range []string{"README.md", "guide.markdown", "page.mdx"} {
		if shouldSyntaxHighlightPreviewPath(path) {
			t.Fatalf("expected markdown path %q to skip syntax highlighting", path)
		}
	}
}

func TestCtrlFTogglesFileBrowser(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(prevWD) }()
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	m := newTestModel()
	m.handleResize(120, 32)

	next, _ := m.Update(tea.KeyPressMsg{Text: "ctrl+f"})
	updated := next.(Model)
	if updated.fileBrowser == nil {
		t.Fatal("expected ctrl+f to open file browser")
	}
	if updated.fileBrowser.preview == nil || filepath.Base(updated.fileBrowser.preview.AbsPath) != "main.go" {
		t.Fatalf("expected file browser preview to load first file, got %#v", updated.fileBrowser.preview)
	}

	next, _ = updated.Update(tea.KeyPressMsg{Text: "ctrl+f"})
	updated = next.(Model)
	if updated.fileBrowser != nil {
		t.Fatal("expected ctrl+f to close file browser")
	}
}

func TestFileBrowserEnterTogglesDirectoryAndFilterSkipsBuildDirs(t *testing.T) {
	workspace := t.TempDir()
	for _, dir := range []string{"src/nested", "classes", "node_modules/pkg"} {
		if err := os.MkdirAll(filepath.Join(workspace, dir), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(workspace, "src", "keep.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatalf("write keep.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "classes", "skip.class"), []byte("x"), 0644); err != nil {
		t.Fatalf("write skip.class: %v", err)
	}
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(prevWD) }()
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	m := newTestModel()
	m.handleResize(120, 32)
	next, _ := m.Update(tea.KeyPressMsg{Text: "ctrl+f"})
	m = next.(Model)
	if m.fileBrowser == nil {
		t.Fatal("expected file browser to open")
	}
	view := m.renderFileBrowser()
	if strings.Contains(view, "classes") || strings.Contains(view, "node_modules") {
		t.Fatalf("expected skipped build dirs to be hidden, got %q", view)
	}

	selectedDir := session.NormalizeWorkspacePath(filepath.Join(workspace, "src"))
	m.fileBrowser.selectedPath = selectedDir
	m.syncFileBrowser(false)
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if !m.fileBrowser.expanded[selectedDir] {
		t.Fatal("expected enter to expand directory")
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if m.fileBrowser.expanded[selectedDir] {
		t.Fatal("expected enter to collapse directory on second press")
	}

	next, _ = m.Update(tea.KeyPressMsg{Text: "/"})
	m = next.(Model)
	next, _ = m.Update(tea.KeyPressMsg{Text: "keep"})
	m = next.(Model)
	if len(m.fileBrowser.entries) != 2 {
		t.Fatalf("expected filtered tree to retain ancestor + file, got %#v", m.fileBrowser.entries)
	}
	if filepath.Base(m.fileBrowser.entries[len(m.fileBrowser.entries)-1].path) != "keep.go" {
		t.Fatalf("expected filter to match keep.go, got %#v", m.fileBrowser.entries)
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if m.fileBrowser.filter != "keep" {
		t.Fatalf("expected enter to leave filter applied, got %q", m.fileBrowser.filter)
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = next.(Model)
	if m.fileBrowser == nil {
		t.Fatal("expected esc to clear filter without closing browser")
	}
	if m.fileBrowser.filter != "" {
		t.Fatalf("expected esc to clear active filter, got %q", m.fileBrowser.filter)
	}
}

func TestBinaryPreviewIsBlocked(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "data.bin")
	if err := os.WriteFile(target, []byte{0x00, 0x01, 0x02, 0x03}, 0644); err != nil {
		t.Fatalf("write binary file: %v", err)
	}
	state := buildPreviewPanelStateForPath(target, 0)
	if state == nil {
		t.Fatal("expected preview state")
	}
	if !strings.Contains(state.Error, "Binary") && !strings.Contains(state.Error, "二进制") {
		t.Fatalf("expected binary preview error, got %#v", state)
	}
}

func TestUTF8MarkdownPreviewIsNotTreatedAsBinary(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "README.md")
	content := "# 标题\n\n这里有中文内容。\n"
	if err := os.WriteFile(target, []byte(content), 0644); err != nil {
		t.Fatalf("write markdown file: %v", err)
	}
	state := buildPreviewPanelStateForPath(target, 0)
	if state == nil {
		t.Fatal("expected preview state")
	}
	if state.Error != "" {
		t.Fatalf("expected utf-8 markdown not to be treated as binary, got %#v", state)
	}
}

func TestFileBrowserDirectoryPreviewDoesNotDuplicatePath(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, "docs"), 0755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(prevWD) }()
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	m := newTestModel()
	m.handleResize(120, 32)
	next, _ := m.Update(tea.KeyPressMsg{Text: "ctrl+f"})
	m = next.(Model)
	m.fileBrowser.selectedPath = filepath.Join(workspace, "docs")
	m.syncFileBrowser(false)

	view := m.renderFileBrowser()
	if strings.Count(view, "docs") != 2 {
		t.Fatalf("expected directory name once in tree and once in preview meta, got %q", view)
	}
}
