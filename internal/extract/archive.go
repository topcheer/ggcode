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

	"github.com/topcheer/ggcode/internal/util"
)

// archiveExtractor extracts text from archive files.
type archiveExtractor struct {
	subFormat string // "zip", "tar", "tar.gz", "tar.bz2", "tar.xz"
	depth     int    // nesting depth for recursive extraction
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

	// #682: tar-family listings could be silently truncated (200MB decompress
	// limit, >500-entry stop) with no marker, unlike the zip path. Surface the
	// truncation so consumers know the inventory is partial.
	if truncated && total > len(files) {
		fmt.Fprintf(&buf, "[Showing first %d of %d files]\n\n", len(files), total)
	} else if truncated {
		buf.WriteString("[Truncated: archive exceeds extraction limits]\n\n")
	}
	// #687: a mid-stream tar error can mean CORRUPTION, not a size limit —
	// mislabeling it "exceeds extraction limits" sends the agent down the
	// wrong attribution path. Distinguish the two explicitly.
	if corrupt {
		fmt.Fprintf(&buf, "[Corrupt archive: stream error after %d files; listing is partial]\n\n", len(files))
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

		// Nested archive — depth tracked via maxArchiveDepth constant.
		// extractArchiveContent itself checks nesting level.
		if isArchiveExt(ext) {
			nested := extractArchiveContentDepth(f.data, ext, e.depth+1)
			if nested != "" {
				// Indent nested content
				for _, line := range strings.Split(nested, "\n") {
					buf.WriteString("  ")
					buf.WriteString(line)
					buf.WriteByte('\n')
				}
			} else {
				// #1205: defensive — if nested extraction returned empty (e.g.,
				// unsupported format, depth exceeded), emit a visible marker rather
				// than a bare empty section. The header "--- name (size) ---" was
				// already written, so without this the entry looks like an empty
				// archive, which is indistinguishable from a true empty archive.
				fmt.Fprintf(&buf, "[Nested archive content not available]\n")
			}
			continue
		}

		// Known document format
		if ext != "" && defaultRegistry.Get(ext) != nil && !isArchiveExt(ext) {
			result, err := Extract(name, f.data)
			// #686: an extraction error used to drop the entry silently — the
			// archive inventory lost track of it entirely. Mark it visibly so
			// the listing stays honest (partial content from e.g. a
			// decode-broken SVG now arrives flagged via its own text, but
			// hard errors still surface here).
			if err != nil {
				fmt.Fprintf(&buf, "[Extraction failed: %v]\n\n", err)
			} else if result.Text != "" {
				text := result.Text
				if len(text) > maxArchiveEntrySize {
					// Snap to a rune boundary so we never slice a multi-byte
					// CJK/emoji rune in half (#547, same fix as #301).
					cut := util.SnapToRuneStart(text, maxArchiveEntrySize)
					text = text[:cut] + "\n... (truncated)"
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

// #566(E): zip entry read budgets. Previously every entry was buffered in
// full (io.ReadAll up to 1MB+1 × 500 entries ≈ 500MB peak; a 390KB crafted
// zip measured 120MB resident) even though plain-text entries only need a
// small prefix for the preview. Two tiers:
//   - structured entries (nested archives, registered document formats)
//     still read up to maxArchiveEntrySize — they cannot be parsed truncated;
//   - everything else reads only maxZipEntryRead bytes (preview budget).
//
// A cumulative cap bounds the worst case of many max-size structured entries.
const (
	maxZipEntryRead = 64 * 1024        // preview budget per plain entry
	maxZipTotalRead = 32 * 1024 * 1024 // cumulative budget across all entries
)

// zipNeedsFullData reports whether an entry must be read whole (capped at
// maxArchiveEntrySize) because it is a nested archive or a structured
// document format that cannot be parsed from a truncated prefix.
func zipNeedsFullData(name string) bool {
	ext := extOf(name)
	// #1205: .tar.xz, .rar, .7z are treated as archive extensions by
	// isArchiveExt, but they have no extraction implementation for nested
	// archives. extractArchiveContentDepth returns a marker based on the
	// extension alone, so we don't need to buffer their full content.
	// Demote them to preview budget to avoid wasting 1MB per entry.
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
			// Cumulative budget exhausted: keep listing the name so the
			// file inventory stays complete, but do not buffer more data.
			files = append(files, archiveFile{name: f.Name})
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		// Stream via LimitReader (limit+1 to detect over-limit) instead of
		// buffering each entry in full.
		d, err := io.ReadAll(io.LimitReader(rc, limit+1))
		rc.Close()
		if err != nil {
			continue
		}
		totalRead += int64(len(d))
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

// maxTarDecompressSize limits total decompressed tar data to prevent
// decompression bombs (small compressed file → huge uncompressed data).
const maxTarDecompressSize = 200 * 1024 * 1024 // 200MB

// countingReader counts bytes read through it so a mid-stream tar error can
// be attributed correctly (#692): reaching maxTarDecompressSize is a size
// limit on a healthy archive, not corruption.
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
	// xz requires external dependency; try decompressing manually
	// For now, return a helpful error
	return nil, 0, false, false, fmt.Errorf("tar.xz support requires xz decompression (not yet available)")
}

func listTar(data []byte) ([]archiveFile, int, bool, bool, error) {
	return listTarFromReader(bytes.NewReader(data), nil)
}

// listTarFromReader returns the buffered files (≤ maxArchiveEntries), the
// total regular-file count when knowable, whether the listing was truncated
// (entry cap or decompress-size cap reached), and whether the stream errored
// mid-way (genuine corruption). #682: previously truncation was silent and the
// drain loop's file count was discarded. #687: corruption and limit
// truncation are reported separately so the marker does not misattribute a
// corrupt stream to a size limit. #692: the 200MB decompression cap was
// bucketed as corruption — a healthy-but-large archive must surface the
// truncation marker instead. `limited` is the countingReader wrapping the
// size-capped stream (nil for plain tar, which has no decompress cap); when
// the byte count reached the cap, any stream error — including a clean
// io.EOF when the cut lands exactly on a header boundary — is a size-limit
// hit, not corruption.
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
			// #692: a clean EOF with the byte budget exhausted means the
			// LimitReader cut the stream exactly on a header boundary —
			// silent truncation otherwise.
			if limitHit() {
				truncated = true
			}
			break
		}
		if err != nil {
			// Unexpected error: either a genuinely corrupt stream or the
			// 200MB decompression limit surfacing as a read error (#692:
			// these are distinct failure modes — check the byte count
			// before blaming corruption). The listing so far is partial.
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
			// Drain remaining headers to count total files (#682: the count
			// was previously computed and discarded — now it drives the
			// "[Showing first %d of %d files]" marker, matching the zip path).
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
			continue
		}
		files = append(files, archiveFile{name: hdr.Name, data: d})
	}
	return files, total, truncated, corrupt, nil
}

// extractArchiveContentDepth recursively extracts text from a nested archive.
// Depth is tracked to prevent stack overflow from malicious archives.
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
	// #1205: unsupported nested formats (.tar.xz, .rar, .7z) must produce
	// a visible marker instead of returning empty (which would leave a bare
	// header with no content, indistinguishable from an empty archive).
	case ".tar.xz", ".rar", ".7z":
		return fmt.Sprintf("[Unsupported nested archive format: %s]", ext)
	default:
		return ""
	}
	e := &archiveExtractor{subFormat: subFmt, depth: depth}
	result, err := e.Extract(data)
	if err != nil {
		// #1205: extraction errors on supported nested formats must surface
		// a visible marker, not return empty. Without this, corrupt nested
		// ZIPs silently vanish from the archive inventory.
		return fmt.Sprintf("[Extraction failed: %v]", err)
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
