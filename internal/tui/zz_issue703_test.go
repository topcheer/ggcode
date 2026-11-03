package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/chat"
)

func write703CostFile(t *testing.T, home, name, json string) {
	t.Helper()
	dir := filepath.Join(home, ".ggcode", "cost")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(json), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func lastSystemText703(m *Model) string {
	n := m.chatList.Len()
	for i := n - 1; i >= 0; i-- {
		if s, ok := m.chatList.ItemAt(i).(*chat.SystemItem); ok {
			return s.Text()
		}
	}
	return ""
}

func TestIssue2312CostAllRetired(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := newTestModel()
	m.handleCostAllCommand()

	out := lastSystemText703(&m)
	if out == "" {
		t.Fatal("no system message produced by /cost all")
	}
	if !strings.Contains(out, "#2150") {
		t.Errorf("retired command must point at the replacement surface; output:\n%s", out)
	}
	if strings.Contains(out, "$") {
		t.Errorf("retired command must not render dollar figures from dead data; output:\n%s", out)
	}
}
