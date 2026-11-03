//go:build goolm

package tui

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4483_DiscordEnableRoutesThroughUpdateLoop(t *testing.T) {
	src, err := os.ReadFile("discord_panel.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(src)

	if !strings.Contains(s, "errDiscordEnableNeeded") {
		t.Fatal("errDiscordEnableNeeded sentinel missing")
	}
	if !strings.Contains(s, "return errDiscordEnableNeeded") {
		t.Fatal("startDiscordAdapterIfNeeded must return the sentinel instead of writing config")
	}

	if !strings.Contains(s, "errors.Is(err, errDiscordEnableNeeded)") {
		t.Fatal("next() lacks the enable-needed retry branch")
	}
	if !strings.Contains(s, "func (m *Model) discordEnableMutation(name string") {
		t.Fatal("discordEnableMutation missing")
	}

	startIdx := strings.Index(s, "func (m *Model) startDiscordAdapterIfNeeded")
	endIdx := strings.Index(s[startIdx:], "func ")
	body := s[startIdx : startIdx+endIdx]
	if strings.Contains(body, "SetIMAdapterEnabled") {
		t.Fatal("startDiscordAdapterIfNeeded still writes config on the Cmd goroutine")
	}
}
