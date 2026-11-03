package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newSendFileMock() *mockIMManager {
	return &mockIMManager{
		snapshot: IMSnapshot{
			CurrentBindings: []IMChannelBinding{{Adapter: "qq", ChannelID: "C123", Platform: "qq"}},
			Adapters:        []IMAdapterState{{Name: "qq", Platform: "qq", Healthy: true}},
		},
	}
}

func TestIMToolSendFile(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(imgPath, []byte("fake-png"), 0644); err != nil {
		t.Fatal(err)
	}
	txtPath := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(txtPath, []byte("fake-pdf"), 0644); err != nil {
		t.Fatal(err)
	}

	mgr := newSendFileMock()
	tool := IMTool{Manager: mgr}

	run := func(args string) Result {
		t.Helper()
		res, err := tool.Execute(context.Background(), json.RawMessage(args))
		if err != nil {
			t.Fatalf("execute %s: %v", args, err)
		}
		return res
	}

	res := run(`{"action":"send_file","adapter":"qq","path":` + jsonString(imgPath) + `,"caption":"执行结果截图"}`)
	if res.IsError {
		t.Fatalf("image send_file failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "image media upload") {
		t.Fatalf("image delivery note missing: %s", res.Content)
	}
	if mgr.lastSendAdapter != "qq" {
		t.Fatalf("send went to %q, want qq", mgr.lastSendAdapter)
	}
	got := mgr.lastSendEvent.Text
	if !strings.Contains(got, "执行结果截图") || !strings.Contains(got, imgPath) {
		t.Fatalf("caption+path not delivered: %q", got)
	}
	if !strings.HasSuffix(strings.TrimSpace(got), ".png") {
		t.Fatalf("path must end the message for extraction: %q", got)
	}

	res = run(`{"action":"send_file","adapter":"qq","path":` + jsonString(txtPath) + `}`)
	if res.IsError {
		t.Fatalf("pdf send_file failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "file path") {
		t.Fatalf("pdf should report path-text delivery: %s", res.Content)
	}
	if !strings.HasSuffix(strings.TrimSpace(mgr.lastSendEvent.Text), ".pdf") {
		t.Fatalf("pdf path not delivered: %q", mgr.lastSendEvent.Text)
	}

	for _, tc := range []struct {
		name, args, wantErr string
	}{
		{"missing adapter", `{"action":"send_file","path":"/tmp/x.png"}`, "adapter name is required"},
		{"missing path", `{"action":"send_file","adapter":"qq"}`, "file path is required"},
		{"relative path", `{"action":"send_file","adapter":"qq","path":"rel/shot.png"}`, "must be absolute"},
		{"nonexistent", `{"action":"send_file","adapter":"qq","path":"/nonexistent/zz.png"}`, "not accessible"},
		{"directory", `{"action":"send_file","adapter":"qq","path":` + jsonString(dir) + `}`, "is a directory"},
	} {
		res := run(tc.args)
		if !res.IsError || !strings.Contains(res.Content, tc.wantErr) {
			t.Fatalf("%s: want error containing %q, got %+v", tc.name, tc.wantErr, res)
		}
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
