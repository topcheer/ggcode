package swarm

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/util"
)

// Teammate session journals (persistent teams P1).
//
// Every teammate gets a real session journal in the user sessions dir with
// ID <ts>-<rand>-tm-<teamID>-<name>. The agent's persist handler (the same
// write path the main conversation loop uses) appends every turn record,
// so a RestoreTeam replays the journal through the session loader and the
// teammate resumes with its memory intact. The "-tm-" infix keeps teammate
// journals filterable out of user-facing session listers.

// teammateSessionID allocates a journal session ID:
// <ts>-<rand>-tm-<teamID>-<name> (#5829 P1 naming convention).
func teammateSessionID(teamID, name string) string {
	return fmt.Sprintf("%s-%s-tm-%s-%s",
		time.Now().Format("20060102-150405"), randomHex(4), teamID, sanitizeFileToken(name))
}

// sanitizeFileToken reduces an arbitrary display name to a filesystem-safe
// token (journal IDs must round-trip through the session store verbatim).
func sanitizeFileToken(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "mate"
	}
	return util.Truncate(out, 40)
}

// journalStore lazily opens (and caches) the session store for teammate
// journals. SessionRoot overrides the directory (tests); default is the
// user's sessions dir.
func (m *Manager) journalStore() (*session.JSONLStore, error) {
	m.journalMu.Lock()
	defer m.journalMu.Unlock()
	if m.journalStoreCache != nil {
		return m.journalStoreCache, nil
	}
	dir := m.SessionRoot
	if dir == "" {
		var err error
		dir, err = session.DefaultDir()
		if err != nil {
			return nil, err
		}
	}
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		return nil, err
	}
	m.journalStoreCache = store
	return store, nil
}

// allocateTeammateJournal creates the journal session file for a newly
// spawned teammate. Errors are logged and a nil session returned; callers
// treat nil as "journaling unavailable" (teammate still runs in-memory).
func (m *Manager) allocateTeammateJournal(teamID, name string) *session.Session {
	sid := teammateSessionID(teamID, name)
	ses := &session.Session{
		ID:        sid,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Title:     util.Truncate(fmt.Sprintf("teammate %s (%s)", name, teamID), 60),
		Workspace: session.CurrentWorkspacePath(),
	}
	store, err := m.journalStore()
	if err != nil {
		debug.Log("swarm", "journal store unavailable for %s/%s: %v", teamID, name, err)
		return nil
	}
	if err := store.Save(ses); err != nil {
		debug.Log("swarm", "journal allocate %s failed: %v", sid, err)
		return nil
	}
	return ses
}

// persistHandlerSetter is satisfied by agent runners wired to the context
// manager's persist hook (the main-loop journal writer surface).
type persistHandlerSetter interface {
	SetPersistHandler(fn func(msg provider.Message))
}

// historySeeder is satisfied by agent runners that can replay persisted
// messages into the live context manager (session-resume semantics).
type historySeeder interface {
	SeedContextMessages(msgs []provider.Message)
}

// attachTeammateJournaler wires the agent's persist handler to append every
// context message to the journal — write-through, same record format as the
// main conversation loop. IMPORTANT: attach AFTER any history seeding, so
// replayed messages are not re-appended to the journal they came from.
func attachTeammateJournaler(agent AgentRunner, store *session.JSONLStore, ses *session.Session) {
	if agent == nil || store == nil || ses == nil {
		return
	}
	if ph, ok := agent.(persistHandlerSetter); ok {
		ph.SetPersistHandler(func(msg provider.Message) {
			if err := store.AppendMessageToDisk(ses, msg); err != nil {
				debug.Log("swarm", "teammate journal append %s failed: %v", ses.ID, err)
			}
		})
	}
}

// seedAgentHistory replays a loaded journal into the agent's context.
// Compaction on load uses the existing thresholds: the replayed context is
// subject to the same early/reactive compaction as a main-agent resume, so
// over-threshold journals are summarized on the next run boundary — no new
// compaction mode.
func seedAgentHistory(agent AgentRunner, msgs []provider.Message) int {
	seeder, ok := agent.(historySeeder)
	if !ok {
		return 0
	}
	seeder.SeedContextMessages(msgs)
	return len(msgs)
}

// loadTeammateJournal loads a member's journal with full history (resume
// semantics — no time-windowed truncation).
func (m *Manager) loadTeammateJournal(sessionFile string) (*session.Session, error) {
	store, err := m.journalStore()
	if err != nil {
		return nil, err
	}
	return store.LoadWithOptions(sessionFile, true)
}

// journalFooter is the tail record scanned for clean/crashed detection.
type journalFooter struct {
	Type   string `json:"type"`
	Status string `json:"footer_status"`
}

// journalSessionFilePath resolves a session ID to its on-disk journal path
// (sessions/<id>.jsonl, matching JSONLStore's layout).
func journalSessionFilePath(store *session.JSONLStore, sessionID string) string {
	return filepath.Join(store.Dir(), sessionID+".jsonl")
}

// journalLastFooter returns the last teammate_footer record in the journal,
// or nil when the journal has no footer (crashed / killed hard).
func (m *Manager) journalLastFooter(sessionFile string) *journalFooter {
	store, err := m.journalStore()
	if err != nil {
		return nil
	}
	f, err := os.Open(journalSessionFilePath(store, sessionFile))
	if err != nil {
		return nil
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	var last *journalFooter
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.Contains(line, "teammate_footer") {
			continue
		}
		var rec struct {
			Type         string `json:"type"`
			FooterStatus string `json:"footer_status"`
		}
		if json.Unmarshal([]byte(line), &rec) == nil && rec.Type == "teammate_footer" {
			last = &journalFooter{Type: rec.Type, Status: rec.FooterStatus}
		}
	}
	return last
}

// flushTeammateFooter writes the terminal status marker so RestoreTeam can
// distinguish clean shutdowns from crashes. "crashed" footers WARN on
// restore (mirrors the arm-record pattern from #5753).
func (m *Manager) flushTeammateFooter(tm *Teammate, status string) {
	ses := teammateJournalSession(tm)
	if ses == nil {
		return
	}
	store, err := m.journalStore()
	if err != nil {
		return
	}
	if err := store.AppendTeammateFooter(ses, status); err != nil {
		debug.Log("swarm", "teammate footer flush %s (%s) failed: %v", ses.ID, status, err)
	}
}

// teammateJournalSession returns the journal session pointer (nil-safe).
func teammateJournalSession(tm *Teammate) *session.Session {
	if tm == nil {
		return nil
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.journal
}

// teammateJournalID returns the journal session ID recorded for a teammate.
func teammateJournalID(tm *Teammate) string {
	if ses := teammateJournalSession(tm); ses != nil {
		return ses.ID
	}
	return ""
}
