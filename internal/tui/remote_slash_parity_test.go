package tui

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/im"
)

// Parity: every one-shot command in the shared IM slash registry must be
// routable through the TUI-attached path (ExecuteRemoteSlashCommand). This is
// the path-B half of the registry contract; the path-A half lives in
// internal/im. If a new command lands in the registry but this path stops
// serving it, this test fails.
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
		// Handlers may legitimately return an error text on a bare test
		// model (no agent/session), but they must respond, not fall through.
		if resp == "" {
			t.Fatalf("/%s handled but empty response", c.Name)
		}
	}
}

// Interactive registry entries must produce the TUI-only hint on this path,
// not an unknown-command error.
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
