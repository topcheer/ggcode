package session

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// #662: #656's tolerant loadSession lumped io.EOF and real I/O errors
// together — an EIO/EDQUOT mid-file silently returned a TRUNCATED session as
// success, and the next Save O_APPENDed onto the truncated state, permanently
// losing the unread tail records. Real I/O errors must surface as errors;
// only io.EOF is a normal end-of-file.

// errReader yields a few good lines, then a real (non-EOF) I/O error.
type errReader struct {
	lines []string
	pos   int
	done  bool
	err   error
}

func (r *errReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.lines) {
		r.done = true
		return 0, r.err // NOT io.EOF — a real I/O failure mid-stream
	}
	line := r.lines[r.pos] + "\n"
	r.pos++
	n := copy(p, line)
	return n, nil
}

// TestIssue662_ReadLineLimitedCountedPropagatesRealIOError: the bounded
// reader primitive must hand real I/O errors back to the caller so the load
// loop can distinguish them from io.EOF (#662). A reader that errors with
// EIO after two good lines must surface syscall.EIO, not io.EOF.
func TestIssue662_ReadLineLimitedCountedPropagatesRealIOError(t *testing.T) {
	br := bufio.NewReader(&errReader{
		lines: []string{`{"type":"meta"}`, `{"type":"message"}`},
		err:   syscall.EIO,
	})
	l1, _, e1 := readLineLimitedCounted(br, 10*1024*1024)
	if e1 != nil || !strings.Contains(string(l1), "meta") {
		t.Fatalf("line1 must read cleanly, got %q err=%v", l1, e1)
	}
	l2, _, e2 := readLineLimitedCounted(br, 10*1024*1024)
	if e2 != nil || !strings.Contains(string(l2), "message") {
		t.Fatalf("line2 must read cleanly, got %q err=%v", l2, e2)
	}
	_, _, e3 := readLineLimitedCounted(br, 10*1024*1024)
	if e3 == nil {
		t.Fatal("a real I/O error (EIO) must be propagated, not swallowed (#662)")
	}
	if !errors.Is(e3, syscall.EIO) {
		t.Fatalf("expected the underlying EIO, got %v", e3)
	}
	if errors.Is(e3, io.EOF) {
		t.Fatal("EIO must never be classified as io.EOF (#662)")
	}
}

// TestIssue662_LoadSessionReturnsErrorOnRealIOError: loadSession must return
// an error when the session file becomes unreadable mid-stream. A directory
// opened for reading yields EISDIR on read(2) on both darwin and linux — a
// portable, faithful "real I/O error after open succeeded" stand-in. Before
// #662 the loop broke on the error and silently returned an empty/truncated
// session as success.
func TestIssue662_LoadSessionReturnsErrorOnRealIOError(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Title = "EIO Session"
	ses.Workspace = "/tmp/ws-662"
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}

	// Replace the session JSONL file with a directory: os.Open succeeds, the
	// first read returns EISDIR — a real I/O error, not EOF.
	path := filepath.Join(dir, ses.ID+".jsonl")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.loadSession(ses.ID)
	if err == nil {
		t.Fatalf("loadSession must return an error on a real I/O error instead of silently returning a truncated session (#662), got %v msgs", len(loaded.Messages))
	}
	if loaded != nil {
		t.Fatalf("no session may be returned alongside the I/O error (#662), got %d msgs", len(loaded.Messages))
	}
	if !strings.Contains(err.Error(), "I/O error") {
		t.Fatalf("the error must identify itself as an I/O read failure (#662), got: %v", err)
	}
}

// TestIssue662_LoadSessionCleanEOFStillSucceeds: regression guard — io.EOF
// (normal end-of-file, including a torn last line without a trailing newline)
// must keep loading successfully with everything read so far. #656 tolerance
// for over-long/torn lines is untouched by the #662 error propagation.
func TestIssue662_LoadSessionCleanEOFStillSucceeds(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := NewSession("zai", "cn-coding-openai", "glm-5-turbo")
	ses.Title = "Clean EOF"
	ses.Workspace = "/tmp/ws-662b"
	ses.Messages = []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "clean tail"}}},
	}
	if err := store.Save(ses); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMetaToDisk(ses); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessagesBatchToDisk(ses, ses.Messages); err != nil {
		t.Fatal(err)
	}

	// Torn final line: strip the trailing newline — the load must still
	// succeed via the io.EOF path (#656 behavior preserved).
	path := filepath.Join(dir, ses.ID+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytesTrimLastNewline(data), 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatalf("clean EOF (torn tail without newline) must still load successfully (#656 preserved by #662): %v", err)
	}
	if len(loaded.Messages) == 0 {
		t.Fatal("messages before the torn tail must still load (#656 preserved by #662)")
	}
}

func bytesTrimLastNewline(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] == '\n' {
		return b[:len(b)-1]
	}
	return b
}

// TestIssue662_WrappedEOFClassification: the classification used by the load
// loop (errors.Is against io.EOF) must treat wrapped EOF as EOF and any other
// error as real — this is the exact predicate guarding the silent-truncation
// path. Demonstrated via the same expressions the loop evaluates.
func TestIssue662_WrappedEOFClassification(t *testing.T) {
	wrappedEOF := fmt.Errorf("read: %w", io.EOF)
	if !errors.Is(wrappedEOF, io.EOF) {
		t.Fatal("wrapped io.EOF must classify as EOF")
	}
	wrappedEIO := fmt.Errorf("read: %w", syscall.EIO)
	if errors.Is(wrappedEIO, io.EOF) {
		t.Fatal("wrapped EIO must NOT classify as EOF (#662)")
	}
	if errors.Is(syscall.ENOSPC, io.EOF) || errors.Is(syscall.EDQUOT, io.EOF) {
		t.Fatal("storage I/O errors must never classify as EOF (#662)")
	}
}
