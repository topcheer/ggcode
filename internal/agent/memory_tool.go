package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/tool"
)

// Anthropic Memory Tool (memory_20250818): provider-declared, client-executed
// long-term memory across the GA "memory tool" surface
// (docs.en/agents-and-tools/tool-use/memory-tool). The API only defines the
// contract: the model issues tool_use blocks named "memory" with one of six
// commands against a virtual /memories filesystem, and the CLIENT implements
// storage and returns the results (contrast with web_search/web_fetch, which
// run server-side). Declaration happens in the Anthropic provider
// (SetMemoryTool → {"type":"memory_20250818"}); this file is the client-side
// executor, following the agent-side meta-tool pattern of tool_search.go so
// the tool never appears in Registry.ToDefinitions.
//
// Storage layout: the virtual /memories prefix maps onto
// <workingDir>/.ggcode/memories, created on demand. Project-local means the
// store persists across sessions for the same project while different
// projects never share files.
//
// Path confinement is enforced on every call: paths must start with
// /memories, traversal ("..", percent-encoded variants) and symlink escapes
// are rejected, so the permission fast path (config_policy.go treats the
// memory tool like save_memory) can never touch files outside the store.

const (
	// memoryToolName is the tool name the model calls. Fixed by the API
	// contract (MemoryTool20250818Param defaults its name to "memory").
	memoryToolName = "memory"

	// memoryVirtualPrefix is the only legal root of every memory path.
	memoryVirtualPrefix = "/memories"

	// memoryDirName is the on-disk store directory under the working dir.
	memoryDirName = ".ggcode/memories"

	// memoryMaxViewLines caps a full-file view; the API errors above this.
	memoryMaxViewLines = 999_999

	// memoryViewSnippetWindow bounds the snippet echoed after str_replace.
	memoryViewSnippetWindow = 40
)

// memoryToolState executes memory tool calls. Stateless apart from the lazy
// root binding: the working directory is read at call time so agents created
// before SetWorkingDir still resolve the correct store.
type memoryToolState struct{}

func newMemoryToolState() *memoryToolState {
	return &memoryToolState{}
}

// memoryRawArgs is the argument shape of every command. view_range arrives
// as a [start, end] integer tuple, so it is decoded as a slice.
type memoryRawArgs struct {
	Command    string  `json:"command"`
	Path       string  `json:"path"`
	OldPath    string  `json:"old_path"`
	NewPath    string  `json:"new_path"`
	FileText   string  `json:"file_text"`
	OldStr     *string `json:"old_str"`
	NewStr     *string `json:"new_str"`
	InsertLine *int    `json:"insert_line"`
	InsertText string  `json:"insert_text"`
	ViewRange  []int64 `json:"view_range"`
}

// executeResult dispatches a memory tool call. workingDir is the agent's
// current project directory; empty falls back to the process cwd.
func (s *memoryToolState) executeResult(args json.RawMessage, workingDir string) tool.Result {
	var raw memoryRawArgs
	if err := json.Unmarshal(args, &raw); err != nil {
		return memoryErr(fmt.Sprintf("Error: Invalid arguments for the memory tool: %v", err))
	}
	if strings.TrimSpace(raw.Command) == "" {
		return memoryErr(`Error: Missing required "command" argument. Supported commands: view, create, str_replace, insert, delete, rename.`)
	}

	root, rerr := memoryRootDir(workingDir)
	if rerr != nil {
		return memoryErr(fmt.Sprintf("Error: Memory storage unavailable: %v", rerr))
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return memoryErr(fmt.Sprintf("Error: Could not create memory directory %s: %v", root, err))
	}

	switch raw.Command {
	case "view":
		return s.view(root, raw)
	case "create":
		return s.create(root, raw)
	case "str_replace":
		return s.strReplace(root, raw)
	case "insert":
		return s.insert(root, raw)
	case "delete":
		return s.delete(root, raw)
	case "rename":
		return s.rename(root, raw)
	default:
		return memoryErr(fmt.Sprintf("Error: Unknown command %q. Supported commands: view, create, str_replace, insert, delete, rename.", raw.Command))
	}
}

