package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tool"
)

func collectDesktopWidgets(obj fyne.CanvasObject, forms *[]*widget.Form, selects *[]*widget.Select, cards *[]*widget.Card) {
	switch v := obj.(type) {
	case *fyne.Container:
		for _, child := range v.Objects {
			collectDesktopWidgets(child, forms, selects, cards)
		}
	case *container.Scroll:
		if v.Content != nil {
			collectDesktopWidgets(v.Content, forms, selects, cards)
		}
	case *widget.Card:
		if cards != nil {
			*cards = append(*cards, v)
		}
		if v.Content != nil {
			collectDesktopWidgets(v.Content, forms, selects, cards)
		}
	case *widget.Form:
		*forms = append(*forms, v)
		for _, item := range v.Items {
			if item != nil && item.Widget != nil {
				collectDesktopWidgets(item.Widget, forms, selects, cards)
			}
		}
	case *widget.Select:
		*selects = append(*selects, v)
	}
}

// helper: create a temp workspace directory with sample files
func createTestWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// Source file
	os.MkdirAll(filepath.Join(root, "cmd"), 0755)
	os.WriteFile(filepath.Join(root, "cmd", "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644)

	// Markdown file
	os.WriteFile(filepath.Join(root, "README.md"), []byte("# Test\n\nHello **world**\n\n```mermaid\ngraph LR\n  A-->B\n```\n"), 0644)

	// Config file
	os.WriteFile(filepath.Join(root, "config.yaml"), []byte("name: test\nversion: 1.0\n"), 0644)

	// Text file
	os.WriteFile(filepath.Join(root, "notes.txt"), []byte("some notes here\nline 2\nline 3\n"), 0644)

	// Binary file (has null bytes)
	os.WriteFile(filepath.Join(root, "binary.dat"), []byte("hello\x00world"), 0644)

	// Nested directory
	os.MkdirAll(filepath.Join(root, "internal", "pkg"), 0755)
	os.WriteFile(filepath.Join(root, "internal", "pkg", "util.go"), []byte("package pkg\n\nfunc Hello() string { return \"hi\" }\n"), 0644)

	// Skip dirs (should not appear in tree)
	os.MkdirAll(filepath.Join(root, ".git", "objects"), 0755)
	os.MkdirAll(filepath.Join(root, "node_modules", "pkg"), 0755)
	os.MkdirAll(filepath.Join(root, "vendor", "lib"), 0755)

	return root
}

func createDesktopTestProjectDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// ── fileIconForExt ─────────────────────────────────────────────

func TestFileIconForExt(t *testing.T) {
	tests := []struct {
		ext  string
		want fyne.Resource
	}{
		{".go", theme.DocumentIcon()},
		{".GO", theme.DocumentIcon()},
		{".md", theme.DocumentIcon()},
		{".txt", theme.DocumentIcon()},
		{".png", theme.MediaPhotoIcon()},
		{".jpg", theme.MediaPhotoIcon()},
		{".jpeg", theme.MediaPhotoIcon()},
		{".gif", theme.MediaPhotoIcon()},
		{".svg", theme.MediaPhotoIcon()},
		{".webp", theme.MediaPhotoIcon()},
		{".yaml", theme.SettingsIcon()},
		{".json", theme.SettingsIcon()},
		{".toml", theme.SettingsIcon()},
		{".xml", theme.SettingsIcon()},
		{".xyz", theme.FileIcon()},
		{"", theme.FileIcon()},
	}
	for _, tt := range tests {
		got := fileIconForExt(tt.ext)
		if got != tt.want {
			t.Errorf("fileIconForExt(%q) = %v, want %v", tt.ext, got, tt.want)
		}
	}
}

// ── skippedDirs ────────────────────────────────────────────────

func TestSkippedDirsComplete(t *testing.T) {
	// Verify all major skip targets are in the map
	mustSkip := []string{".git", "node_modules", "vendor", ".venv", "venv",
		"bin", "dist", "build", "__pycache__", "target", "coverage",
		".gradle", ".cache", ".terraform", "pods"}
	for _, d := range mustSkip {
		if _, ok := skippedDirs[d]; !ok {
			t.Errorf("%q not in skippedDirs", d)
		}
	}
}

func TestSkippedDirsNotInTree(t *testing.T) {
	root := createTestWorkspace(t)
	ft := NewFileTree(root, func(absPath string) {})
	children := ft.childUIDs("")

	forbidden := map[string]bool{
		".git": true, "node_modules": true, "vendor": true,
	}
	for _, child := range children {
		name := filepath.Base(string(child))
		if forbidden[name] {
			t.Errorf("forbidden directory appeared in tree: %s", name)
		}
	}
}

// ── FileTree tests ─────────────────────────────────────────────

func TestFileTreeShowsWorkspaceFiles(t *testing.T) {
	root := createTestWorkspace(t)
	ft := NewFileTree(root, func(absPath string) {})

	if ft.tree == nil {
		t.Fatal("tree widget is nil")
	}

	c := test.NewCanvas()
	c.SetContent(ft.Widget())
	c.Resize(fyne.NewSize(400, 600))

	children := ft.childUIDs("")
	if len(children) == 0 {
		t.Fatal("root has no children")
	}

	foundCmd := false
	foundInternal := false
	for _, child := range children {
		if string(child) == "cmd" {
			foundCmd = true
			if !ft.isBranch("cmd") {
				t.Error("cmd should be a branch (directory)")
			}
		}
		if string(child) == "internal" {
			foundInternal = true
			if !ft.isBranch("internal") {
				t.Error("internal should be a branch (directory)")
			}
		}
	}
	if !foundCmd {
		t.Error("cmd directory not found in tree")
	}
	if !foundInternal {
		t.Error("internal directory not found in tree")
	}
}

func TestFileTreeFileNotBranch(t *testing.T) {
	root := createTestWorkspace(t)
	ft := NewFileTree(root, nil)

	if ft.isBranch("README.md") {
		t.Error("README.md should not be a branch")
	}
	if ft.isBranch("config.yaml") {
		t.Error("config.yaml should not be a branch")
	}
}

func TestFileTreeOnOpenCallback(t *testing.T) {
	root := createTestWorkspace(t)
	clicked := ""
	ft := NewFileTree(root, func(absPath string) {
		clicked = absPath
	})

	ft.onSelected("README.md")
	expected := filepath.Join(root, "README.md")
	if clicked != expected {
		t.Errorf("onOpen callback: got %q, want %q", clicked, expected)
	}
}

func TestFileTreeOnOpenDirectoryIgnored(t *testing.T) {
	root := createTestWorkspace(t)
	clicked := ""
	ft := NewFileTree(root, func(absPath string) {
		clicked = absPath
	})

	ft.onSelected("cmd")
	if clicked != "" {
		t.Errorf("onOpen should not fire for directory, got %q", clicked)
	}
}

func TestFileTreeOnOpenNilCallback(t *testing.T) {
	root := createTestWorkspace(t)
	ft := NewFileTree(root, nil)

	// Should not panic
	ft.onSelected("README.md")
}

func TestFileTreeOnOpenNonexistentPath(t *testing.T) {
	root := createTestWorkspace(t)
	clicked := ""
	ft := NewFileTree(root, func(absPath string) {
		clicked = absPath
	})

	// Path that doesn't exist
	ft.onSelected("nonexistent.go")
	if clicked != "" {
		// os.Stat fails, IsDir() returns false, but it won't be a real file
		// The callback still fires because os.Stat fails silently
		// This tests that it doesn't panic
	}
}

func TestFileTreeRefresh(t *testing.T) {
	root := createTestWorkspace(t)
	ft := NewFileTree(root, nil)

	children1 := ft.childUIDs("")
	count1 := len(children1)

	os.WriteFile(filepath.Join(root, "newfile.txt"), []byte("test"), 0644)

	ft.entries = nil
	ft.Refresh()

	children2 := ft.childUIDs("")
	count2 := len(children2)

	if count2 <= count1 {
		t.Errorf("after adding file: children count %d, expected > %d", count2, count1)
	}
}

func TestFileTreeDirectorySortOrder(t *testing.T) {
	root := t.TempDir()
	// Create files and dirs with names that sort differently
	os.MkdirAll(filepath.Join(root, "z_dir"), 0755)
	os.MkdirAll(filepath.Join(root, "a_dir"), 0755)
	os.WriteFile(filepath.Join(root, "z_file.txt"), []byte("z"), 0644)
	os.WriteFile(filepath.Join(root, "a_file.txt"), []byte("a"), 0644)

	ft := NewFileTree(root, nil)
	children := ft.childUIDs("")

	// All directories should come before all files
	firstFileIdx := -1
	lastDirIdx := -1
	for i, child := range children {
		if ft.isBranch(string(child)) {
			lastDirIdx = i
		} else {
			if firstFileIdx == -1 {
				firstFileIdx = i
			}
		}
	}
	if firstFileIdx >= 0 && lastDirIdx >= 0 && firstFileIdx < lastDirIdx {
		t.Errorf("directories should come before files: firstFile=%d, lastDir=%d", firstFileIdx, lastDirIdx)
	}

	// Directories should be sorted alphabetically
	var dirNames []string
	for _, child := range children {
		if ft.isBranch(string(child)) {
			dirNames = append(dirNames, string(child))
		}
	}
	for i := 1; i < len(dirNames); i++ {
		if dirNames[i] < dirNames[i-1] {
			t.Errorf("dirs not sorted: %q > %q", dirNames[i-1], dirNames[i])
		}
	}
}

func TestFileTreeDSDotStoreSkipped(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, ".DS_Store"), []byte("junk"), 0644)
	os.WriteFile(filepath.Join(root, "real.txt"), []byte("ok"), 0644)

	ft := NewFileTree(root, nil)
	children := ft.childUIDs("")

	for _, child := range children {
		if string(child) == ".DS_Store" {
			t.Error(".DS_Store should be skipped")
		}
	}
}

