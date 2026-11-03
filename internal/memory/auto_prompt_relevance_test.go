package memory

import (
	"strings"
	"testing"
)

func TestRelevanceGate_RelatedStaysInline(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	writeMem(t, dir, "build-process-impl", "Run: make build\nUse Go 1.26 for the build.")

	inline, _, err := am.LoadForPromptForTask("fix the Go build failure in cmd/ggcode")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(inline) != 1 || inline[0].Key != "build-process-impl" {
		t.Fatalf("expected related entry inline, got %v", inline)
	}
}

func TestRelevanceGate_UnrelatedDegradesToIndex(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	writeMem(t, dir, "docker-deploy-impl", "deploy: docker push registry.example/team/app")

	inline, indexOnly, err := am.LoadForPromptForTask("fix the flaky postgres migration test")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(inline) != 0 {
		t.Fatalf("unrelated entry must not inline, got %v", inline)
	}
	found := false
	for _, k := range indexOnly {
		if k == "docker-deploy-impl" {
			found = true
		}
	}
	if !found {
		t.Fatalf("degraded entry must stay in index, got %v", indexOnly)
	}
}

func TestRelevanceGate_EmptyTaskBypasses(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	writeMem(t, dir, "docker-deploy-impl", "deploy: docker push registry.example/team/app")

	inline, _, err := am.LoadForPrompt()
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(inline) != 1 {
		t.Fatalf("empty task must keep legacy full-inline behavior, got %v", inline)
	}
}

func TestRelevanceGate_MixedAndShortTask(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	writeMem(t, dir, "build-process-impl", "make build with Go toolchain")
	writeMem(t, dir, "docker-deploy-impl", "docker push registry.example/team/app")

	inline, indexOnly, err := am.LoadForPromptForTask("update the make build target")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(inline) != 1 || !strings.Contains(inline[0].Key, "build-process") {
		t.Fatalf("expected only build entry inline, got %v", inline)
	}
	if len(indexOnly) != 1 || !strings.Contains(indexOnly[0], "docker-deploy") {
		t.Fatalf("expected docker entry index-only, got %v", indexOnly)
	}

	dir2 := t.TempDir()
	am2 := &AutoMemory{dir: dir2}
	writeMem(t, dir2, "build-process-impl", "make build with Go toolchain")
	writeMem(t, dir2, "docker-deploy-impl", "docker push registry.example/team/app")
	inline2, index2, err := am2.LoadForPromptForTask("docker")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(inline2) != 1 || !strings.Contains(inline2[0].Key, "docker-deploy") {
		t.Fatalf("single-token task: sharing entry inlines, got %v", inline2)
	}
	if len(index2) != 1 || !strings.Contains(index2[0], "build-process") {
		t.Fatalf("single-token task: non-sharing entry degrades, got %v", index2)
	}
}