// memoryErr wraps an error string as a tool result flagged with IsError, the
// same convention the docs use for the reference implementations: errors are
// ordinary string results the model can read and react to.
func memoryErr(msg string) tool.Result {
	return tool.Result{Content: msg, IsError: true}
}

// memoryRootDir resolves the on-disk directory backing /memories.
func memoryRootDir(workingDir string) (string, error) {
	base := strings.TrimSpace(workingDir)
	if base == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		base = cwd
	}
	abs, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	return filepath.Join(abs, memoryDirName), nil
}

// resolveMemoryPath validates a virtual path and maps it onto the real store.
// Returns the canonical virtual path and the real filesystem path.
func resolveMemoryPath(root, virtual string) (string, string, error) {
	p := strings.TrimSpace(virtual)
	if p == "" {
		return "", "", fmt.Errorf("Error: Missing required \"path\" argument.")
	}
	if !strings.HasPrefix(p, memoryVirtualPrefix) {
		return "", "", fmt.Errorf("Error: Path must be an absolute path starting with %s, got: %s", memoryVirtualPrefix, p)
	}
	lower := strings.ToLower(p)
	if strings.Contains(lower, "%2e") || strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") {
		return "", "", fmt.Errorf("Error: Path contains percent-encoded segments, which is not allowed: %s", p)
	}
	clean := filepath.ToSlash(filepath.Clean(p))
	if clean != memoryVirtualPrefix && !strings.HasPrefix(clean, memoryVirtualPrefix+"/") {
		return "", "", fmt.Errorf("Error: Path must be an absolute path starting with %s, got: %s", memoryVirtualPrefix, p)
	}
	if strings.Contains(clean, "..") {
		return "", "", fmt.Errorf("Error: Path traversal is not allowed: %s", p)
	}

	realRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	// Resolve the store root through any symlinks (e.g. /tmp -> /private/tmp
	// on macOS, symlinked project directories) so the containment check
	// below compares like-for-like resolved paths. The root always exists by
	// the time this runs (executeResult creates it up front).
	if resolvedRoot, rerr := filepath.EvalSymlinks(realRoot); rerr == nil {
		realRoot = resolvedRoot
	}
	rel := strings.TrimPrefix(clean, memoryVirtualPrefix)
	real := filepath.Join(realRoot, rel)

	// Symlink hardening: resolve what already exists (the leaf may be new,
	// so fall back to its parent) and re-verify containment inside the store.
	probe := real
	if _, err := os.Lstat(real); err != nil {
		probe = filepath.Dir(real)
	}
	if resolved, err := filepath.EvalSymlinks(probe); err == nil {
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			return "", "", err
		}
		if resolved != realRoot && !strings.HasPrefix(resolved, realRoot+string(os.PathSeparator)) {
			return "", "", fmt.Errorf("Error: Path resolves outside the memory store: %s", p)
		}
	}
	return clean, real, nil
}

// memoryErrf returns a memory path error for a failed resolution.
func memoryErrf(virtual string, err error) tool.Result {
	if err != nil {
		return memoryErr(err.Error())
	}
	return memoryErr(fmt.Sprintf("Error: Invalid path %q.", virtual))
}

// humanizeSize renders a byte count for directory listings ("4.0K" style).
func humanizeSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	value := float64(n)
	units := []string{"K", "M", "G", "T"}
	for _, u := range units {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f%s", value, u)
		}
	}
	return fmt.Sprintf("%.1fP", value/unit)
}

// memoryPathError renders the canonical missing-path error string.
func memoryPathError(virtual string) tool.Result {
	return memoryErr(fmt.Sprintf("Error: The path %s does not exist.", virtual))
}