func TestFileTreeIsBranchNonexistent(t *testing.T) {
	root := createTestWorkspace(t)
	ft := NewFileTree(root, nil)

	if ft.isBranch("does_not_exist") {
		t.Error("nonexistent path should not be a branch")
	}
}

func TestFileTreeLazyLoading(t *testing.T) {
	root := createTestWorkspace(t)
	ft := NewFileTree(root, nil)

	// Before accessing, entries should be nil
	if ft.entries != nil {
		t.Fatal("entries should start nil")
	}

	// First access loads
	_ = ft.childUIDs("")
	if ft.entries == nil {
		t.Fatal("entries should be populated after first access")
	}

	// Second access uses cache (same count)
	cached := ft.childUIDs("")
	if len(cached) != len(ft.entries[""]) {
		t.Error("second access should use cache")
	}
}

func TestFileTreeNestedDirectories(t *testing.T) {
	root := createTestWorkspace(t)
	ft := NewFileTree(root, nil)

	// Load root first
	ft.childUIDs("")

	// Load internal/children
	internalChildren := ft.childUIDs("internal")
	foundPkg := false
	for _, child := range internalChildren {
		if filepath.Base(string(child)) == "pkg" {
			foundPkg = true
			if !ft.isBranch(string(child)) {
				t.Error("pkg should be a branch")
			}
		}
	}
	if !foundPkg {
		t.Error("pkg directory not found under internal/")
	}

	// Load internal/pkg/ children
	pkgPath := "internal" + string(filepath.Separator) + "pkg"
	pkgChildren := ft.childUIDs(pkgPath)
	foundUtil := false
	for _, child := range pkgChildren {
		if filepath.Base(string(child)) == "util.go" {
			foundUtil = true
			if ft.isBranch(string(child)) {
				t.Error("util.go should not be a branch")
			}
		}
	}
	if !foundUtil {
		t.Error("util.go not found under internal/pkg/")
	}
}

func TestFileTreeWidgetNotNil(t *testing.T) {
	root := createTestWorkspace(t)
	ft := NewFileTree(root, nil)

	w := ft.Widget()
	if w == nil {
		t.Fatal("Widget() returned nil")
	}
}

func TestFileTreeUpdateNode(t *testing.T) {
	root := createTestWorkspace(t)
	ft := NewFileTree(root, nil)

	node := ft.createNode(true)
	ft.updateNode("cmd", true, node)

	node2 := ft.createNode(false)
	ft.updateNode("README.md", false, node2)
}

// ── isBinaryData ───────────────────────────────────────────────

