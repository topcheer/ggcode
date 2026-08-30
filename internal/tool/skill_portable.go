package tool

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/commands"
)

// SkillManifest is embedded inside a .ggskill bundle to carry metadata.
type SkillManifest struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Version     string    `json:"version"`
	ExportedAt  time.Time `json:"exported_at"`
	ExportedBy  string    `json:"exported_by,omitempty"`
	// Files lists all files included in the bundle (relative paths).
	Files []string `json:"files"`
}

// MaxSkillBundleSize limits how large a skill bundle can be (16 MB).
const MaxSkillBundleSize = 16 << 20

// HTTPTimeout for downloading skill bundles from URLs.
const skillHTTPTimeout = 30 * time.Second

// --- Export ---

// exportSkill packages the named skill (including companion files) into a
// .ggskill tarball at outputPath. Returns the manifest and the output path.
func exportSkill(cmd *commands.Command, outputPath string) (*SkillManifest, string, error) {
	if cmd == nil {
		return nil, "", fmt.Errorf("skill is nil")
	}
	skillDir := skillDirFromPath(cmd.Path)
	if skillDir == "" {
		// Single-file skill: bundle just the .md file.
		return exportSingleFileSkill(cmd, outputPath)
	}

	// Directory-based skill: walk the directory recursively and bundle ALL
	// files (#869: ReadDir + IsDir-skip silently dropped subdirectories like
	// templates/, scripts/, assets/ - the export reported success but the
	// bundle could never round-trip a skill with companion directories).
	var files []string
	var total int64
	walkErr := filepath.WalkDir(skillDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(skillDir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		// Skip hidden files and lock files (any depth).
		base := filepath.Base(rel)
		if strings.HasPrefix(base, ".") || base == "go.sum" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Size() > MaxSkillBundleSize {
			return fmt.Errorf("file %s is too large (%d bytes, max %d)", rel, info.Size(), MaxSkillBundleSize)
		}
		// #1341: symmetric total cap with import (readBundle). Without it
		// an exported multi-file bundle could exceed the import-side
		// compressed-stream limit and fail to round-trip.
		total += info.Size()
		if total > MaxSkillBundleSize {
			return fmt.Errorf("skill directory total size %d exceeds max %d", total, MaxSkillBundleSize)
		}
		files = append(files, rel)
		return nil
	})
	if walkErr != nil {
		return nil, "", walkErr
	}

	if len(files) == 0 {
		return nil, "", fmt.Errorf("skill directory %s has no files to export", skillDir)
	}

	manifest := &SkillManifest{
		Name:        cmd.Name,
		Description: cmd.Description,
		Version:     cmd.Version,
		ExportedAt:  time.Now().UTC(),
		ExportedBy:  "ggcode",
		Files:       files,
	}

	if err := writeBundle(outputPath, manifest, skillDir, files); err != nil {
		return nil, "", err
	}
	return manifest, outputPath, nil
}

func exportSingleFileSkill(cmd *commands.Command, outputPath string) (*SkillManifest, string, error) {
	manifest := &SkillManifest{
		Name:        cmd.Name,
		Description: cmd.Description,
		Version:     cmd.Version,
		ExportedAt:  time.Now().UTC(),
		ExportedBy:  "ggcode",
		Files:       []string{filepath.Base(cmd.Path)},
	}

	tmpDir, err := os.MkdirTemp("", "ggcode-skill-export-")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(tmpDir)

	// Copy the single file into a temp dir for consistent bundling.
	dst := filepath.Join(tmpDir, filepath.Base(cmd.Path))
	if err := copyFile(cmd.Path, dst); err != nil {
		return nil, "", err
	}

	if err := writeBundle(outputPath, manifest, tmpDir, manifest.Files); err != nil {
		return nil, "", err
	}
	return manifest, outputPath, nil
}

func writeBundle(outputPath string, manifest *SkillManifest, baseDir string, files []string) (err error) {
	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("cannot create output file: %w", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	// #1341: gzip/tar buffer internally - a full disk only surfaces at
	// Close. Capture Close errors explicitly (reverse order) so a
	// truncated bundle is never reported as a successful export.
	defer func() {
		if cerr := tw.Close(); cerr != nil && err == nil {
			err = cerr
		}
		if cerr := gw.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	// Write manifest.json first.
	manifestData, _ := json.MarshalIndent(manifest, "", "  ")
	if err := writeTarEntry(tw, manifestFileName, manifestData); err != nil {
		return err
	}

	// Write each skill file.
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(baseDir, name))
		if err != nil {
			return fmt.Errorf("cannot read %s: %w", name, err)
		}
		if err := writeTarEntry(tw, name, data); err != nil {
			return err
		}
	}
	return nil
}

// --- Import ---