// view implements view: directory listing (2 levels, hidden entries and
// node_modules excluded) or numbered file content with an optional
// view_range [start, end] (inclusive; negative end = through EOF).
func (s *memoryToolState) view(root string, raw memoryRawArgs) tool.Result {
	clean, real, err := resolveMemoryPath(root, raw.Path)
	if err != nil {
		return memoryErrf(raw.Path, err)
	}
	info, err := os.Lstat(real)
	if err != nil {
		return memoryPathError(clean)
	}

	if info.IsDir() {
		return s.viewDir(clean, real)
	}
	if isImagePath(clean) {
		return tool.Result{
			Content: fmt.Sprintf("The path %s is an image file (%s). Image rendering is not supported by this memory store; store and inspect text files instead.", clean, humanizeSize(info.Size())),
		}
	}

	content, err := os.ReadFile(real)
	if err != nil {
		return memoryErr(fmt.Sprintf("Error: Could not read %s: %v", clean, err))
	}
	trimmed := strings.TrimRight(string(content), "\n")
	var lines []string
	if trimmed == "" {
		lines = []string{}
	} else {
		lines = strings.Split(trimmed, "\n")
	}
	if len(lines) > memoryMaxViewLines {
		return memoryErr(fmt.Sprintf("Error: Cannot view %s: it has more than %d lines. Use view_range to view specific portions of the file.", clean, memoryMaxViewLines))
	}
	// Optional view_range [start, end], 1-based inclusive; a negative or
	// omitted end means "through EOF" (the API convention). Invalid ranges
	// error out so the model can correct itself instead of reading the
	// wrong slice silently. numberBase keeps line numbering absolute when a
	// slice is shown, matching what the model wrote to the file.
	numberBase := 1
	if len(raw.ViewRange) > 0 {
		start := raw.ViewRange[0]
		askedEnd := int64(-1)
		if len(raw.ViewRange) > 1 {
			askedEnd = raw.ViewRange[1]
		}
		end := askedEnd
		if end < 0 {
			end = int64(len(lines))
		}
		if start < 1 || start > int64(len(lines)) || end < start || end > int64(len(lines)) {
			return memoryErr(fmt.Sprintf("Error: Invalid view_range [%d, %d] for %s: the file has %d lines.", start, askedEnd, clean, len(lines)))
		}
		lines = lines[start-1 : end]
		numberBase = int(start)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Here's the content of %s with line numbers:\n", clean)
	writeNumberedLines(&b, lines, numberBase)
	return tool.Result{Content: strings.TrimRight(b.String(), "\n")}
}

// viewDir renders the two-level listing format from the reference clients.
func (s *memoryToolState) viewDir(clean, real string) tool.Result {
	entries := listDirEntries(real, clean)
	var b strings.Builder
	fmt.Fprintf(&b, "Here're the files and directories up to 2 levels deep in %s, excluding hidden items and node_modules:\n", clean)
	if len(entries) == 0 {
		fmt.Fprintf(&b, "0B\t%s\n", clean)
		return tool.Result{Content: strings.TrimRight(b.String(), "\n")}
	}
	for _, e := range entries {
		fmt.Fprintf(&b, "%s\t%s\n", humanizeSize(e.size), e.vpath)
		if e.isDir {
			for _, sub := range listDirEntries(e.realpath, e.vpath) {
				fmt.Fprintf(&b, "%s\t%s\n", humanizeSize(sub.size), sub.vpath)
			}
		}
	}
	return tool.Result{Content: strings.TrimRight(b.String(), "\n")}
}

type memoryDirEntry struct {
	vpath    string
	realpath string
	size     int64
	isDir    bool
}

// listDirEntries returns visible, sorted entries of dir. vdir is the virtual
// path of the directory being listed (e.g. "/memories"), used to build each
// entry's virtual path directly.
func listDirEntries(dir, vdir string) []memoryDirEntry {
	raw, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	entries := make([]memoryDirEntry, 0, len(raw))
	for _, e := range raw {
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" {
			continue
		}
		real := filepath.Join(dir, name)
		var size int64
		if info, err := e.Info(); err == nil && !e.IsDir() {
			size = info.Size()
		}
		entries = append(entries, memoryDirEntry{
			vpath:    strings.TrimSuffix(vdir, "/") + "/" + name,
			realpath: real,
			size:     size,
			isDir:    e.IsDir(),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].isDir != entries[j].isDir {
			return entries[i].isDir
		}
		return entries[i].vpath < entries[j].vpath
	})
	return entries
}