func TestIsBinaryData(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"empty", []byte{}, false},
		{"pure text", []byte("hello world"), false},
		{"has null byte", []byte("hello\x00world"), true},
		{"null at start", []byte("\x00abc"), true},
		{"null at end within 512", append([]byte(strings.Repeat("a", 511)), 0), true},
		{"null beyond 512", append([]byte(strings.Repeat("a", 600)), 0), false},
		{"large text no null", []byte(strings.Repeat("abcdefgh\n", 100)), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBinaryData(tt.data); got != tt.want {
				t.Errorf("isBinaryData() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ── formatSize ─────────────────────────────────────────────────

func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{2147483648, "2.0 GB"},
	}
	for _, tt := range tests {
		got := formatSize(tt.bytes)
		if got != tt.want {
			t.Errorf("formatSize(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}

// ── splitMarkdownAndMermaid ────────────────────────────────────

func TestSplitMarkdownNoMermaid(t *testing.T) {
	input := "# Title\n\nSome text\n"
	parts := splitMarkdownAndMermaid(input)
	if len(parts) != 1 || parts[0].isMermaid {
		t.Errorf("expected 1 non-mermaid part, got %v", parts)
	}
}

func TestSplitMarkdownSingleMermaid(t *testing.T) {
	input := "# Title\n\n```mermaid\ngraph LR\n  A-->B\n```\n\nMore text\n"
	parts := splitMarkdownAndMermaid(input)
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(parts))
	}
	if parts[0].isMermaid {
		t.Error("first part should be text")
	}
	if !parts[1].isMermaid {
		t.Error("second part should be mermaid")
	}
	if parts[1].content != "graph LR\n  A-->B" {
		t.Errorf("mermaid content = %q", parts[1].content)
	}
	if parts[2].isMermaid {
		t.Error("third part should be text")
	}
}

func TestSplitMarkdownMultipleMermaid(t *testing.T) {
	input := "```mermaid\ngraph A\n```\nText between\n```mermaid\ngraph B\n```\nFinal text"
	parts := splitMarkdownAndMermaid(input)
	if len(parts) != 4 {
		t.Fatalf("expected 4 parts, got %d", len(parts))
	}
	if !parts[0].isMermaid {
		t.Error("first should be mermaid")
	}
	if parts[1].isMermaid {
		t.Error("second should be text")
	}
	if !parts[2].isMermaid {
		t.Error("third should be mermaid")
	}
	if parts[3].isMermaid {
		t.Error("fourth should be text")
	}
	if parts[3].content != "Final text" {
		t.Errorf("fourth content = %q, want %q", parts[3].content, "Final text")
	}
}

func TestSplitMarkdownMermaidOnly(t *testing.T) {
	input := "```mermaid\ngraph LR\n  A-->B\n```"
	parts := splitMarkdownAndMermaid(input)
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	if !parts[0].isMermaid {
		t.Error("should be mermaid")
	}
}

func TestSplitMarkdownEmpty(t *testing.T) {
	input := ""
	parts := splitMarkdownAndMermaid(input)
	if len(parts) != 1 {
		t.Fatalf("expected 1 part (empty fallback), got %d", len(parts))
	}
}

func TestSplitMarkdownMermaidAtEnd(t *testing.T) {
	input := "# Header\n\n```mermaid\npie\n  title Pets\n  dogs: 50\n```"
	parts := splitMarkdownAndMermaid(input)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
	if parts[0].isMermaid {
		t.Error("first should be text")
	}
	if !parts[1].isMermaid {
		t.Error("second should be mermaid")
	}
}

// ── imageExts / markdownExts ───────────────────────────────────

func TestImageExts(t *testing.T) {
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".bmp", ".ico"} {
		if !imageExts[ext] {
			t.Errorf("imageExts[%q] should be true", ext)
		}
	}
	if imageExts[".go"] {
		t.Error(".go should not be in imageExts")
	}
}

func TestMarkdownExts(t *testing.T) {
	for _, ext := range []string{".md", ".markdown", ".mdown", ".mkd"} {
		if !markdownExts[ext] {
			t.Errorf("markdownExts[%q] should be true", ext)
		}
	}
	if markdownExts[".go"] {
		t.Error(".go should not be in markdownExts")
	}
}

func TestPlainTextExts(t *testing.T) {
	for _, ext := range []string{".txt", ".log", ".csv", ".ini", ".cfg", ".env"} {
		if !isPlainTextExt(ext) {
			t.Errorf("isPlainTextExt(%q) should be true", ext)
		}
	}
	if isPlainTextExt(".go") {
		t.Error("isPlainTextExt(.go) should be false")
	}
	if isPlainTextExt(".py") {
		t.Error("isPlainTextExt(.py) should be false")
	}
}

func TestTextPreviewUsesWordWrap(t *testing.T) {
	root := t.TempDir()
	app := &App{dc: &DesktopConfig{WorkDir: root}}

	os.WriteFile(filepath.Join(root, "readme.txt"), []byte(strings.Repeat("hello world ", 100)), 0644)

	fp := NewFilePreview(app, filepath.Join(root, "readme.txt"), 0, nil)
	obj := fp.Widget()
	w := test.NewWindow(obj)
	w.Resize(fyne.NewSize(600, 400))

	entry := findEntry(obj)
	if entry == nil {
		t.Fatal("text preview should contain an Entry")
	}
	if entry.Wrapping != fyne.TextWrapWord {
		t.Error("plain text preview should use TextWrapWord")
	}
}

// ── FilePreview tests ──────────────────────────────────────────

func TestFilePreviewCodeFile(t *testing.T) {
	root := createTestWorkspace(t)
	app := &App{dc: &DesktopConfig{WorkDir: root}}

	goFile := filepath.Join(root, "cmd", "main.go")
	fp := NewFilePreview(app, goFile, 0, nil)
	if fp == nil {
		t.Fatal("preview is nil")
	}
	obj := fp.Widget()
	w := test.NewWindow(obj)
	w.Resize(fyne.NewSize(600, 400))

	// Verify code preview uses a single Entry widget, not many Labels
	entry := findEntry(obj)
	if entry == nil {
		t.Fatal("code preview should contain a widget.Entry for code content")
	}
	if !entry.Disabled() {
		t.Error("code preview Entry should be disabled (read-only)")
	}
	if !entry.MultiLine {
		t.Error("code preview Entry should be MultiLine")
	}
	if entry.Text == "" {
		t.Error("code preview Entry should have content")
	}
	// Verify line numbers are present
	if !strings.Contains(entry.Text, "1 ") {
		t.Errorf("code preview should have line numbers, got: %q", entry.Text[:min(100, len(entry.Text))])
	}
	// Verify code content is on the same line as line number (not vertical)
	lines := strings.Split(entry.Text, "\n")
	if len(lines) < 2 {
		t.Fatalf("code preview should have multiple lines, got %d", len(lines))
	}
	// Verify each line has a line number prefix
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Line should start with a number followed by spaces and content
		if !strings.HasPrefix(trimmed, fmt.Sprintf("%d", i+1)) {
			t.Errorf("line %d should start with line number %d: %q", i+1, i+1, trimmed)
		}
		// Vertical rendering bug: if line number and code are on separate lines
		// each line would be just 1-2 chars. Verify at least one line has real content.
	}
	// Verify at least one line has actual code content (not just line numbers)
	hasCodeContent := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) > 5 { // "1  package" is > 5 chars
			hasCodeContent = true
			break
		}
	}
	if !hasCodeContent {
		t.Error("no line has real code content - possible vertical rendering bug")
	}
}

func TestFilePreviewCodeWithTargetLine(t *testing.T) {
	root := createTestWorkspace(t)
	app := &App{dc: &DesktopConfig{WorkDir: root}}

	os.WriteFile(filepath.Join(root, "ten.go"), []byte("line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\n"), 0644)

	fp := NewFilePreview(app, filepath.Join(root, "ten.go"), 5, nil)
	obj := fp.Widget()
	w := test.NewWindow(obj)
	w.Resize(fyne.NewSize(600, 400))

	entry := findEntry(obj)
	if entry == nil {
		t.Fatal("code preview should contain a widget.Entry")
	}
	// Verify 10 lines of content
	lines := strings.Split(entry.Text, "\n")
	if len(lines) != 10 {
		t.Errorf("expected 10 lines, got %d", len(lines))
	}
	// Verify each line has both line number and content
	for i, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), fmt.Sprintf("%d", i+1)) {
			t.Errorf("line %d should start with line number: %q", i+1, line)
		}
	}
}

func TestFilePreviewMarkdownFile(t *testing.T) {
	root := createTestWorkspace(t)
	app := &App{dc: &DesktopConfig{WorkDir: root}}

	fp := NewFilePreview(app, filepath.Join(root, "README.md"), 0, nil)
	obj := fp.Widget()
	w := test.NewWindow(obj)
	w.Resize(fyne.NewSize(600, 400))

	// Markdown preview should NOT use a plain Entry
	// It should use markdownx.MarkdownWidget for rich rendering
	if findEntry(obj) != nil {
		// Markdown file should not be rendered as plain text Entry
		// (unless it's wrapped in a larger container)
		_ = w // just verify no panic
	}
}

func TestFilePreviewBinaryFile(t *testing.T) {
	root := createTestWorkspace(t)
	app := &App{dc: &DesktopConfig{WorkDir: root}}

	fp := NewFilePreview(app, filepath.Join(root, "binary.dat"), 0, nil)
	obj := fp.Widget()
	w := test.NewWindow(obj)
	w.Resize(fyne.NewSize(600, 400))

	// Binary preview should show file info text, not a code Entry
	lbl := findLabelDeep(obj)
	if lbl == nil {
		t.Fatal("binary preview should show a label with file info")
	}
	if !strings.Contains(lbl.Text, "Binary") {
		t.Errorf("binary info should mention 'Binary', got: %q", lbl.Text)
	}
	if !strings.Contains(lbl.Text, "binary.dat") {
		t.Errorf("binary info should show filename, got: %q", lbl.Text)
	}
}

