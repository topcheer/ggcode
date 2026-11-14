package a2aintrospect

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/a2a"
)

// Sources aggregates the seven v3 faces from real runtime sources (#5597).
// All content is VERBATIM (user ruling: same workspace = trust domain, no
// redaction layers). Faces left nil are omitted from the snapshot.
type Sources struct {
	Status   func() *a2a.RuntimeStatus
	Inflight func() *a2a.RuntimeInflight

	// Conversation serves verbatim turns for the window before the cursor
	// (oldest-first), plus whether more exist (next cursor = opaque token
	// the implementation understands).
	Conversation func(cursor string, limit int) (turns []a2a.RuntimeConversationTurn, next string, truncated bool)

	// Activity serves recent tool events + touched files with the same
	// cursor contract.
	Activity func(cursor string, limit int) (events []a2a.RuntimeActivityEvent, files []a2a.RuntimeFileTouch, next string, truncated bool)

	Children func() *a2a.RuntimeChildren
	Schedule func() *a2a.RuntimeSchedule
	Env      func() *a2a.RuntimeEnv

	DefaultConvLimit int // default 20
	DefaultActLimit  int // default 50

	// Now overrides the snapshot clock in tests.
	Now func() time.Time
}

func (s *Sources) convLimit(req a2a.SnapshotRequest) int {
	if req.LimitConv > 0 {
		return req.LimitConv
	}
	if s.DefaultConvLimit > 0 {
		return s.DefaultConvLimit
	}
	return 20
}

func (s *Sources) actLimit(req a2a.SnapshotRequest) int {
	if req.LimitAct > 0 {
		return req.LimitAct
	}
	if s.DefaultActLimit > 0 {
		return s.DefaultActLimit
	}
	return 50
}

func (s *Sources) wants(faces []string, face string) bool {
	if len(faces) == 0 {
		return true
	}
	for _, f := range faces {
		if strings.EqualFold(f, face) {
			return true
		}
	}
	return false
}

// RuntimeSnapshotV3 aggregates the wired faces. brief depth keeps headers
// and counters; full serves verbatim content with cursors.
func (s *Sources) RuntimeSnapshotV3(req a2a.SnapshotRequest) (*a2a.RuntimeSnapshotV3, error) {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	snap := &a2a.RuntimeSnapshotV3{Schema: "v3", TakenAt: now().UTC().Format(time.RFC3339), Depth: req.Depth}
	if snap.Depth == "" {
		snap.Depth = a2a.DepthFull
	}
	faces := req.Faces

	if s.wants(faces, "status") && s.Status != nil {
		snap.Status = s.Status()
	}
	if s.wants(faces, "inflight") && s.Inflight != nil {
		snap.Inflight = s.Inflight()
	}
	if s.wants(faces, "conversation") && s.Conversation != nil {
		limit := s.convLimit(req)
		if snap.Depth == a2a.DepthBrief {
			limit = 5
		}
		turns, next, trunc := s.Conversation(req.CursorConv, limit)
		if snap.Depth == a2a.DepthBrief {
			for i := range turns {
				if len(turns[i].Text) > 120 {
					turns[i].Text = turns[i].Text[:120] + "..."
				}
			}
		}
		snap.Conversation = &a2a.RuntimeConversation{Turns: turns, NextCursor: next, Truncated: trunc}
	}
	if s.wants(faces, "activity") && s.Activity != nil {
		limit := s.actLimit(req)
		if snap.Depth == a2a.DepthBrief {
			limit = 10
		}
		events, files, next, trunc := s.Activity(req.CursorAct, limit)
		snap.Activity = &a2a.RuntimeActivity{Events: events, Files: files, NextCursor: next, Truncated: trunc}
	}
	if s.wants(faces, "children") && s.Children != nil {
		snap.Children = s.Children()
	}
	if s.wants(faces, "schedule") && s.Schedule != nil {
		snap.Schedule = s.Schedule()
	}
	if s.wants(faces, "env") && s.Env != nil {
		snap.Env = s.Env()
	}
	return snap, nil
}

// ConversationFromTurns adapts a simple slice (no pagination) to the cursor
// contract: the cursor is a plain index into the remaining tail.
func ConversationFromTurns(all []a2a.RuntimeConversationTurn) func(cursor string, limit int) ([]a2a.RuntimeConversationTurn, string, bool) {
	return func(cursor string, limit int) ([]a2a.RuntimeConversationTurn, string, bool) {
		start := 0
		if cursor != "" {
			if n, err := strconv.Atoi(cursor); err == nil && n > 0 && n <= len(all) {
				start = n
			}
		}
		end := start + limit
		trunc := false
		if end >= len(all) {
			end = len(all)
		} else {
			trunc = true
		}
		window := all[start:end]
		next := ""
		if trunc {
			next = strconv.Itoa(end)
		}
		return window, next, trunc
	}
}

// TailJSONL returns the last n non-empty lines of a JSONL file (oldest
// first). Missing file yields nil.
func TailJSONL(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var ring = make([]string, 0, n)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if len(ring) == n {
			copy(ring, ring[1:])
			ring[n-1] = line
		} else {
			ring = append(ring, line)
		}
	}
	return ring
}
