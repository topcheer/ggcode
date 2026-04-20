package extract

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"fmt"
	"io"
	"strings"
)

// archiveExtractor extracts text from archive files.
type archiveExtractor struct {
	subFormat string // "zip", "tar", "tar.gz", "tar.bz2", "tar.xz"
}

func (e *archiveExtractor) Format() string { return e.subFormat }

const (
	maxArchiveEntrySize  = 1 * 1024 * 1024 // 1MB per entry
	maxArchiveTotalLines = 2000
	maxArchiveDepth      = 2
	maxArchiveEntries    = 500 // max files to read from archive
)

func (e *archiveExtractor) Extract(data []byte) (TextResult, error) {
	var files []archiveFile
	var err error

	switch e.subFormat {
	case "zip":
		files, err = listZip(data)
	case "tar":
		files, err = listTar(data, false, "")
	case "tar.gz", "tgz":
		files, err = listTarGz(data)
	case "tar.bz2":
		files, err = listTarBz2(data)
	case "tar.xz":
		files, err = listTarXz(data)
	default:
		return TextResult{}, fmt.Errorf("unsupported archive format: %s", e.subFormat)
	}
	if err != nil {
		return TextResult{}, err
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "[Archive: %s format, %d files]\n\n", e.subFormat, len(files))

	if len(files) >= maxArchiveEntries && e.subFormat == "zip" {
		total := totalZipFiles(data)
		if total > len(files) {
			fmt.Fprintf(&buf, "[Showing first %d of %d files]\n\n", len(files), total)
		}
	}

	for _, f := range files {
		if buf.Len() > maxArchiveTotalLines*80 { // rough line estimate
			buf.WriteString("... (truncated, too many files)\n")
			break
		}
		name := f.name
		if strings.HasPrefix(name, "./") {
			name = name[2:]
		}
		if name == "" {
			continue
		}

		sizeStr := formatSize(len(f.data))
		fmt.Fprintf(&buf, "--- %s (%s) ---\n", name, sizeStr)

		ext := extOf(name)

		// Nested archive
		if isArchiveExt(ext) && maxArchiveDepth > 1 {
			nested := extractArchiveContent(f.data, ext)
			if nested != "" {
				// Indent nested content
				for _, line := range strings.Split(nested, "\n") {
					buf.WriteString("  ")
					buf.WriteString(line)
					buf.WriteByte('\n')
				}
			}
			continue
		}

		// Known document format
		if ext != "" && defaultRegistry.Get(ext) != nil && !isArchiveExt(ext) {
			result, err := Extract(name, f.data)
			if err == nil && result.Text != "" {
				text := result.Text
				if len(text) > maxArchiveEntrySize {
					text = text[:maxArchiveEntrySize] + "\n... (truncated)"
				}
				buf.WriteString(text)
				buf.WriteByte('\n')
			}
			continue
		}

		// Image/binary: skip
		if isImageExt(ext) || isBinaryExt(ext) {
			buf.WriteString("[Binary: skipped]\n\n")
			continue
		}

		// Text file or unknown: try to read as text
		if len(f.data) > maxArchiveEntrySize {
			fmt.Fprintf(&buf, "[File too large: %s]\n\n", formatSize(len(f.data)))
			continue
		}
		if isLikelyText(f.data) {
			text := string(f.data)
			text = strings.TrimSpace(text)
			if text != "" {
				buf.WriteString(text)
				buf.WriteByte('\n')
			}
		} else {
			buf.WriteString("[Binary: skipped]\n")
		}
		buf.WriteByte('\n')
	}

	return TextResult{
		Text:   strings.TrimSpace(buf.String()),
		Format: e.subFormat,
	}, nil
}

// archiveFile represents a file inside an archive.
type archiveFile struct {
	name string
	data []byte
}