// writeNumberedLines renders lines with absolute line numbers: the first
// entry gets number base (full files pass 1; view_range slices pass the
// slice's start so the model sees original positions).
func writeNumberedLines(b *strings.Builder, lines []string, base int) {
	for i, line := range lines {
		fmt.Fprintf(b, "%6d\t%s\n", base+i, line)
	}
}

func isImagePath(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".jpg", ".jpeg", ".png":
		return true
	}
	return false
}

// create implements create: write file_text to a NEW file.
func (s *memoryToolState) create(root string, raw memoryRawArgs) tool.Result {
	clean, real, err := resolveMemoryPath(root, raw.Path)
	if err != nil {
		return memoryErrf(raw.Path, err)
	}
	if clean == memoryVirtualPrefix {
		return memoryErr(fmt.Sprintf("Error: %s is the memory root directory and cannot be created as a file.", clean))
	}
	if _, err := os.Lstat(real); err == nil {
		return memoryErr(fmt.Sprintf("Error: File %s already exists. Use str_replace to modify it, or delete it first to recreate it.", clean))
	}
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		return memoryErr(fmt.Sprintf("Error: Could not create parent directory for %s: %v", clean, err))
	}
	if err := os.WriteFile(real, []byte(raw.FileText), 0o644); err != nil {
		return memoryErr(fmt.Sprintf("Error: Could not create %s: %v", clean, err))
	}
	return tool.Result{Content: fmt.Sprintf("File created successfully at: %s", clean)}
}

// loadFileLines reads a file as lines for the editing commands.
func loadFileLines(clean, real string) ([]string, error) {
	content, err := os.ReadFile(real)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimRight(string(content), "\n")
	if trimmed == "" {
		return []string{}, nil
	}
	return strings.Split(trimmed, "\n"), nil
}

// strReplace implements str_replace: replace the unique occurrence of
// old_str with new_str (omitted new_str = deletion).
func (s *memoryToolState) strReplace(root string, raw memoryRawArgs) tool.Result {
	clean, real, err := resolveMemoryPath(root, raw.Path)
	if err != nil {
		return memoryErrf(raw.Path, err)
	}
	if raw.OldStr == nil {
		return memoryErr(fmt.Sprintf("Error: Missing required \"old_str\" argument for str_replace on %s.", clean))
	}
	if *raw.OldStr == "" {
		return memoryErr(fmt.Sprintf("Error: old_str cannot be empty for str_replace on %s.", clean))
	}
	lines, err := loadFileLines(clean, real)
	if err != nil {
		return memoryPathError(clean)
	}
	joined := strings.Join(lines, "\n")
	count := strings.Count(joined, *raw.OldStr)
	if count == 0 {
		return memoryErr(fmt.Sprintf("Error: No match found for replacement in %s. Perhaps use view to inspect the file, or check that old_str is an exact match (not fuzzy).", clean))
	}
	if count > 1 {
		return memoryErr(fmt.Sprintf("Error: Found %d matches of old_str in %s. The old_str must be unique so the edit is unambiguous. Use view to check the file and retry with more surrounding context.", count, clean))
	}
	newStr := ""
	if raw.NewStr != nil {
		newStr = *raw.NewStr
	}
	updated := strings.Replace(joined, *raw.OldStr, newStr, 1)
	if err := os.WriteFile(real, []byte(updated), 0o644); err != nil {
		return memoryErr(fmt.Sprintf("Error: Could not write %s: %v", clean, err))
	}
	outLines := strings.Split(updated, "\n")
	if len(outLines) > memoryViewSnippetWindow {
		start := strings.Index(updated, newStr)
		if start < 0 {
			start = 0
		}
		anchorLine := strings.Count(updated[:start], "\n")
		lo := anchorLine - memoryViewSnippetWindow/2
		if lo < 0 {
			lo = 0
		}
		hi := lo + memoryViewSnippetWindow
		if hi > len(outLines) {
			hi = len(outLines)
		}
		outLines = outLines[lo:hi]
	}
	var b strings.Builder
	b.WriteString("The memory file has been edited.\n")
	writeNumberedLines(&b, outLines, 1)
	return tool.Result{Content: strings.TrimRight(b.String(), "\n")}
}