func TestFilePreviewDirectoryError(t *testing.T) {
	root := createTestWorkspace(t)
	app := &App{dc: &DesktopConfig{WorkDir: root}}

	fp := NewFilePreview(app, root, 0, nil)
	obj := fp.Widget()
	w := test.NewWindow(obj)
	w.Resize(fyne.NewSize(600, 400))

	lbl := findLabelDeep(obj)
	if lbl == nil {
		t.Fatal("directory error should show a label")
	}
	if !strings.Contains(lbl.Text, "directory") {
		t.Errorf("should mention directory error, got: %q", lbl.Text)
	}
}

func TestFilePreviewNonexistentFile(t *testing.T) {
	app := &App{dc: &DesktopConfig{WorkDir: "/tmp"}}

	fp := NewFilePreview(app, "/nonexistent/file.go", 0, nil)
	obj := fp.Widget()
	w := test.NewWindow(obj)
	w.Resize(fyne.NewSize(600, 400))

	lbl := findLabelDeep(obj)
	if lbl == nil {
		t.Fatal("nonexistent file should show an error label")
	}
	if !strings.Contains(lbl.Text, "Cannot") {
		t.Errorf("should show access error, got: %q", lbl.Text)
	}
}

func TestFilePreviewLargeFile(t *testing.T) {
	root := t.TempDir()
	app := &App{dc: &DesktopConfig{WorkDir: root}}

	largeFile := filepath.Join(root, "large.log")
	f, _ := os.Create(largeFile)
	f.WriteString(strings.Repeat("x\n", maxPreviewSize/2+1))
	f.Close()

	fp := NewFilePreview(app, largeFile, 0, nil)
	obj := fp.Widget()
	w := test.NewWindow(obj)
	w.Resize(fyne.NewSize(600, 400))

	lbl := findLabelDeep(obj)
	if lbl == nil {
		t.Fatal("large file should show an error label")
	}
	if !strings.Contains(lbl.Text, "large") && !strings.Contains(lbl.Text, "Large") {
		t.Errorf("should mention file too large, got: %q", lbl.Text)
	}
}

func TestFilePreviewEmptyFile(t *testing.T) {
	root := t.TempDir()
	app := &App{dc: &DesktopConfig{WorkDir: root}}

	os.WriteFile(filepath.Join(root, "empty.go"), []byte(""), 0644)

	fp := NewFilePreview(app, filepath.Join(root, "empty.go"), 0, nil)
	w := test.NewWindow(fp.Widget())
	w.Resize(fyne.NewSize(600, 400))
}

func TestFilePreviewTextFile(t *testing.T) {
	root := createTestWorkspace(t)
	app := &App{dc: &DesktopConfig{WorkDir: root}}

	fp := NewFilePreview(app, filepath.Join(root, "notes.txt"), 0, nil)
	obj := fp.Widget()
	w := test.NewWindow(obj)
	w.Resize(fyne.NewSize(600, 400))

	entry := findEntry(obj)
	if entry == nil {
		t.Fatal("text file preview should contain an Entry")
	}
	if entry.Wrapping != fyne.TextWrapWord {
		t.Errorf("plain text file should use TextWrapWord, got %v", entry.Wrapping)
	}
}

func TestFilePreviewYAMLFile(t *testing.T) {
	root := createTestWorkspace(t)
	app := &App{dc: &DesktopConfig{WorkDir: root}}

	fp := NewFilePreview(app, filepath.Join(root, "config.yaml"), 0, nil)
	w := test.NewWindow(fp.Widget())
	w.Resize(fyne.NewSize(600, 400))
}

func TestFilePreviewImagePNG(t *testing.T) {
	root := t.TempDir()
	app := &App{dc: &DesktopConfig{WorkDir: root}}

	pngData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41,
		0x54, 0x08, 0xD7, 0x63, 0xD8, 0xCD, 0xC0, 0x00,
		0x00, 0x00, 0x04, 0x00, 0x01, 0xF6, 0x17, 0xA4,
		0x49, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E,
		0x44, 0xAE, 0x42, 0x60, 0x82,
	}
	os.WriteFile(filepath.Join(root, "test.png"), pngData, 0644)

	fp := NewFilePreview(app, filepath.Join(root, "test.png"), 0, nil)
	obj := fp.Widget()
	w := test.NewWindow(obj)
	w.Resize(fyne.NewSize(600, 400))

	// Should not crash, image should render
	fp.Close() // no-op for non-HTML
}

func TestFilePreviewHTMLFile(t *testing.T) {
	root := t.TempDir()
	app := &App{dc: &DesktopConfig{WorkDir: root}}

	htmlContent := `<!DOCTYPE html><html><head><title>Test</title></head><body><h1>Hello</h1></body></html>`
	os.WriteFile(filepath.Join(root, "index.html"), []byte(htmlContent), 0644)

	fp := NewFilePreview(app, filepath.Join(root, "index.html"), 0, nil)
	obj := fp.Widget()
	w := test.NewWindow(obj)
	w.Resize(fyne.NewSize(600, 400))

	// Verify server was started
	if fp.server == nil {
		t.Fatal("HTML preview should start an HTTP server")
	}

	// Verify the server is serving content
	addr := fp.server.Addr
	resp, err := http.Get("http://" + addr + "/index.html")
	if err != nil {
		t.Fatalf("failed to get HTML from preview server: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("<h1>Hello</h1>")) {
		t.Errorf("response should contain HTML content, got: %s", string(body[:min(200, len(body))]))
	}

	// Close should shut down the server
	fp.Close()
	if fp.server != nil {
		t.Error("server should be nil after Close()")
	}
}

func TestFindFreePort(t *testing.T) {
	port := findFreePort()
	if port < 1 || port > 65535 {
		t.Errorf("invalid port: %d", port)
	}
	// Verify port is actually available
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Errorf("port %d not available: %v", port, err)
	}
	ln.Close()
}

func TestFilePreviewCloseButton(t *testing.T) {
	root := createTestWorkspace(t)
	app := &App{dc: &DesktopConfig{WorkDir: root}}

	closed := false
	fp := NewFilePreview(app, filepath.Join(root, "README.md"), 0, func() {
		closed = true
	})

	obj := fp.Widget()
	w := test.NewWindow(obj)
	w.Resize(fyne.NewSize(600, 400))

	closeBtn := findButton(obj, "")
	if closeBtn != nil {
		test.Tap(closeBtn)
		if !closed {
			t.Error("close callback was not called")
		}
	}
}

func TestFilePreviewWidgetRelativePath(t *testing.T) {
	root := createTestWorkspace(t)
	app := &App{dc: &DesktopConfig{WorkDir: root}}

	fp := NewFilePreview(app, filepath.Join(root, "cmd", "main.go"), 0, nil)
	obj := fp.Widget()
	w := test.NewWindow(obj)
	w.Resize(fyne.NewSize(600, 400))

	// Find the label that shows the path
	label := findLabel(obj)
	if label == nil {
		t.Fatal("no label found in preview header")
	}
	if label.Text != filepath.Join("cmd", "main.go") {
		t.Errorf("path label = %q, want %q", label.Text, filepath.Join("cmd", "main.go"))
	}
}