func listZip(data []byte) ([]archiveFile, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open ZIP: %w", err)
	}
	totalFiles := 0
	for _, f := range r.File {
		if !f.FileInfo().IsDir() {
			totalFiles++
		}
	}
	var files []archiveFile
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if len(files) >= maxArchiveEntries {
			break
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		d, err := io.ReadAll(io.LimitReader(rc, maxArchiveEntrySize+1))
		rc.Close()
		if err != nil {
			continue
		}
		files = append(files, archiveFile{name: f.Name, data: d})
	}
	return files, nil
}

// totalZipFiles returns total file count (excluding dirs) without reading data.
func totalZipFiles(data []byte) int {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return 0
	}
	n := 0
	for _, f := range r.File {
		if !f.FileInfo().IsDir() {
			n++
		}
	}
	return n
}

func listTarGz(data []byte) ([]archiveFile, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()
	return listTarFromReader(gz)
}

func listTarBz2(data []byte) ([]archiveFile, error) {
	br := bzip2.NewReader(bytes.NewReader(data))
	return listTarFromReader(br)
}

func listTarXz(data []byte) ([]archiveFile, error) {
	// xz requires external dependency; try decompressing manually
	// For now, return a helpful error
	return nil, fmt.Errorf("tar.xz support requires xz decompression (not yet available)")
}

func listTar(data []byte, _ bool, _ string) ([]archiveFile, error) {
	return listTarFromReader(bytes.NewReader(data))
}

func listTarFromReader(r io.Reader) ([]archiveFile, error) {
	tr := tar.NewReader(r)
	var files []archiveFile
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != 0 {
			continue
		}
		if len(files) >= maxArchiveEntries {
			// Drain remaining headers to count total files
			for {
				_, err := tr.Next()
				if err != nil {
					break
				}
			}
			break
		}
		d, err := io.ReadAll(io.LimitReader(tr, maxArchiveEntrySize+1))
		if err != nil {
			continue
		}
		files = append(files, archiveFile{name: hdr.Name, data: d})
	}
	return files, nil
}

// extractArchiveContent recursively extracts text from a nested archive.
func extractArchiveContent(data []byte, ext string) string {
	subFmt := ""
	switch ext {
	case ".zip":
		subFmt = "zip"
	case ".tar":
		subFmt = "tar"
	case ".tar.gz", ".tgz":
		subFmt = "tar.gz"
	case ".tar.bz2":
		subFmt = "tar.bz2"
	default:
		return ""
	}
	e := &archiveExtractor{subFormat: subFmt}
	result, err := e.Extract(data)
	if err != nil {
		return ""
	}
	return result.Text
}

// isArchiveExt checks if an extension is an archive format.
func isArchiveExt(ext string) bool {
	switch ext {
	case ".zip", ".tar", ".tar.gz", ".tgz", ".tar.bz2", ".tar.xz", ".rar", ".7z":
		return true
	}
	return false
}

// isImageExt checks if an extension is an image format.
func isImageExt(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".ico", ".svg",
		".tiff", ".tif", ".heic", ".heif":
		return true
	}
	return false
}

// isBinaryExt checks if an extension is a known binary format we don't extract.
func isBinaryExt(ext string) bool {
	switch ext {
	case ".exe", ".dll", ".so", ".dylib", ".o", ".a", ".lib",
		".class", ".jar", ".war", ".pyc", ".pyd", ".wasm",
		".woff", ".woff2", ".ttf", ".otf", ".eot",
		".mp3", ".mp4", ".avi", ".mov", ".mkv", ".flv",
		".wav", ".flac", ".aac", ".ogg",
		".sqlite", ".db", ".iso", ".dmg", ".pkg", ".deb", ".rpm":
		return true
	}
	return false
}

// isLikelyText checks if data appears to be text (no NULL bytes in first 8KB).
func isLikelyText(data []byte) bool {
	check := data
	if len(check) > 8192 {
		check = check[:8192]
	}
	for _, b := range check {
		if b == 0 {
			return false
		}
	}
	return true
}

func formatSize(n int) string {
	if n >= 1024*1024 {
		return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
	}
	if n >= 1024 {
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	}
	return fmt.Sprintf("%dB", n)
}
