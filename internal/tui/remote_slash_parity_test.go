package tui

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/im"
)

func TestRemoteSlashRegistryParity(t *testing.T) {
	m := newTestModel()
	for _, c := range im.IMSlashRegistry() {
		if c.Interactive {
			continue
		}
		resp, handled := m.ExecuteRemoteSlashCommand("/" + c.Name)
		if !handled {
			t.Fatalf("/%s registered one-shot but NOT handled by the TUI remote path", c.Name)
		}

		if resp == "" {
			t.Fatalf("/%s handled but empty response", c.Name)
		}
	}
}

func TestRemoteSlashInteractiveHint(t *testing.T) {
	m := newTestModel()
	for _, c := range im.IMSlashRegistry() {
		if !c.Interactive {
			continue
		}
		resp, handled := m.ExecuteRemoteSlashCommand("/" + c.Name)
		if !handled || !strings.Contains(resp, "TUI") {
			t.Fatalf("/%s interactive: handled=%v resp=%q", c.Name, handled, resp)
		}
	}
}