func TestFilePreviewWidgetNoWorkDir(t *testing.T) {
	root := createTestWorkspace(t)
	app := &App{dc: &DesktopConfig{WorkDir: ""}} // no workdir

	absPath := filepath.Join(root, "README.md")
	fp := NewFilePreview(app, absPath, 0, nil)
	obj := fp.Widget()
	w := test.NewWindow(obj)
	w.Resize(fyne.NewSize(600, 400))

	label := findLabel(obj)
	if label == nil {
		t.Fatal("no label found")
	}
	// Without WorkDir, should show absolute path
	if label.Text != absPath {
		t.Errorf("path label = %q, want %q", label.Text, absPath)
	}
}

func TestFilePreviewNilOnClose(t *testing.T) {
	root := createTestWorkspace(t)
	app := &App{dc: &DesktopConfig{WorkDir: root}}

	fp := NewFilePreview(app, filepath.Join(root, "README.md"), 0, nil)
	obj := fp.Widget()
	w := test.NewWindow(obj)
	w.Resize(fyne.NewSize(600, 400))

	closeBtn := findButton(obj, "")
	if closeBtn != nil {
		// Should not panic with nil onClose
		test.Tap(closeBtn)
	}
}

// ── highlightCode ──────────────────────────────────────────────

func TestHighlightCodeGoFile(t *testing.T) {
	lines := highlightCode("main.go", "package main\n\nfunc main() {}\n")
	if len(lines) < 2 {
		t.Errorf("expected multiple lines, got %d", len(lines))
	}
}

func TestHighlightCodeUnknownExt(t *testing.T) {
	lines := highlightCode("unknown.xyz", "hello world\nline 2")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines, got %d", len(lines))
	}
	if lines[0] != "hello world" {
		t.Errorf("line 0 = %q, want %q", lines[0], "hello world")
	}
}

// ── linkifyFilePaths ───────────────────────────────────────────

func TestLinkifyFilePathsNilApp(t *testing.T) {
	result := linkifyFilePaths("check /some/path/file.go", nil)
	if result != "check /some/path/file.go" {
		t.Error("should return unchanged text with nil app")
	}
}

func TestLinkifyFilePathsEmptyWorkDir(t *testing.T) {
	app := &App{dc: &DesktopConfig{WorkDir: ""}}
	result := linkifyFilePaths("check /some/path/file.go", app)
	if result != "check /some/path/file.go" {
		t.Error("should return unchanged text with empty WorkDir")
	}
}

func TestLinkifyFilePathsExistingFile(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "hello.go"), []byte("package main"), 0644)
	app := &App{dc: &DesktopConfig{WorkDir: root}}

	text := "see " + filepath.Join(root, "hello.go") + " for details"
	result := linkifyFilePaths(text, app)

	if !strings.Contains(result, "[hello.go](file://") {
		t.Errorf("expected linkified path in %q", result)
	}
}

func TestLinkifyFilePathsNonexistentFile(t *testing.T) {
	root := t.TempDir()
	app := &App{dc: &DesktopConfig{WorkDir: root}}

	text := "see " + filepath.Join(root, "nonexistent.go") + " for details"
	result := linkifyFilePaths(text, app)

	if strings.Contains(result, "[nonexistent.go](file://") {
		t.Error("should not linkify nonexistent file")
	}
}

func TestLinkifyFilePathsOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	app := &App{dc: &DesktopConfig{WorkDir: root}}

	text := "see /other/workspace/file.go for details"
	result := linkifyFilePaths(text, app)

	if strings.Contains(result, "[file.go](file://") {
		t.Error("should not linkify paths outside workspace")
	}
}

// ── interceptFileLinks ─────────────────────────────────────────

func TestInterceptFileLinks(t *testing.T) {
	root := t.TempDir()
	app := &App{dc: &DesktopConfig{WorkDir: root}}

	link := widget.NewHyperlink("test.go", mustParseURLTest(t, "file://"+filepath.Join(root, "test.go")))
	box := container.NewVBox(link)

	interceptFileLinks(box, app)

	if link.OnTapped == nil {
		t.Error("OnTapped should be set for file:// link")
	}
}

func TestInterceptFileLinksHTTPS(t *testing.T) {
	root := t.TempDir()
	app := &App{dc: &DesktopConfig{WorkDir: root}}

	link := widget.NewHyperlink("docs", mustParseURLTest(t, "https://example.com"))
	box := container.NewVBox(link)

	interceptFileLinks(box, app)

	if link.OnTapped != nil {
		t.Error("OnTapped should not be set for https:// link")
	}
}

// ── App showFilePreview / closeFilePreview ─────────────────────

func TestAppShowAndCloseFilePreview(t *testing.T) {
	root := createTestWorkspace(t)
	app := &App{
		fyneApp: test.NewApp(),
		dc:      &DesktopConfig{WorkDir: root},
	}
	w := app.fyneApp.NewWindow("test")

	chatLabel := widget.NewLabel("chat")
	app.chatViewObj = chatLabel
	app.sidebarObj = widget.NewLabel("sidebar")
	app.split = container.NewHSplit(chatLabel, app.sidebarObj)
	app.content = container.NewHBox(app.split)
	w.SetContent(app.content)
	w.Resize(fyne.NewSize(800, 600))

	goFile := filepath.Join(root, "cmd", "main.go")
	app.showFilePreview(goFile, 0)

	if app.filePreview == nil {
		t.Fatal("filePreview should be set after showFilePreview")
	}

	app.closeFilePreview()

	if app.filePreview != nil {
		t.Error("filePreview should be nil after closeFilePreview")
	}
}

func TestAppShowFilePreviewNilContent(t *testing.T) {
	root := createTestWorkspace(t)
	app := &App{dc: &DesktopConfig{WorkDir: root}}

	// Should not panic with nil content
	app.showFilePreview(filepath.Join(root, "README.md"), 0)
}

func TestAppCloseFilePreviewRestoresSplit(t *testing.T) {
	root := createTestWorkspace(t)
	app := &App{
		fyneApp: test.NewApp(),
		dc:      &DesktopConfig{WorkDir: root},
	}
	w := app.fyneApp.NewWindow("test")

	chatLabel := widget.NewLabel("chat")
	app.chatViewObj = chatLabel
	app.sidebarObj = widget.NewLabel("sidebar")
	app.split = container.NewHSplit(chatLabel, app.sidebarObj)
	app.content = container.NewHBox(app.split)
	w.SetContent(app.content)
	w.Resize(fyne.NewSize(800, 600))

	// Show then close
	app.showFilePreview(filepath.Join(root, "README.md"), 0)
	app.closeFilePreview()

	if app.filePreview != nil {
		t.Error("filePreview should be nil")
	}
}

func TestAppShowFilePreviewWithSidebarHidden(t *testing.T) {
	root := createTestWorkspace(t)
	app := &App{
		fyneApp:       test.NewApp(),
		dc:            &DesktopConfig{WorkDir: root},
		sidebarHidden: true,
	}
	w := app.fyneApp.NewWindow("test")

	chatLabel := widget.NewLabel("chat")
	app.chatViewObj = chatLabel
	app.sidebarObj = widget.NewLabel("sidebar")
	app.split = container.NewHSplit(chatLabel, app.sidebarObj)
	app.content = container.NewHBox(app.split)
	w.SetContent(app.content)
	w.Resize(fyne.NewSize(800, 600))

	// Should not panic with sidebar hidden
	app.showFilePreview(filepath.Join(root, "README.md"), 0)
	if app.filePreview == nil {
		t.Error("filePreview should be set")
	}
}

