package tmux

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInferDefaultLayoutGoProject(t *testing.T) {
	workspace := t.TempDir()
	writeTestFile(t, filepath.Join(workspace, "go.mod"), "module example.com/app\n")
	writeTestFile(t, filepath.Join(workspace, "Makefile"), "verify-ci:\n\tgo test ./...\n")

	layout := InferDefaultLayout(workspace)
	if !layoutHas(layout, "shell", "") || !layoutHas(layout, "test", "go test -tags goolm ./...") || !layoutHas(layout, "verify", "make verify-ci") {
		t.Fatalf("Go layout = %+v", layout)
	}
}

func TestInferDefaultLayoutNodeProject(t *testing.T) {
	workspace := t.TempDir()
	writeTestFile(t, filepath.Join(workspace, "package.json"), `{"scripts":{"dev":"vite","test":"vitest"}}`)

	layout := InferDefaultLayout(workspace)
	if !layoutHas(layout, "shell", "") || !layoutHas(layout, "dev", "npm run dev") || !layoutHas(layout, "test", "npm test") {
		t.Fatalf("Node layout = %+v", layout)
	}
}

func TestInferDefaultLayoutFlutterProject(t *testing.T) {
	workspace := t.TempDir()
	writeTestFile(t, filepath.Join(workspace, "pubspec.yaml"), "name: app\n")

	layout := InferDefaultLayout(workspace)
	if !layoutHas(layout, "shell", "") || !layoutHas(layout, "analyze", "flutter analyze") || !layoutHas(layout, "test", "flutter test") {
		t.Fatalf("Flutter layout = %+v", layout)
	}
}

func TestInferDefaultLayoutGenericProject(t *testing.T) {
	layout := InferDefaultLayout(t.TempDir())
	if len(layout) != 1 || layout[0].Purpose != "shell" || layout[0].Command != "" {
		t.Fatalf("Generic layout = %+v, want shell only", layout)
	}
}

func TestManagerSetupInfersAndPersistsDefaultLayout(t *testing.T) {
	workspace := t.TempDir()
	writeTestFile(t, filepath.Join(workspace, "go.mod"), "module example.com/app\n")
	storePath := filepath.Join(t.TempDir(), "tmux-panes.json")
	mgr := NewManagerWithStorePath(&Client{bin: "/nonexistent-tmux-for-test"}, workspace, storePath)
	created, err := mgr.SetupLayout(context.Background(), "default")
	if err == nil {
		t.Fatalf("expected setup to fail when not inside tmux, got created=%+v", created)
	}
	layout := mgr.Layout("default")
	if !layoutHas(layout, "test", "go test -tags goolm ./...") {
		t.Fatalf("expected inferred default layout to be saved, got %+v", layout)
	}
	reloaded := NewManagerWithStorePath(NewClient(), workspace, storePath)
	if !layoutHas(reloaded.Layout("default"), "test", "go test -tags goolm ./...") {
		t.Fatalf("expected inferred layout to persist, got %+v", reloaded.Layout("default"))
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func layoutHas(layout []LayoutPane, purpose, command string) bool {
	for _, pane := range layout {
		if pane.Purpose == purpose && pane.Command == command {
			return true
		}
	}
	return false
}
