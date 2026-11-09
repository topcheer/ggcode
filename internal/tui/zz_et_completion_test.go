package tui

import (
	"strings"
	"testing"
)

// /et 顶级命令族 completion 面钉测:命令表/描述/占位符(subcommand 显示)必须齐
func TestEtCompletionSurface(t *testing.T) {
	found := false
	for _, c := range SlashCommands {
		if c == "/et" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("/et missing from SlashCommands completion list")
	}
	if SlashCommandDescriptions["/et"] == "" {
		t.Fatal("/et missing from SlashCommandDescriptions")
	}
	ph, ok := SlashCommandPlaceholders["/et"]
	if !ok {
		t.Fatal("/et missing from SlashCommandPlaceholders")
	}
	for _, sub := range []string{"on", "off", "show", "setup"} {
		if !strings.Contains(ph, sub) {
			t.Errorf("/et placeholder %q must mention subcommand %q", ph, sub)
		}
	}
}