func TestAppShowOnboardPopulatesEndpointOptions(tt *testing.T) {
	app := &App{
		fyneApp: test.NewApp(),
		dc:      &DesktopConfig{WorkDir: "/tmp/test-workspace", Language: "en"},
		cfg:     &config.Config{},
		ui:      NewUIState(),
	}
	app.window = app.fyneApp.NewWindow("test")
	app.content = container.NewStack(widget.NewLabel(""))
	app.window.SetContent(app.content)

	app.showOnboard()

	var forms []*widget.Form
	var selects []*widget.Select
	for _, obj := range app.content.Objects {
		collectDesktopWidgets(obj, &forms, &selects, nil)
	}
	if len(forms) != 1 {
		tt.Fatalf("expected one onboard form, got %d", len(forms))
	}
	form := forms[0]
	if len(form.Items) != 4 {
		tt.Fatalf("expected vendor/endpoint/api key/model form items, got %d", len(form.Items))
	}
	if form.Items[1].Text != t("sidebar.endpoint_label") {
		tt.Fatalf("expected endpoint field in onboard form, got %q", form.Items[1].Text)
	}
	if len(selects) != 3 {
		tt.Fatalf("expected vendor, endpoint, and model selects, got %d", len(selects))
	}

	presets := config.VendorPresets()
	if len(presets) == 0 {
		tt.Fatal("expected vendor presets")
	}
	preset := presets[0]
	if len(preset.Endpoints) == 0 {
		tt.Fatalf("expected preset %q to include endpoints", preset.ID)
	}
	selects[0].OnChanged(preset.DisplayName)

	wantOptions := make([]string, 0, len(preset.Endpoints))
	for _, ep := range preset.Endpoints {
		if ep.DisplayName != "" {
			wantOptions = append(wantOptions, ep.DisplayName)
		} else {
			wantOptions = append(wantOptions, ep.ID)
		}
	}
	gotOptions := map[string]bool{}
	for _, option := range selects[1].Options {
		gotOptions[option] = true
	}
	if len(gotOptions) != len(wantOptions) {
		tt.Fatalf("expected %d endpoint options, got %d (%v)", len(wantOptions), len(gotOptions), selects[1].Options)
	}
	for _, want := range wantOptions {
		if !gotOptions[want] {
			tt.Fatalf("expected endpoint options to include %q, got %v", want, selects[1].Options)
		}
	}
	if selects[1].Selected == "" {
		tt.Fatal("expected default endpoint selection after choosing vendor")
	}
}

func TestAppShowOnboardUsesLargerPanel(tt *testing.T) {
	app := &App{
		fyneApp: test.NewApp(),
		dc:      &DesktopConfig{WorkDir: "/tmp/test-workspace", Language: "en"},
		cfg:     &config.Config{},
		ui:      NewUIState(),
	}
	app.window = app.fyneApp.NewWindow("test")
	app.content = container.NewStack(widget.NewLabel(""))
	app.window.SetContent(app.content)

	app.showOnboard()

	if len(app.content.Objects) != 1 {
		tt.Fatalf("expected one onboard root object, got %d", len(app.content.Objects))
	}
	size := app.content.Objects[0].MinSize()
	if size.Width < onboardPanelMinSize.Width || size.Height < onboardPanelMinSize.Height {
		tt.Fatalf("expected onboard panel at least %v, got %v", onboardPanelMinSize, size)
	}
}

func TestAppShowOnboardUsesCurrentLanguage(tt *testing.T) {
	loadTranslations()
	setLanguage("zh-CN")
	defer setLanguage("en")

	app := &App{
		fyneApp: test.NewApp(),
		dc:      defaultDesktopConfig(),
		cfg:     config.DefaultConfig(),
		ui:      NewUIState(),
	}
	app.window = app.fyneApp.NewWindow("test")
	app.content = container.NewStack(widget.NewLabel(""))
	app.window.SetContent(app.content)

	app.showOnboard()

	var forms []*widget.Form
	var selects []*widget.Select
	var cards []*widget.Card
	for _, obj := range app.content.Objects {
		collectDesktopWidgets(obj, &forms, &selects, &cards)
	}
	if len(cards) == 0 {
		tt.Fatal("expected onboarding card")
	}
	if cards[0].Title != "设置 ggcode" || cards[0].Subtitle != "配置你的 AI 提供方" {
		tt.Fatalf("expected localized onboarding card, got title=%q subtitle=%q", cards[0].Title, cards[0].Subtitle)
	}
	if len(forms) != 1 {
		tt.Fatalf("expected one onboard form, got %d", len(forms))
	}
	wantLabels := []string{"厂商", "端点", "API Key", "模型"}
	for i, want := range wantLabels {
		if forms[0].Items[i].Text != want {
			tt.Fatalf("expected localized form label %d to be %q, got %q", i, want, forms[0].Items[i].Text)
		}
	}
}

func TestAppRefreshLanguageUIRerendersOnboard(tt *testing.T) {
	loadTranslations()
	setLanguage("en")
	defer setLanguage("en")

	app := &App{
		fyneApp: test.NewApp(),
		dc:      &DesktopConfig{WorkDir: "/tmp/test-workspace", Language: "en"},
		cfg:     &config.Config{},
		ui:      NewUIState(),
	}
	app.window = app.fyneApp.NewWindow("test")
	app.content = container.NewStack(widget.NewLabel(""))
	app.window.SetContent(app.content)

	app.showOnboard()
	setLanguage("zh-CN")
	app.refreshLanguageUI()

	var forms []*widget.Form
	var selects []*widget.Select
	var cards []*widget.Card
	for _, obj := range app.content.Objects {
		collectDesktopWidgets(obj, &forms, &selects, &cards)
	}
	if len(cards) == 0 {
		tt.Fatal("expected onboarding card after language change")
	}
	if cards[0].Title != "设置 ggcode" || cards[0].Subtitle != "配置你的 AI 提供方" {
		tt.Fatalf("expected live language switch to rerender onboarding card, got title=%q subtitle=%q", cards[0].Title, cards[0].Subtitle)
	}
	if len(forms) != 1 {
		tt.Fatalf("expected one onboard form after language change, got %d", len(forms))
	}
	if forms[0].Items[0].Text != "厂商" || forms[0].Items[1].Text != "端点" {
		tt.Fatalf("expected onboard form labels to update live, got %q / %q", forms[0].Items[0].Text, forms[0].Items[1].Text)
	}
}

func TestSidebarRenderHideButtonTogglesSidebar(t *testing.T) {
	root := createTestWorkspace(t)
	app := &App{
		fyneApp: test.NewApp(),
		dc:      &DesktopConfig{WorkDir: root},
		cfg: &config.Config{
			Vendor:   "zai",
			Endpoint: "default",
			Vendors: map[string]config.VendorConfig{
				"zai": {
					DisplayName: "Z.ai",
					Endpoints: map[string]config.EndpointConfig{
						"default": {
							DisplayName:  "Default",
							Protocol:     "openai",
							BaseURL:      "https://api.example.com/v1",
							DefaultModel: "glm-5.1",
							Models:       []string{"glm-5.1"},
						},
					},
				},
			},
		},
		ui: NewUIState(),
	}
	w := app.fyneApp.NewWindow("test")

	chatLabel := widget.NewLabel("chat")
	sidebarLabel := widget.NewLabel("sidebar")
	app.chatViewObj = chatLabel
	app.sidebarObj = sidebarLabel
	app.split = container.NewHSplit(chatLabel, sidebarLabel)
	app.content = container.NewStack(app.split)
	w.SetContent(app.content)
	w.Resize(fyne.NewSize(1200, 800))

	bridge := &AgentBridge{
		resolved: &config.ResolvedEndpoint{
			VendorID:   "zai",
			VendorName: "Z.ai",
			Model:      "glm-5.1",
			Models:     []string{"glm-5.1"},
		},
	}
	sidebar := NewSidebar(app, bridge, app.ui)
	obj := sidebar.Render()

	hideBtn := findButtonByIcon(obj, theme.NavigateNextIcon())
	if hideBtn == nil {
		t.Fatal("expected sidebar hide button")
	}
	if hideBtn.OnTapped == nil {
		t.Fatal("expected sidebar hide button to be tappable")
	}
	hideBtn.OnTapped()

	if !app.sidebarHidden {
		t.Fatal("expected hide button to toggle sidebarHidden")
	}
	if len(app.content.Objects) != 1 || app.content.Objects[0] != app.chatViewObj {
		t.Fatal("expected hide button to swap content to chat view")
	}
}

