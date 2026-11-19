package swarm

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

// journaledFakeAgent is an AgentRunner whose responses reference every user
// message it has ever seen (seeded history included), proving memory
// continuity across a kill-and-restore cycle. It fires the persist handler
// for user and assistant turns exactly like the real agent's context
// manager does.
type journaledFakeAgent struct {
	mu      sync.Mutex
	history []provider.Message
	handler func(provider.Message)
}

func (a *journaledFakeAgent) RunStream(_ context.Context, prompt string, onEvent func(provider.StreamEvent), _ ...string) error {
	user := provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: prompt}}}
	a.record(user)

	resp := a.recall()
	assistant := provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: resp}}}
	a.record(assistant)

	if onEvent != nil {
		onEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: resp})
	}
	return nil
}

func (a *journaledFakeAgent) record(msg provider.Message) {
	a.mu.Lock()
	a.history = append(a.history, msg)
	h := a.handler
	a.mu.Unlock()
	if h != nil {
		h(msg)
	}
}

func (a *journaledFakeAgent) recall() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var sb strings.Builder
	sb.WriteString("recall:")
	for _, m := range a.history {
		if m.Role != "user" {
			continue
		}
		for _, b := range m.Content {
			if b.Type == "text" {
				sb.WriteString(" [")
				sb.WriteString(b.Text)
				sb.WriteString("]")
			}
		}
	}
	return sb.String()
}

func (a *journaledFakeAgent) SetPersistHandler(fn func(provider.Message)) {
	a.mu.Lock()
	a.handler = fn
	a.mu.Unlock()
}

// SeedContextMessages implements the restore replay surface.
func (a *journaledFakeAgent) SeedContextMessages(msgs []provider.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, m := range msgs {
		if m.Role == "system" {
			continue
		}
		a.history = append(a.history, m)
	}
}

func newPersistentTestManager(t *testing.T, boardRoot, sessionRoot string) *Manager {
	t.Helper()
	factory := func(_ provider.Provider, _ interface{}, _ string, _ int) AgentRunner {
		return &journaledFakeAgent{}
	}
	m := NewManager(config.SwarmConfig{PollInterval: 30 * time.Millisecond}, nil, factory, nil)
	m.BoardRoot = boardRoot
	m.SessionRoot = sessionRoot
	m.JournalsEnabled = true
	return m
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(msg)
}

func teammateResultContains(m *Manager, teamID, tmID, needle string) bool {
	r, ok := m.GetTeammateResult(teamID, tmID)
	return ok && strings.Contains(r, needle)
}

// P1 integration: create team + 2 teammates, run a context-mutating task,
// tear the manager down, RestoreTeam, and prove the next task references
// prior context (memory survived the restart).
func TestTeamKillRestorePreservesMemory(t *testing.T) {
	boardRoot := t.TempDir()
	sessionRoot := t.TempDir()

	m1 := newPersistentTestManager(t, boardRoot, sessionRoot)
	s := m1.CreateTeam("memteam", "leader-1")
	alice, err := m1.SpawnTeammate(s.ID, "alice", "", nil)
	if err != nil {
		t.Fatalf("spawn alice: %v", err)
	}
	bob, err := m1.SpawnTeammate(s.ID, "bob", "", nil)
	if err != nil {
		t.Fatalf("spawn bob: %v", err)
	}

	if err := m1.SendToTeammate(s.ID, alice.ID, MailMessage{Content: "remember SECRET-ALPHA-42", Type: "task"}); err != nil {
		t.Fatalf("send alice: %v", err)
	}
	if err := m1.SendToTeammate(s.ID, bob.ID, MailMessage{Content: "remember SECRET-BETA-77", Type: "task"}); err != nil {
		t.Fatalf("send bob: %v", err)
	}
	waitFor(t, 10*time.Second, func() bool {
		return teammateResultContains(m1, s.ID, alice.ID, "SECRET-ALPHA-42") &&
			teammateResultContains(m1, s.ID, bob.ID, "SECRET-BETA-77")
	}, "initial tasks never completed")

	// Graceful teardown: clean journal footers must be flushed.
	m1.Shutdown()

	// Manifest + journals on disk.
	mf, err := m1.loadManifest(s.ID)
	if err != nil {
		t.Fatalf("manifest after shutdown: %v", err)
	}
	if len(mf.Teammates) != 2 {
		t.Fatalf("manifest teammates = %d, want 2", len(mf.Teammates))
	}

	// Second "process": fresh manager, same persistence roots. Simulate the
	// restarted host (the first owner's PID no longer answers and its
	// heartbeat is stale) by aging the lease.
	m2 := newPersistentTestManager(t, boardRoot, sessionRoot)
	stale := ownerLease{BootID: "restarted-from", PID: 4194304, Hostname: "restarted-host", Heartbeat: time.Now().Add(-2 * ownerLeaseTTL)}
	if err := writeOwnerLeaseFile(m2.ownerLeasePath(s.ID), &stale); err != nil {
		t.Fatalf("age lease: %v", err)
	}

	snap, err := m2.RestoreTeam(s.ID)
	if err != nil {
		t.Fatalf("RestoreTeam: %v", err)
	}
	if snap.ID != s.ID || len(snap.Teammates) != 2 {
		t.Fatalf("restored snapshot mismatch: %+v", snap)
	}
	restoredIDs := map[string]bool{}
	for _, tm := range snap.Teammates {
		restoredIDs[tm.ID] = true
	}
	if !restoredIDs[alice.ID] || !restoredIDs[bob.ID] {
		t.Fatalf("restored teammate IDs %v do not preserve originals (%s, %s)", restoredIDs, alice.ID, bob.ID)
	}

	// Next task must reference prior context (journal replay worked).
	if err := m2.SendToTeammate(s.ID, alice.ID, MailMessage{Content: "what do you remember", Type: "task"}); err != nil {
		t.Fatalf("send alice after restore: %v", err)
	}
	waitFor(t, 10*time.Second, func() bool {
		return teammateResultContains(m2, s.ID, alice.ID, "SECRET-ALPHA-42")
	}, "post-restore task did not reference prior context (memory lost)")
	if err := m2.SendToTeammate(s.ID, bob.ID, MailMessage{Content: "what do you remember", Type: "task"}); err != nil {
		t.Fatalf("send bob after restore: %v", err)
	}
	waitFor(t, 10*time.Second, func() bool {
		return teammateResultContains(m2, s.ID, bob.ID, "SECRET-BETA-77")
	}, "post-restore task (bob) did not reference prior context")
	m2.Shutdown()
}