// importSkill reads a .ggskill bundle from sourcePath (local file or URL),
// extracts it into destDir/skillName/, and returns the extracted manifest.
func importSkill(sourcePath, destDir string) (*SkillManifest, string, error) {
	reader, cleanup, err := openSkillSource(sourcePath)
	if err != nil {
		return nil, "", err
	}
	defer cleanup()

	manifest, files, err := readBundle(reader)
	if err != nil {
		return nil, "", err
	}
	if len(files) == 0 {
		return nil, "", fmt.Errorf("skill bundle contains no files")
	}

	skillName := sanitizeSkillName(manifest.Name)
	if skillName == "" {
		return nil, "", fmt.Errorf("skill bundle has invalid name in manifest")
	}

	skillDir := filepath.Join(destDir, skillName)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return nil, "", fmt.Errorf("cannot create skill directory: %w", err)
	}

	for name, data := range files {
		// Security: prevent path traversal - allow relative subpaths
		// (skills may carry templates/, scripts/ companion dirs, #869),
		// but reject absolute paths, parent refs, and any component
		// traversal.
		clean := filepath.ToSlash(filepath.Clean(name))
		if clean != name || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "../") || clean == ".." || strings.Contains(clean, "/../") {
			return nil, "", fmt.Errorf("refusing to extract file with unsafe path: %s", name)
		}
		if filepath.Base(clean) == manifestFileName {
			continue // already parsed
		}
		outPath := filepath.Join(skillDir, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			return nil, "", fmt.Errorf("cannot create directory for %s: %w", clean, err)
		}
		if err := os.WriteFile(outPath, data, 0644); err != nil {
			return nil, "", fmt.Errorf("cannot write %s: %w", clean, err)
		}
	}

	return manifest, skillDir, nil
}

// openSkillSource opens a .ggskill bundle from a local path or HTTP(S) URL.
func openSkillSource(source string) (io.Reader, func(), error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, nil, fmt.Errorf("source path is empty")
	}

	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		client := &http.Client{Timeout: skillHTTPTimeout}
		resp, err := client.Get(source)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot download skill from %s: %w", source, err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, nil, fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
		}
		return resp.Body, func() { resp.Body.Close() }, nil
	}

	// Expand ~ prefix for local paths.
	if strings.HasPrefix(source, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			source = filepath.Join(home, source[2:])
		}
	}

	f, err := os.Open(source)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot open %s: %w", source, err)
	}
	return f, func() { f.Close() }, nil
}

// readBundle reads a .ggskill tar.gz and returns the manifest + file contents.
func readBundle(reader io.Reader) (*SkillManifest, map[string][]byte, error) {
	// Wrap in LimitedReader to prevent decompression bombs.
	lr := &io.LimitedReader{R: reader, N: MaxSkillBundleSize + 1}

	gw, err := gzip.NewReader(lr)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid gzip format: %w", err)
	}
	defer gw.Close()

	tw := tar.NewReader(gw)
	files := make(map[string][]byte)
	var manifest *SkillManifest
	var total int64

	for {
		header, err := tw.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("error reading bundle: %w", err)
		}

		// Skip directories.
		if header.Typeflag != tar.TypeReg {
			continue
		}

		name := header.Name
		if name == "." || name == "" {
			continue
		}

		data, err := io.ReadAll(io.LimitReader(tw, MaxSkillBundleSize+1))
		if err != nil {
			return nil, nil, fmt.Errorf("error reading %s from bundle: %w", name, err)
		}
		if int64(len(data)) > MaxSkillBundleSize {
			return nil, nil, fmt.Errorf("bundle exceeds max size of %d bytes", MaxSkillBundleSize)
		}
		// #1341: cumulative decompressed cap. The compressed-stream
		// LimitedReader (16MB) does NOT bound total decompressed bytes: a
		// ~1000x-compressible gzip bomb (all-zero tar entries, each under
		// the per-file cap) could otherwise expand to tens of GB resident
		// in the files map. #import:<url> makes this remotely deliverable.
		total += int64(len(data))
		if total > MaxSkillBundleSize {
			return nil, nil, fmt.Errorf("bundle total decompressed size exceeds %d bytes", MaxSkillBundleSize)
		}

		if name == manifestFileName {
			var m SkillManifest
			if err := json.Unmarshal(data, &m); err != nil {
				return nil, nil, fmt.Errorf("invalid manifest.json: %w", err)
			}
			manifest = &m
		} else {
			files[name] = data
		}
	}

	if manifest == nil {
		return nil, nil, fmt.Errorf("bundle is missing manifest.json -- not a valid .ggskill file")
	}
	return manifest, files, nil
}

// --- Helpers ---

const manifestFileName = "manifest.json"

// skillDirFromPath returns the parent directory if the skill is directory-based,
// or empty string if it's a standalone .md file.
func skillDirFromPath(skillPath string) string {
	if skillPath == "" {
		return ""
	}
	if filepath.Base(skillPath) == "SKILL.md" {
		return filepath.Dir(skillPath)
	}
	return ""
}

func writeTarEntry(tw *tar.Writer, name string, data []byte) error {
	header := &tar.Header{
		Name:     name,
		Size:     int64(len(data)),
		Mode:     0644,
		ModTime:  time.Now(),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("cannot write tar header for %s: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("cannot write tar content for %s: %w", name, err)
	}
	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

func sanitizeSkillName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ToLower(name)
	var sb strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			sb.WriteRune(r)
		} else if r == ' ' {
			sb.WriteRune('-')
		}
	}
	return sb.String()
}
