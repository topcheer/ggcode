package tool

// #3316 tool-layer probes: send_file dispatch to the FileSender capability
// and the legacy path-text fallback for unsupported adapters.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SendFileDirect default: capability absent (existing tests keep their
// legacy path-text expectations). The interface grew in #3316.
func (m *mockIMManager) SendFileDirect(ctx context.Context, adapter string, file IMOutboundFile, caption string) (bool, error) {
	return false, nil
}

// fileCapableManager embeds the existing mock and adds SendFileDirect
// capture (the interface grew in #3316; the base mock keeps its defaults).
type fileCapableManager struct {
	mockIMManager
	supported bool
	err       error
	gotPath   string
	gotMIME   string
	sendCalls int
}

func (m *fileCapableManager) SendFileDirect(ctx context.Context, adapter string, file IMOutboundFile, caption string) (bool, error) {
	m.sendCalls++
	m.gotPath = file.Path
	m.gotMIME = file.MIME
	if m.err != nil {
		return false, m.err
	}
	return m.supported, nil
}

func newFileToolManager(t *testing.T, supported bool) *fileCapableManager {
	t.Helper()
	dir := t.TempDir()
	f := filepath.Join(dir, "probe.bin")
	if err := os.WriteFile(f, []byte("BINX"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &fileCapableManager{supported: supported}
	m.snapshot = IMSnapshot{
		CurrentBindings: []IMChannelBinding{{Adapter: "probe", ChannelID: "c1"}},
		Adapters:        []IMAdapterState{{Name: "probe", Healthy: true}},
	}
	return m
}

func TestIssue3316_ToolSendFileUploadsWhenSupported(t *testing.T) {
	dir := t.TempDir()
	pdf := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(pdf, []byte("%PDF-1.4 fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newFileToolManager(t, true)
	m.mockIMManager.snapshot.Adapters = []IMAdapterState{{Name: "probe", Healthy: true}}
	tool := IMTool{Manager: m}

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"send_file","adapter":"probe","path":"`+pdf+`","caption":"see attached"}`))
	if err != nil || res.IsError {
		t.Fatalf("send_file failed: err=%v res=%+v", err, res)
	}
	if !strings.Contains(res.Content, "uploaded") || !strings.Contains(res.Content, "application/pdf") {
		t.Fatalf("supported adapter must report a real upload, got: %s", res.Content)
	}
	if m.gotMIME != "application/pdf" || m.gotPath != pdf {
		t.Fatalf("adapter must receive file bytes + mime, got mime=%q path=%q", m.gotMIME, m.gotPath)
	}
	if m.sendCalls != 1 {
		t.Fatalf("exactly one SendFileDirect call, got %d", m.sendCalls)
	}
}

func TestIssue3316_ToolSendFileFallsBackToPathText(t *testing.T) {
	dir := t.TempDir()
	logf := filepath.Join(dir, "app.log")
	if err := os.WriteFile(logf, []byte("line"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newFileToolManager(t, false) // capability absent
	tool := IMTool{Manager: m}

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"send_file","adapter":"probe","path":"`+logf+`"}`))
	if err != nil || res.IsError {
		t.Fatalf("send_file failed: err=%v res=%+v", err, res)
	}
	// Fallback: the message text carries the path (SendDirect was used).
	if m.lastSendAdapter != "probe" {
		t.Fatalf("unsupported adapter must fall back to SendDirect, last adapter=%q", m.lastSendAdapter)
	}
	body := m.lastSendEvent.Text
	if !strings.Contains(body, logf) {
		t.Fatalf("fallback text must contain the file path, got: %q", body)
	}
	if m.sendCalls != 1 {
		t.Fatalf("capability probe must have been attempted once, got %d", m.sendCalls)
	}
}