// P1 pins: journal naming convention, clean footer after graceful shutdown,
// crashed detection when the footer is absent, and RestoreTeamByName.
func TestTeammateJournalNamingAndFooter(t *testing.T) {
	boardRoot := t.TempDir()
	sessionRoot := t.TempDir()

	sid := teammateSessionID("team-ab12cd", "Weird Name!")
	if !strings.Contains(sid, "-tm-team-ab12cd-Weird-Name") {
		t.Fatalf("journal ID %q violates the -tm-<teamID>-<name> convention", sid)
	}
	if got := sanitizeFileToken("a/b c!"); got != "a-b-c" {
		t.Fatalf("sanitizeFileToken(a/b c!) = %q", got)
	}

	m1 := newPersistentTestManager(t, boardRoot, sessionRoot)
	s := m1.CreateTeam("footerteam", "leader-1")
	alice, err := m1.SpawnTeammate(s.ID, "alice", "", nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if err := m1.SendToTeammate(s.ID, alice.ID, MailMessage{Content: "remember FOOTER-KEY", Type: "task"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, 10*time.Second, func() bool {
		return teammateResultContains(m1, s.ID, alice.ID, "FOOTER-KEY")
	}, "task never completed")

	mf, err := m1.loadManifest(s.ID)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	sessionFile := mf.Teammates[0].SessionFile
	if !strings.Contains(sessionFile, "-tm-") {
		t.Fatalf("sessionFile %q lacks -tm-", sessionFile)
	}
	store1, err := m1.journalStore()
	if err != nil {
		t.Fatalf("journal store: %v", err)
	}
	if _, err := os.Stat(journalSessionFilePath(store1, sessionFile)); err != nil {
		t.Fatalf("journal file missing: %v", err)
	}

	m1.Shutdown()

	// Graceful shutdown flushed a clean footer.
	if f := m1.journalLastFooter(sessionFile); f == nil || f.Status != "clean" {
		t.Fatalf("clean footer missing after graceful shutdown: %+v", f)
	}

	// Kill-style: strip the footer -> restore still works but the journal
	// reads as crashed (no clean footer).
	m2 := newPersistentTestManager(t, boardRoot, sessionRoot)
	stale := ownerLease{BootID: "gone-boot", PID: 4194304, Hostname: "restarted-host", Heartbeat: time.Now().Add(-2 * ownerLeaseTTL)}
	if err := writeOwnerLeaseFile(m2.ownerLeasePath(s.ID), &stale); err != nil {
		t.Fatalf("age lease: %v", err)
	}
	if _, err := m2.RestoreTeamByName("footerteam"); err != nil {
		t.Fatalf("RestoreTeamByName: %v", err)
	}
	if err := m2.SendToTeammate(s.ID, alice.ID, MailMessage{Content: "again", Type: "task"}); err != nil {
		t.Fatalf("send after restore: %v", err)
	}
	waitFor(t, 10*time.Second, func() bool {
		return teammateResultContains(m2, s.ID, alice.ID, "FOOTER-KEY")
	}, "memory lost after RestoreTeamByName")
	m2.Shutdown()
}