// insert implements insert: insert_text goes AFTER line insert_line
// (insert_line 0 = very beginning of the file).
func (s *memoryToolState) insert(root string, raw memoryRawArgs) tool.Result {
	clean, real, err := resolveMemoryPath(root, raw.Path)
	if err != nil {
		return memoryErrf(raw.Path, err)
	}
	if raw.InsertLine == nil {
		return memoryErr(fmt.Sprintf("Error: Missing required \"insert_line\" argument for insert on %s.", clean))
	}
	if raw.InsertText == "" {
		return memoryErr(fmt.Sprintf("Error: insert_text cannot be empty for insert on %s.", clean))
	}
	lines, err := loadFileLines(clean, real)
	if err != nil {
		return memoryPathError(clean)
	}
	at := *raw.InsertLine
	if at < 0 || at > len(lines) {
		return memoryErr(fmt.Sprintf("Error: The insert_line %d is out of bounds. The file %s has %d lines. Use view to check the file's length, or use insert_line=0 to insert at the beginning.", at, clean, len(lines)))
	}
	ins := strings.TrimSuffix(raw.InsertText, "\n")
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:at]...)
	out = append(out, strings.Split(ins, "\n")...)
	out = append(out, lines[at:]...)
	if err := os.WriteFile(real, []byte(strings.Join(out, "\n")), 0o644); err != nil {
		return memoryErr(fmt.Sprintf("Error: Could not write %s: %v", clean, err))
	}
	return tool.Result{Content: fmt.Sprintf("The file %s has been edited.", clean)}
}

// delete implements delete: removes files or directories (recursively).
// Deleting the memory root itself is rejected.
func (s *memoryToolState) delete(root string, raw memoryRawArgs) tool.Result {
	clean, real, err := resolveMemoryPath(root, raw.Path)
	if err != nil {
		return memoryErrf(raw.Path, err)
	}
	if clean == memoryVirtualPrefix {
		return memoryErr("Error: Cannot delete the memory root directory. Delete its contents instead.")
	}
	if _, err := os.Lstat(real); err != nil {
		return memoryPathError(clean)
	}
	if err := os.RemoveAll(real); err != nil {
		return memoryErr(fmt.Sprintf("Error: Could not delete %s: %v", clean, err))
	}
	return tool.Result{Content: fmt.Sprintf("Successfully deleted %s", clean)}
}

// rename implements rename: move old_path to new_path (files or dirs).
func (s *memoryToolState) rename(root string, raw memoryRawArgs) tool.Result {
	cleanOld, realOld, err := resolveMemoryPath(root, raw.OldPath)
	if err != nil {
		return memoryErrf(raw.OldPath, err)
	}
	if cleanOld == memoryVirtualPrefix {
		return memoryErr("Error: Cannot rename the memory root directory. Rename its contents instead.")
	}
	cleanNew, realNew, err := resolveMemoryPath(root, raw.NewPath)
	if err != nil {
		return memoryErrf(raw.NewPath, err)
	}
	if _, err := os.Lstat(realOld); err != nil {
		return memoryErr(fmt.Sprintf("Error: The path %s does not exist.", cleanOld))
	}
	if _, err := os.Lstat(realNew); err == nil {
		return memoryErr(fmt.Sprintf("Error: The destination path %s already exists.", cleanNew))
	}
	if err := os.MkdirAll(filepath.Dir(realNew), 0o755); err != nil {
		return memoryErr(fmt.Sprintf("Error: Could not create parent directory for %s: %v", cleanNew, err))
	}
	if err := os.Rename(realOld, realNew); err != nil {
		return memoryErr(fmt.Sprintf("Error: Could not rename %s to %s: %v", cleanOld, cleanNew, err))
	}
	return tool.Result{Content: fmt.Sprintf("Successfully renamed %s to %s", cleanOld, cleanNew)}
}
