// Package intentions implements the prospective intention layer (#5260):
// pending "when X happens, do Y" statements captured from explicit user
// utterances, activated by cue families (file change / time / event
// observation), with a pending -> fired -> realized/retired lifecycle.
//
// Storage namespace: memory.intentions.* (see docs/design/adr-0001). The
// store is a single workspace-level JSON file guarded by a cross-process
// flock sidecar, mirroring the cron store discipline (internal/cron).
package intentions

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/cron"
)

// CueFamily selects which adapter activates an intention (#5260 AC-2).
type CueFamily string

const (
	// CueFile activates on file changes matched by a glob pattern
	// (mirrors internal/agentruntime FileWatchTrigger semantics).
	CueFile CueFamily = "file"
	// CueTime activates on a cron-expression schedule (reuses cron.NextTime).
	CueTime CueFamily = "time"
	// CueEvent activates when an observed event text matches the pattern
	// (substring, case-insensitive). Observations are pushed via
	// Engine.Observe / the intention tool's observe action.
	CueEvent CueFamily = "event"
)

// ValidCueFamilies lists the supported cue families.
var ValidCueFamilies = []CueFamily{CueFile, CueTime, CueEvent}

// State is the intention lifecycle state (#5260 AC-3).
type State string

const (
	StatePending  State = "pending"
	StateFired    State = "fired"
	StateRealized State = "realized"
	StateRetired  State = "retired"
)

// Intention is one prospective intention record (memory.intentions.*).
type Intention struct {
	ID            string    `json:"id"`
	Statement     string    `json:"statement"`      // "do Y"
	CueFamily     CueFamily `json:"cue_family"`     // file | time | event
	CuePattern    string    `json:"cue_pattern"`    // glob | cron expr | event substring
	OriginSession string    `json:"origin_session"` // session that captured it
	OriginExcerpt string    `json:"origin_excerpt"` // verbatim utterance excerpt
	CreatedAt     time.Time `json:"created_at"`
	State         State     `json:"state"`
	FireCount     int       `json:"fire_count"`
	Repeat        bool      `json:"repeat,omitempty"`       // default false = fire-once
	CooldownSec   int       `json:"cooldown_sec,omitempty"` // for Repeat intentions
	CooldownUntil time.Time `json:"cooldown_until,omitempty"`
	LastFiredAt   time.Time `json:"last_fired_at,omitempty"`
}

// Validate checks the capture invariants (AC-1: statement, cue, origin
// session + excerpt must all be present; pattern must parse for its family).
func (in *Intention) Validate() error {
	if strings.TrimSpace(in.Statement) == "" {
		return fmt.Errorf("intention: statement is required")
	}
	if strings.TrimSpace(in.OriginExcerpt) == "" {
		return fmt.Errorf("intention: origin_excerpt is required (verbatim user utterance provenance)")
	}
	if strings.TrimSpace(in.CuePattern) == "" {
		return fmt.Errorf("intention: cue_pattern is required")
	}
	switch in.CueFamily {
	case CueFile:
		if _, err := filepath.Match(in.CuePattern, ""); err != nil {
			return fmt.Errorf("intention: file cue_pattern is not a valid glob: %w", err)
		}
	case CueTime:
		if _, err := cron.NextTime(in.CuePattern, time.Now()); err != nil {
			return fmt.Errorf("intention: time cue_pattern is not a valid cron expression: %w", err)
		}
	case CueEvent:
		// substring pattern: nothing further to validate
	default:
		return fmt.Errorf("intention: unknown cue_family %q (want file|time|event)", in.CueFamily)
	}
	return nil
}

// dedupKey identifies a semantically identical activation request.
func (in *Intention) dedupKey() string {
	return strings.TrimSpace(in.Statement) + "\x00" + string(in.CueFamily) + "\x00" + strings.TrimSpace(in.CuePattern)
}

// Active reports whether the intention can still fire.
func (in *Intention) Active() bool {
	return in.State == StatePending || (in.State == StateFired && in.Repeat)
}
