package tool

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/commands"
)

// helper: create a directory-based skill on disk
func createTestSkillDir(t *testing.T, parent, name string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	skillContent := "---\nname: " + name + "\ndescription: test skill\nversion: 1.0.0\n---\n# " + name + "\nThis is a test skill.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "helper.go"), []byte("package helper\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestExportSkill_DirectoryBased(t *testing.T) {
	tmp := t.TempDir()
	skillDir := createTestSkillDir(t, tmp, "my-skill")
	outPath := filepath.Join(tmp, "my-skill.ggskill")

	cmd := &commands.Command{
		Name:    "my-skill",
		Path:    filepath.Join(skillDir, "SKILL.md"),
		Version: "1.0.0",
	}

	manifest, path, err := exportSkill(cmd, outPath)
	if err != nil {
		t.Fatalf("exportSkill failed: %v", err)
	}
	if path != outPath {
		t.Errorf("expected path %s, got %s", outPath, path)
	}
	if manifest.Name != "my-skill" {
		t.Errorf("expected manifest name 'my-skill', got %q", manifest.Name)
	}
	if manifest.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", manifest.Version)
	}
	if len(manifest.Files) < 2 {
		t.Errorf("expected at least 2 files in manifest, got %d", len(manifest.Files))
	}
	// Verify the file exists
	if _, err := os.Stat(outPath); os.IsNotExist(err) {
		t.Error("output file was not created")
	}
}

func TestExportSkill_SingleFile(t *testing.T) {
	tmp := t.TempDir()
	skillPath := filepath.Join(tmp, "standalone.md")
	content := "---\nname: standalone\ndescription: standalone skill\n---\n# standalone\nBody here.\n"
	if err := os.WriteFile(skillPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(tmp, "standalone.ggskill")

	cmd := &commands.Command{
		Name: "standalone",
		Path: skillPath,
	}
	manifest, _, err := exportSkill(cmd, outPath)
	if err != nil {
		t.Fatalf("exportSkill failed: %v", err)
	}
	if manifest.Name != "standalone" {
		t.Errorf("expected name 'standalone', got %q", manifest.Name)
	}
	if len(manifest.Files) != 1 {
		t.Errorf("expected 1 file, got %d", len(manifest.Files))
	}
}

func TestImportSkill_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	skillDir := createTestSkillDir(t, tmp, "exportable")
	outPath := filepath.Join(tmp, "exportable.ggskill")

	cmd := &commands.Command{
		Name:    "exportable",
		Path:    filepath.Join(skillDir, "SKILL.md"),
		Version: "2.1.0",
	}
	// Export
	manifest, _, err := exportSkill(cmd, outPath)
	if err != nil {
		t.Fatalf("exportSkill failed: %v", err)
	}
	if len(manifest.Files) < 2 {
		t.Fatalf("expected at least 2 files, got %d", len(manifest.Files))
	}

	// Import to a different directory
	destDir := filepath.Join(tmp, "imported")
	importedManifest, importedDir, err := importSkill(outPath, destDir)
	if err != nil {
		t.Fatalf("importSkill failed: %v", err)
	}
	if importedManifest.Name != "exportable" {
		t.Errorf("expected imported name 'exportable', got %q", importedManifest.Name)
	}
	if importedManifest.Version != "2.1.0" {
		t.Errorf("expected version '2.1.0', got %q", importedManifest.Version)
	}
	// Verify SKILL.md exists in imported dir
	if _, err := os.Stat(filepath.Join(importedDir, "SKILL.md")); os.IsNotExist(err) {
		t.Error("SKILL.md not found in imported skill directory")
	}
	// Verify helper.go exists
	if _, err := os.Stat(filepath.Join(importedDir, "helper.go")); os.IsNotExist(err) {
		t.Error("helper.go not found in imported skill directory")
	}
}

func TestImportSkill_PathTraversalBlocked(t *testing.T) {
	tmp := t.TempDir()
	// Craft a malicious bundle with a path-traversal entry
	outPath := filepath.Join(tmp, "malicious.ggskill")
	f, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	// Write a fake manifest
	manifestJSON := []byte(`{"name":"evil","files":["../../etc/passwd"]}`)
	writeTarEntry(tw, "manifest.json", manifestJSON)

	// Write the malicious file
	writeTarEntry(tw, "../../etc/passwd", []byte("hacked"))

	tw.Close()
	gw.Close()
	f.Close()

	destDir := filepath.Join(tmp, "dest")
	_, _, err = importSkill(outPath, destDir)
	if err == nil {
		t.Error("expected error for path traversal, got nil")
	}
}

func TestImportSkill_InvalidBundle(t *testing.T) {
	tmp := t.TempDir()
	// Write a random file that is not a valid gzip
	badPath := filepath.Join(tmp, "bad.ggskill")
	if err := os.WriteFile(badPath, []byte("not a gzip file"), 0644); err != nil {
		t.Fatal(err)
	}
	_, _, err := importSkill(badPath, tmp)
	if err == nil {
		t.Error("expected error for invalid bundle, got nil")
	}
}

func TestImportSkill_MissingManifest(t *testing.T) {
	tmp := t.TempDir()
	outPath := filepath.Join(tmp, "nomanifest.ggskill")
	f, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	writeTarEntry(tw, "SKILL.md", []byte("# some skill"))
	tw.Close()
	gw.Close()
	f.Close()

	_, _, err = importSkill(outPath, tmp)
	if err == nil {
		t.Error("expected error for missing manifest, got nil")
	}
}

func TestSanitizeSkillName(t *testing.T) {
	tests := []struct {
		input, expected string
	}{
		{"My Skill Name", "my-skill-name"},
		{"already-ok", "already-ok"},
		{"UPPER", "upper"},
		{"café résumé", "caf-rsum"},
		{"  spaces  ", "spaces"},
		{"name@v1.0!", "namev10"},
		{"", ""},
	}
	for _, tt := range tests {
		got := sanitizeSkillName(tt.input)
		if got != tt.expected {
			t.Errorf("sanitizeSkillName(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestSkillDirFromPath(t *testing.T) {
	// Directory-based skill
	got := skillDirFromPath("/home/user/.ggcode/skills/my-skill/SKILL.md")
	if got != "/home/user/.ggcode/skills/my-skill" {
		t.Errorf("expected skill dir, got %q", got)
	}
	// Standalone file
	got = skillDirFromPath("/home/user/.ggcode/commands/standalone.md")
	if got != "" {
		t.Errorf("expected empty for standalone file, got %q", got)
	}
	// Empty path
	got = skillDirFromPath("")
	if got != "" {
		t.Errorf("expected empty for empty input, got %q", got)
	}
}
