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
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/util"
)

type archiveExtractor struct {
	subFormat string
	depth     int
}

func (e *archiveExtractor) Format() string { return e.subFormat }

const (
	maxArchiveEntrySize  = 1 * 1024 * 1024
	maxArchiveTotalLines = 2000
	maxArchiveDepth      = 2
	maxArchiveEntries    = 500
)

func (e *archiveExtractor) Extract(data []byte) (TextResult, error) {
	var files []archiveFile
	var total int
	var truncated bool
	var corrupt bool
	var err error

	switch e.subFormat {
	case "zip":
		files, err = listZip(data)
		if err == nil {
			total = len(files)
			if len(files) >= maxArchiveEntries {
				if t := totalZipFiles(data); t > len(files) {
					total, truncated = t, true
				}
			}
		}
	case "tar":
		files, total, truncated, corrupt, err = listTar(data)
	case "tar.gz", "tgz":
		files, total, truncated, corrupt, err = listTarGz(data)
	case "tar.bz2":
		files, total, truncated, corrupt, err = listTarBz2(data)
	case "tar.xz":
		files, total, truncated, corrupt, err = listTarXz(data)
	default:
		return TextResult{}, fmt.Errorf("unsupported archive format: %s", e.subFormat)
	}
	if err != nil {
		return TextResult{}, err
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "[Archive: %s format, %d files]\n\n", e.subFormat, len(files))

	if truncated && total > len(files) {
		fmt.Fprintf(&buf, "[Showing first %d of %d files]\n\n", len(files), total)
	} else if truncated {
		buf.WriteString("[Truncated: archive exceeds extraction limits]\n\n")
	}

	if corrupt {
		fmt.Fprintf(&buf, "[Corrupt archive: stream error after %d files; listing is partial]\n\n", len(files))
	}

	for _, f := range files {
		if buf.Len() > maxArchiveTotalLines*80 {
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

		if isArchiveExt(ext) {
			nested := extractArchiveContentDepth(f.data, ext, e.depth+1)
			if nested != "" {

				for _, line := range strings.Split(nested, "\n") {
					buf.WriteString("  ")
					buf.WriteString(line)
					buf.WriteByte('\n')
				}
			} else {

				fmt.Fprintf(&buf, "[Nested archive content not available]\n")
			}
			continue
		}

		if ext != "" && defaultRegistry.Get(ext) != nil && !isArchiveExt(ext) {
			result, err := Extract(name, f.data)

			if err != nil {
				fmt.Fprintf(&buf, "[Extraction failed: %v]\n\n", err)
			} else if result.Text != "" {
				text := result.Text
				if len(text) > maxArchiveEntrySize {

					cut := util.SnapToRuneStart(text, maxArchiveEntrySize)
					text = text[:cut] + "\n... (truncated)"
				}
				buf.WriteString(text)
				buf.WriteByte('\n')
			}
			continue
		}

		if isImageExt(ext) || isBinaryExt(ext) {
			buf.WriteString("[Binary: skipped]\n\n")
			continue
		}

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

type archiveFile struct {
	name string
	data []byte
}

const (
	maxZipEntryRead = 64 * 1024
	maxZipTotalRead = 32 * 1024 * 1024
)

func zipNeedsFullData(name string) bool {
	ext := extOf(name)

	if ext == ".tar.xz" || ext == ".rar" || ext == ".7z" {
		return false
	}
	if isArchiveExt(ext) {
		return true
	}
	return ext != "" && defaultRegistry.Get(ext) != nil
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
	var totalRead int64
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if len(files) >= maxArchiveEntries {
			break
		}
		limit := int64(maxZipEntryRead)
		if zipNeedsFullData(f.Name) {
			limit = maxArchiveEntrySize
		}
		if totalRead >= maxZipTotalRead {

			files = append(files, archiveFile{name: f.Name})
			continue
		}
		rc, err := f.Open()
		if err != nil {

			files = append(files, archiveFile{name: f.Name, data: []byte(fmt.Sprintf("[unreadable: %v]", err))})
			continue
		}

		d, err := io.ReadAll(io.LimitReader(rc, limit+1))
		rc.Close()
		if err != nil {
			files = append(files, archiveFile{name: f.Name, data: []byte(fmt.Sprintf("[unreadable: %v]", err))})
			continue
		}

		if int64(len(d)) > limit {
			marker := []byte(fmt.Sprintf("\n[truncated at %d bytes]", limit))
			cut := limit - int64(len(marker))

			for cut > 0 && !utf8.RuneStart(d[cut]) {
				cut--
			}
			d = append(d[:cut], marker...)
		}
		totalRead += int64(len(d))
		files = append(files, archiveFile{name: f.Name, data: d})
	}
	return files, nil
}

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

const maxTarDecompressSize = 200 * 1024 * 1024

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func listTarGz(data []byte) ([]archiveFile, int, bool, bool, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, 0, false, false, fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()
	cr := &countingReader{r: io.LimitReader(gz, maxTarDecompressSize)}
	return listTarFromReader(cr, cr)
}

func listTarBz2(data []byte) ([]archiveFile, int, bool, bool, error) {
	br := bzip2.NewReader(bytes.NewReader(data))
	cr := &countingReader{r: io.LimitReader(br, maxTarDecompressSize)}
	return listTarFromReader(cr, cr)
}

func listTarXz(data []byte) ([]archiveFile, int, bool, bool, error) {

	return nil, 0, false, false, fmt.Errorf("tar.xz support requires xz decompression (not yet available)")
}

func listTar(data []byte) ([]archiveFile, int, bool, bool, error) {
	return listTarFromReader(bytes.NewReader(data), nil)
}

func listTarFromReader(r io.Reader, limited *countingReader) ([]archiveFile, int, bool, bool, error) {
	limitHit := func() bool {
		return limited != nil && limited.n >= maxTarDecompressSize
	}
	tr := tar.NewReader(r)
	var files []archiveFile
	total := 0
	truncated := false
	corrupt := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {

			if limitHit() {
				truncated = true
			}
			break
		}
		if err != nil {

			if limitHit() {
				truncated = true
			} else {
				corrupt = true
				truncated = false
			}
			break
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != 0 {
			continue
		}
		total++
		if len(files) >= maxArchiveEntries {

			for {
				h, err := tr.Next()
				if err != nil {
					if err != io.EOF && !limitHit() {
						corrupt = true
					}
					break
				}
				if h.Typeflag == tar.TypeReg || h.Typeflag == 0 {
					total++
				}
			}
			if total > len(files) {
				truncated = true
			}
			break
		}
		d, err := io.ReadAll(io.LimitReader(tr, maxArchiveEntrySize+1))
		if err != nil {

			files = append(files, archiveFile{name: hdr.Name, data: []byte(fmt.Sprintf("[unreadable: %v]", err))})
			continue
		}
		files = append(files, archiveFile{name: hdr.Name, data: d})
	}
	return files, total, truncated, corrupt, nil
}

func extractArchiveContentDepth(data []byte, ext string, depth int) string {
	if depth >= maxArchiveDepth {
		return ""
	}
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

	case ".tar.xz", ".rar", ".7z":
		return fmt.Sprintf("[Unsupported nested archive format: %s]", ext)
	default:
		return ""
	}
	e := &archiveExtractor{subFormat: subFmt, depth: depth}
	result, err := e.Extract(data)
	if err != nil {

		return fmt.Sprintf("[Extraction failed: %v]", err)
	}
	return result.Text
}

func isArchiveExt(ext string) bool {
	switch ext {
	case ".zip", ".tar", ".tar.gz", ".tgz", ".tar.bz2", ".tar.xz", ".rar", ".7z":
		return true
	}
	return false
}

func isImageExt(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".ico", ".svg",
		".tiff", ".tif", ".heic", ".heif":
		return true
	}
	return false
}

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
