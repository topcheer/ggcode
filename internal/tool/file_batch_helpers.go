package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/extract"
	"github.com/topcheer/ggcode/internal/image"
)

const (
	maxMultiFileReadFiles         = 20
	defaultMultiFileReadLimit     = 200
	maxExplicitMultiFileReadLimit = 500
	maxMultiFileReadTotalLines    = 4000
	maxMultiFileReadTotalBytes    = 300 * 1024
	maxMultiFileEditFiles         = 10
	maxMultiFileEditEditsPerFile  = 20
	maxMultiFileEditTotalEdits    = 200
	maxMultiFileEditPayloadBytes  = 200 * 1024
)

type textEdit struct {
	OldText string
	NewText string
}

type PlannedFileEdit struct {
	Path             string
	OldContent       string
	NewContent       string
	AppliedEditCount int
}

type MultiFileEditFileResult struct {
	Path             string `json:"path"`
	Status           string `json:"status"`
	AppliedEditCount int    `json:"applied_edit_count,omitempty"`
	Error            string `json:"error,omitempty"`
}

type MultiFileEditContent struct {
	Summary      string                    `json:"summary"`
	Mode         string                    `json:"mode"`
	Applied      bool                      `json:"applied"`
	PlannedFiles int                       `json:"planned_files"`
	WrittenFiles int                       `json:"written_files"`
	FailedFiles  int                       `json:"failed_files"`
	SkippedFiles int                       `json:"skipped_files"`
	WrittenPaths []string                  `json:"written_paths"`
	FailedPaths  []string                  `json:"failed_paths"`
	SkippedPaths []string                  `json:"skipped_paths"`
	Results      []MultiFileEditFileResult `json:"results"`
}

func cleanAbsolutePath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path must not be empty")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be absolute: %s", path)
	}
	return filepath.Clean(path), nil
}

func readTextContentFromBytes(path string, data []byte, offset, limit int, opts readFileRangeOptions) (string, error) {
	if image.IsImageFile(path) {
		return "", fmt.Errorf("multi_file_read only supports text files and extracted document text")
	}
	if extract.IsDocumentFile(path) {
		result, err := extract.Extract(path, data)
		if err != nil {
			return "", fmt.Errorf("error extracting document text: %v", err)
		}
		var header string
		if result.Pages > 0 {
			header = fmt.Sprintf("[Extracted from %s, %d pages]\n", result.Format, result.Pages)
		} else {
			header = fmt.Sprintf("[Extracted from %s]\n", result.Format)
		}
		return header + readFileRangeWithOptions(result.Text, offset, limit, opts), nil
	}

	content := string(data)
	text := readFileRangeWithOptions(content, offset, limit, opts)
	var meta string
	if indentHint := detectIndentStyle(content); indentHint != "" {
		meta = indentHint
	}
	if encHint := detectEncoding(data); encHint != "" {
		if meta != "" {
			meta += ", "
		}
		meta += encHint
	}
	if meta != "" {
		text = "[" + meta + "]\n" + text
	}
	return text, nil
}

func readTextContentAtPath(path string, offset, limit int, opts readFileRangeOptions) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("error accessing file: %v", err)
	}
	if info.Size() > maxFileSize {
		if offset > 0 || limit > 0 {
			return readFileRangeStreaming(path, offset, limit, opts)
		}
		return "", fmt.Errorf("file too large (%d MB). Use read_file with offset/limit for range reading.", info.Size()/(1024*1024))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("error reading file: %v", err)
	}
	return readTextContentFromBytes(path, data, offset, limit, opts)
}

func planTextEdits(content string, edits []textEdit) (string, int, string) {
	type editPos struct {
		start int
		end   int
		idx   int
		old   string
		new_  string
	}
	var positions []editPos
	for i, edit := range edits {
		if edit.OldText == "" {
			return "", 0, fmt.Sprintf("edits[%d]: old_text must not be empty", i)
		}
		mr := resolveOldText(content, edit.OldText)
		if mr.canonical == "" {
			hint := diagnoseMatchFailure(content, edit.OldText)
			msg := fmt.Sprintf("edits[%d]: old_text not found in file", i)
			if hint != "" {
				msg += ". " + hint
			}
			return "", 0, msg
		}
		oldText := mr.canonical
		count := strings.Count(content, oldText)
		if count > 1 && !mr.anchored {
			lines := findMatchLineNumbers(content, oldText)
			msg := fmt.Sprintf(
				"edits[%d]: old_text found %d times — must be unique. Add 1-3 lines of surrounding context to disambiguate, or copy the exact numbered lines from read_file so this edit is line-number anchored.",
				i, count,
			)
			if len(lines) > 0 {
				more := ""
				if count > len(lines) {
					more = fmt.Sprintf(" (showing first %d)", len(lines))
				}
				msg += fmt.Sprintf(" Matches start at line(s): %s%s.", formatMatchLines(lines), more)
			}
			return "", 0, msg
		}
		idx := mr.start
		if !mr.anchored {
			idx = strings.Index(content, oldText)
		}
		newText := edit.NewText
		if mr.transform != "" {
			newText = adjustNewText(content, edit.NewText, mr)
		}
		positions = append(positions, editPos{
			start: idx,
			end:   idx + len(oldText),
			idx:   i,
			old:   oldText,
			new_:  newText,
		})
	}

	sort.Slice(positions, func(i, j int) bool { return positions[i].start < positions[j].start })
	for i := 1; i < len(positions); i++ {
		if positions[i].start < positions[i-1].end {
			return "", 0, fmt.Sprintf(
				"edits[%d] and edits[%d]: overlapping matches — each old_text must not overlap",
				positions[i-1].idx, positions[i].idx,
			)
		}
	}

	out := content
	for i := len(positions) - 1; i >= 0; i-- {
		p := positions[i]
		out = out[:p.start] + p.new_ + out[p.end:]
	}
	return out, len(edits), ""
}
