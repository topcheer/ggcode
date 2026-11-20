package swarm

import (
	"os"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// Live-store diagnostic (run with -tags probeLive): points the manager at
// the REAL global store and runs AutoRestoreSessionTeams so the SUMMARY
// log line reveals the actual boardRoot, scan count and skip reasons.
// go test -tags probeLive ./internal/swarm/ -run LiveStoreProbe -v -count=1

func TestLiveStoreProbe(t *testing.T) {
	if os.Getenv("PROBE_LIVE_STORE") == "" {
		t.Skip("set PROBE_LIVE_STORE=1 to probe the real global store")
	}
	m := NewManager(config.SwarmConfig{}, nil, nil, nil)
	m.JournalsEnabled = true
	m.SetOwnerSession("20261030-184202-e9d0a9e5b668c9a0")
	t.Logf("boardRoot=%s", m.boardRoot())
	snaps := m.AutoRestoreSessionTeams()
	t.Logf("restored=%d", len(snaps))
	for _, s := range snaps {
		t.Logf("  team %s (%s): %d members", s.ID, s.Name, len(s.Teammates))
	}
}
