package main

import (
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

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

// ── FileTree tests ────────────────────────────────────────────

func TestFileTreeShowsWorkspaceFiles(t *testing.T) {
	root := createTestWorkspace(t)
	ft := NewFileTree(root, func(absPath string) {})

	// Verify tree widget was created
	if ft.tree == nil {
		t.Fatal("tree widget is nil")
	}

	// Force layout so child UIDs get loaded
	c := test.NewCanvas()
	c.SetContent(ft.Widget())
	c.Resize(fyne.NewSize(400, 600))

	// Verify children of root are loaded
	children := ft.childUIDs("")
	if len(children) == 0 {
		t.Fatal("root has no children")
	}

	// .git, node_modules, vendor should be skipped
	for _, child := range children {
		name := filepath.Base(string(child))
		if name == ".git" || name == "node_modules" || name == "vendor" {
			t.Errorf("skipped directory appeared in tree: %s", name)
		}
	}

	// Verify cmd and internal are branches
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

	// Simulate selecting a file
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

	// Selecting a directory should not trigger callback
	ft.onSelected("cmd")
	if clicked != "" {
		t.Errorf("onOpen should not fire for directory, got %q", clicked)
	}
}

func TestFileTreeRefresh(t *testing.T) {
	root := createTestWorkspace(t)
	ft := NewFileTree(root, nil)

	// Load initial children
	children1 := ft.childUIDs("")
	count1 := len(children1)

	// Create a new file
	os.WriteFile(filepath.Join(root, "newfile.txt"), []byte("test"), 0644)

	// Clear cache and refresh
	ft.entries = nil
	ft.Refresh()

	children2 := ft.childUIDs("")
	count2 := len(children2)

	if count2 <= count1 {
		t.Errorf("after adding file: children count %d, expected > %d", count2, count1)
	}
}

// ── FilePreview tests ─────────────────────────────────────────

func TestFilePreviewCodeFile(t *testing.T) {
	root := createTestWorkspace(t)
	app := &App{dc: &DesktopConfig{WorkDir: root}}

	goFile := filepath.Join(root, "cmd", "main.go")
	fp := NewFilePreview(app, goFile, 0, nil)

	if fp == nil {
		t.Fatal("preview is nil")
	}

	w := test.NewWindow(fp.Widget())
	w.Resize(fyne.NewSize(600, 400))

	// Verify the widget rendered without panicking
	// (if we get here, buildCodePreview succeeded)
}

func TestFilePreviewMarkdownFile(t *testing.T) {
	root := createTestWorkspace(t)
	app := &App{dc: &DesktopConfig{WorkDir: root}}

	mdFile := filepath.Join(root, "README.md")
	fp := NewFilePreview(app, mdFile, 0, nil)

	w := test.NewWindow(fp.Widget())
	w.Resize(fyne.NewSize(600, 400))
	_ = w
}

func TestFilePreviewBinaryFile(t *testing.T) {
	root := createTestWorkspace(t)
	app := &App{dc: &DesktopConfig{WorkDir: root}}

	binFile := filepath.Join(root, "binary.dat")
	fp := NewFilePreview(app, binFile, 0, nil)

	w := test.NewWindow(fp.Widget())
	w.Resize(fyne.NewSize(600, 400))
	_ = w
}

func TestFilePreviewDirectoryError(t *testing.T) {
	root := createTestWorkspace(t)
	app := &App{dc: &DesktopConfig{WorkDir: root}}

	fp := NewFilePreview(app, root, 0, nil)
	w := test.NewWindow(fp.Widget())
	w.Resize(fyne.NewSize(600, 400))
}

func TestFilePreviewNonexistentFile(t *testing.T) {
	app := &App{dc: &DesktopConfig{WorkDir: "/tmp"}}

	fp := NewFilePreview(app, "/nonexistent/file.go", 0, nil)
	w := test.NewWindow(fp.Widget())
	w.Resize(fyne.NewSize(600, 400))
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

	// Find and tap close button (CancelIcon button in header)
	closeBtn := findButton(obj, "")
	if closeBtn != nil {
		test.Tap(closeBtn)
		if !closed {
			t.Error("close callback was not called")
		}
	}
}

// ── App showFilePreview / closeFilePreview ────────────────────

func TestAppShowAndCloseFilePreview(t *testing.T) {
	root := createTestWorkspace(t)
	app := &App{
		fyneApp: test.NewApp(),
		dc:      &DesktopConfig{WorkDir: root},
	}
	w := app.fyneApp.NewWindow("test")

	// Create a minimal chat view so closeFilePreview doesn't crash
	chatLabel := widget.NewLabel("chat")
	app.chatViewObj = chatLabel
	app.sidebarObj = widget.NewLabel("sidebar")
	app.split = container.NewHSplit(chatLabel, app.sidebarObj)
	app.content = container.NewHBox(app.split)
	w.SetContent(app.content)
	w.Resize(fyne.NewSize(800, 600))

	// Show preview
	goFile := filepath.Join(root, "cmd", "main.go")
	app.showFilePreview(goFile, 0)

	if app.filePreview == nil {
		t.Fatal("filePreview should be set after showFilePreview")
	}

	// Close preview
	app.closeFilePreview()

	if app.filePreview != nil {
		t.Error("filePreview should be nil after closeFilePreview")
	}
}

// ── Helper functions ──────────────────────────────────────────

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