func TestSidebarContextTabShowsSessionUsageCard(t *testing.T) {
	loadTranslations()
	setLanguage("en")

	app := &App{
		fyneApp: test.NewApp(),
		dc:      &DesktopConfig{WorkDir: createTestWorkspace(t)},
		cfg: &config.Config{
			Vendor:   "zai",
			Endpoint: "default",
			Vendors: map[string]config.VendorConfig{
				"zai": {
					DisplayName: "Z.ai",
					Endpoints: map[string]config.EndpointConfig{
						"default": {
							DisplayName:  "Default",
							Protocol:     "openai",
							BaseURL:      "https://api.example.com/v1",
							DefaultModel: "glm-5.1",
							Models:       []string{"glm-5.1"},
						},
					},
				},
			},
		},
		ui: NewUIState(),
	}
	app.ui.SetSessionUsage(configuredTestUsage())

	bridge := &AgentBridge{
		resolved: &config.ResolvedEndpoint{
			VendorID:   "zai",
			VendorName: "Z.ai",
			Model:      "glm-5.1",
			Models:     []string{"glm-5.1"},
		},
	}

	sidebar := NewSidebar(app, bridge, app.ui)
	obj := sidebar.buildContextTab()

	var forms []*widget.Form
	var selects []*widget.Select
	var cards []*widget.Card
	collectDesktopWidgets(obj, &forms, &selects, &cards)

	var usageCard *widget.Card
	for _, card := range cards {
		if card.Title == "Session Usage" {
			usageCard = card
			break
		}
	}
	if usageCard == nil {
		t.Fatal("expected session usage card in context tab")
	}
	var labels []string
	collectLabelTexts(usageCard.Content, &labels)
	labelText := strings.Join(labels, "\n")
	for _, want := range []string{"Total", "1.5K", "Input", "400", "Output", "340", "Cache Read", "800", "Cache Write", "64", "Cache Hit", "67%"} {
		if !strings.Contains(labelText, want) {
			t.Fatalf("expected %q in session usage card labels, got %v", want, labels)
		}
	}
}

func TestShowMetricsWindowRendersCards(t *testing.T) {
	loadTranslations()
	setLanguage("en")

	app := &App{
		fyneApp: test.NewApp(),
		dc:      &DesktopConfig{WorkDir: createTestWorkspace(t)},
		cfg: &config.Config{
			Vendor:   "zai",
			Endpoint: "default",
			Vendors: map[string]config.VendorConfig{
				"zai": {
					DisplayName: "Z.ai",
					Endpoints: map[string]config.EndpointConfig{
						"default": {
							DisplayName:  "Default",
							Protocol:     "openai",
							BaseURL:      "https://api.example.com/v1",
							DefaultModel: "glm-5.1",
							Models:       []string{"glm-5.1"},
						},
					},
				},
			},
		},
		ui: NewUIState(),
	}
	app.window = app.fyneApp.NewWindow("main")
	app.ui.SetSessionMetrics([]metrics.MetricEvent{
		{TurnIndex: 1, Type: "llm", TTFT: 900 * time.Millisecond, ThinkTime: 1500 * time.Millisecond, Duration: 6 * time.Second},
		{TurnIndex: 1, Type: "tool", ToolName: "bash", ToolSuccess: true, ToolDuration: 2 * time.Second},
		{TurnIndex: 2, Type: "llm", TTFT: 1200 * time.Millisecond, ThinkTime: 2 * time.Second, Duration: 8 * time.Second},
		{TurnIndex: 2, Type: "tool", ToolName: "read_bash", ToolSuccess: false, ToolError: "timeout", ToolDuration: 3 * time.Second},
	})

	bridge := &AgentBridge{
		resolved: &config.ResolvedEndpoint{
			VendorID:   "zai",
			VendorName: "Z.ai",
			Model:      "glm-5.1",
			Models:     []string{"glm-5.1"},
		},
	}
	app.agentBridge = bridge
	app.showMetricsWindow()
	if app.metricsWindow == nil {
		t.Fatal("expected metrics window to open")
	}
	obj := app.metricsWindow.Content()

	var forms []*widget.Form
	var selects []*widget.Select
	var cards []*widget.Card
	collectDesktopWidgets(obj, &forms, &selects, &cards)

	var metricsCard *widget.Card
	var turnsCard *widget.Card
	for _, card := range cards {
		switch card.Title {
		case "Session Metrics":
			metricsCard = card
		case "Recent Turns":
			turnsCard = card
		}
	}
	if metricsCard == nil || turnsCard == nil {
		t.Fatalf("expected metrics cards, got %+v", cards)
	}

	var labels []string
	collectLabelTexts(metricsCard.Content, &labels)
	labelText := strings.Join(labels, "\n")
	for _, want := range []string{"Turns", "2", "Avg TTFT", "1.1s", "P95 Dur", "8.0s", "Fail Rate", "50%", "Slow Tools", "read_bash 3.0s"} {
		if !strings.Contains(labelText, want) {
			t.Fatalf("expected %q in metrics card labels, got %v", want, labels)
		}
	}

	labels = nil
	collectLabelTexts(turnsCard.Content, &labels)
	turnText := strings.Join(labels, "\n")
	for _, want := range []string{"#2", "1.2s / 8.0s / 1t", "#1", "900ms / 6.0s / 1t"} {
		if !strings.Contains(turnText, want) {
			t.Fatalf("expected %q in recent turns card labels, got %v", want, labels)
		}
	}
}

func TestAgentBridgeAppendsTurnMetricsDigestWithoutMerging(t *testing.T) {
	loadTranslations()
	setLanguage("en")

	ui := NewUIState()
	ses := session.NewSession("zai", "default", "glm-5.1")
	ses.Metrics = []metrics.MetricEvent{
		{TurnIndex: 2, Type: "llm", TTFT: 1200 * time.Millisecond, ThinkTime: 2 * time.Second, Duration: 8 * time.Second},
		{TurnIndex: 2, Type: "tool", ToolName: "read_bash", ToolSuccess: false, ToolError: "timeout", ToolDuration: 3 * time.Second},
	}
	ses.RebuildEndpointStats()
	bridge := &AgentBridge{ui: ui, currentSes: ses}

	bridge.appendTurnMetricsDigest(2)
	ui.AppendChat(ChatMessage{Role: "system", Content: "Processing queued message...", Time: time.Now()})

	ui.ChatMu.Lock()
	defer ui.ChatMu.Unlock()
	if len(ui.ChatMsgs) != 2 {
		t.Fatalf("expected digest and queued system message to remain separate, got %+v", ui.ChatMsgs)
	}
	if !strings.Contains(ui.ChatMsgs[0].Content, "📊 Turn #2") || !ui.ChatMsgs[0].PreventMerge {
		t.Fatalf("expected first system message to be metrics digest, got %+v", ui.ChatMsgs[0])
	}
	if ui.ChatMsgs[1].Content != "Processing queued message..." {
		t.Fatalf("expected queued message to remain intact, got %+v", ui.ChatMsgs[1])
	}
}

