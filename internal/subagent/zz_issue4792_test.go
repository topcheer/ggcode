package subagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// #4792 (S-22): when a sub-agent result exceeds the inline cap, the FULL text
// must be persisted to a receipt file and the truncation marker must point at
// it - verbatim-echo flows (review/closure) then read the file instead of
// burning an extra subagent to re-harvest.

func TestWriteFullResultReceiptPersistsVerbatim(t *testing.T) {
	id := "zz-test-4792-receipt"
	full := strings.Repeat("x", 150*1024) // over the 100KB cap shape
	path := writeFullResultReceipt(id, full)
	if path == "" {
		t.Fatal("expected a receipt path, got empty (best-effort channel must succeed on a writable home)")
	}
	if filepath.Base(path) != id+".txt" {
		t.Fatalf("receipt file name: want %q got %q", id+".txt", filepath.Base(path))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read receipt: %v", err)
	}
	if len(data) != len(full) || string(data) != full {
		t.Fatalf("receipt must be verbatim: got %d bytes, want %d", len(data), len(full))
	}
	os.Remove(path)
}

func TestWriteFullResultReceiptEmptyInputs(t *testing.T) {
	if got := writeFullResultReceipt("", "x"); got != "" {
		t.Fatalf("empty id must return \"\", got %q", got)
	}
	if got := writeFullResultReceipt("id", ""); got != "" {
		t.Fatalf("empty content must return \"\", got %q", got)
	}
}

func TestReceiptsLiveUnderHomeDotGgcode(t *testing.T) {
	path := writeFullResultReceipt("zz-test-4792-layout", "payload")
	if path == "" {
		t.Fatal("expected receipt path")
	}
	wantPrefix := filepath.Join(config.HomeDir(), ".ggcode", "subagent-results")
	if !strings.HasPrefix(path, wantPrefix) {
		t.Fatalf("receipt must live under %s, got %s", wantPrefix, path)
	}
	os.Remove(path)
}