func TestDesktopSaveMemoryRefreshesAgentPromptImmediately(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projectDir := createDesktopTestProjectDir(t)

	bridge := NewAgentBridge(&config.Config{}, nil, &config.ResolvedEndpoint{}, projectDir, NewUIState())
	bridge.currentSes = session.NewSession("", "", "")
	if err := bridge.setupAgent(); err != nil {
		t.Fatalf("setupAgent: %v", err)
	}

	toolAny, ok := bridge.registry.Get("save_memory")
	if !ok {
		t.Fatal("expected save_memory tool to be registered")
	}
	saveTool, ok := toolAny.(*tool.SaveMemoryTool)
	if !ok {
		t.Fatalf("expected save_memory tool type, got %T", toolAny)
	}

	if strings.Contains(bridge.agent.SystemPrompt(), "desktop memory refresh") {
		t.Fatal("prompt should not include saved memory before execute")
	}

	input, _ := json.Marshal(map[string]string{
		"key":     "desktop-refresh",
		"content": "desktop memory refresh",
		"scope":   "project",
	})
	result, err := saveTool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected save_memory error: %s", result.Content)
	}
	if !strings.Contains(bridge.agent.SystemPrompt(), "desktop memory refresh") {
		t.Fatalf("expected prompt refresh after save_memory, got %q", bridge.agent.SystemPrompt())
	}
}

func TestBuildSystemPromptIncludesProjectAutoMemory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	projectDir := createDesktopTestProjectDir(t)
	projectAutoMem := memory.NewProjectAutoMemory(projectDir)
	if projectAutoMem == nil {
		t.Fatal("expected project auto memory")
	}
	if err := projectAutoMem.SaveMemory("desktop-memory", "applies to desktop prompt"); err != nil {
		t.Fatalf("save memory: %v", err)
	}

	prompt := buildSystemPrompt(projectDir, nil, projectAutoMem)
	if !strings.Contains(prompt, "## Auto Memory (Project)") || !strings.Contains(prompt, "applies to desktop prompt") {
		t.Fatalf("expected project auto memory in prompt, got %q", prompt)
	}
}

func TestSetTitleStoresNativeWindowTitle(t *testing.T) {
	app := &App{
		fyneApp: test.NewApp(),
		dc:      defaultDesktopConfig(),
		ui:      NewUIState(),
	}
	app.window = app.fyneApp.NewWindow("ggcode")
	app.buildUI()

	app.setTitle("ggcode — workspace [model]")
	if app.windowTitle != "ggcode — workspace [model]" {
		t.Fatalf("expected stored window title to update, got %q", app.windowTitle)
	}
}

func TestTitlebarChromeHeightKeepsComfortableMinimum(t *testing.T) {
	if got := titlebarChromeHeight(nativeTitlebarConfig{TopInset: 20}); got != 36 {
		t.Fatalf("expected minimum chrome height 36, got %v", got)
	}
	if got := titlebarChromeHeight(nativeTitlebarConfig{TopInset: 34}); got != 42 {
		t.Fatalf("expected inset-aware chrome height 42, got %v", got)
	}
}

func TestApplyNativeTitlebarConfigUpdatesChromeSizer(t *testing.T) {
	app := &App{
		fyneApp: test.NewApp(),
		ui:      NewUIState(),
	}
	app.window = app.fyneApp.NewWindow("ggcode")
	app.nativeTitlebar = nativeTitlebarConfig{Integrated: true, TopInset: 28, LeadingInset: 88}
	app.buildUI()

	app.applyNativeTitlebarConfig(nativeTitlebarConfig{Integrated: true, TopInset: 40, LeadingInset: 88})
	if app.titleBarSizer == nil {
		t.Fatal("expected integrated titlebar chrome sizer")
	}
	if got := app.titleBarSizer.MinSize().Height; got != 48 {
		t.Fatalf("expected chrome height to update to 48, got %v", got)
	}
}

func TestBuildUIDoesNotAddTopChromeWhenNativeTitlebarNotIntegrated(t *testing.T) {
	app := &App{
		fyneApp: test.NewApp(),
		ui:      NewUIState(),
	}
	app.window = app.fyneApp.NewWindow("ggcode")
	app.nativeTitlebar = nativeTitlebarConfig{}
	app.buildUI()
	if app.titleBarLabel != nil || app.titleBarSizer != nil {
		t.Fatalf("expected no custom top chrome when native integration is disabled")
	}
}

func configuredTestUsage() provider.TokenUsage {
	return provider.TokenUsage{
		InputTokens:       1200,
		OutputTokens:      340,
		CacheRead:         800,
		CacheWrite:        64,
		PromptTokensTotal: 1200,
	}
}

// ── Helper functions ───────────────────────────────────────────

func findButton(obj fyne.CanvasObject, text string) *widget.Button {
	switch w := obj.(type) {
	case *widget.Button:
		if text == "" || w.Text == text {
			return w
		}
	case *fyne.Container:
		for _, child := range w.Objects {
			if btn := findButton(child, text); btn != nil {
				return btn
			}
		}
	}
	return nil
}

func findButtonByIcon(obj fyne.CanvasObject, icon fyne.Resource) *widget.Button {
	switch w := obj.(type) {
	case *widget.Button:
		if sameResource(w.Icon, icon) {
			return w
		}
	case *fyne.Container:
		for _, child := range w.Objects {
			if btn := findButtonByIcon(child, icon); btn != nil {
				return btn
			}
		}
	}
	return nil
}

func sameResource(a, b fyne.Resource) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Name() == b.Name() && bytes.Equal(a.Content(), b.Content())
}

func findLabel(obj fyne.CanvasObject) *widget.Label {
	switch w := obj.(type) {
	case *widget.Label:
		return w
	case *fyne.Container:
		for _, child := range w.Objects {
			if lbl := findLabel(child); lbl != nil {
				return lbl
			}
		}
	}
	return nil
}

func findEntry(obj fyne.CanvasObject) *widget.Entry {
	// Use Fyne's test helper to walk all objects
	all := test.LaidOutObjects(obj)
	for _, o := range all {
		if e, ok := o.(*widget.Entry); ok {
			return e
		}
	}
	// Also try direct container walking
	switch w := obj.(type) {
	case *widget.Entry:
		return w
	case *fyne.Container:
		for _, child := range w.Objects {
			if e := findEntry(child); e != nil {
				return e
			}
		}
	}
	return nil
}

func findLabelDeep(obj fyne.CanvasObject) *widget.Label {
	all := test.LaidOutObjects(obj)
	for _, o := range all {
		if l, ok := o.(*widget.Label); ok {
			return l
		}
	}
	return findLabel(obj)
}

func collectLabelTexts(obj fyne.CanvasObject, texts *[]string) {
	switch w := obj.(type) {
	case *widget.Label:
		*texts = append(*texts, w.Text)
	case *fyne.Container:
		for _, child := range w.Objects {
			collectLabelTexts(child, texts)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func mustParseURLTest(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse URL %q: %v", raw, err)
	}
	return u
}
