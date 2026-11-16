package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/intentions"
)

// IntentionTool exposes the prospective intention layer (#5260): capture
// "when X happens, do Y" statements from explicit user utterances, list
// them, confirm realization, retire them, and push event observations.
type IntentionTool struct {
	Store  *intentions.Store
	Engine *intentions.Engine
}

func (t IntentionTool) Name() string { return "intention" }

func (t IntentionTool) Description() string {
	return "Capture and manage prospective intentions - deferred tasks of the form " +
		"'when X happens, do Y' - stated by the user. Storage namespace memory.intentions.* " +
		"(isolated from session instructions, see docs/design/adr-0001). " +
		"Actions: put (capture; requires statement, cue_family file|time|event, cue_pattern " +
		"[glob | cron expr | event substring], and origin_excerpt - the verbatim user words " +
		"that requested this, e.g. 'when CI is green on #5257, continue the release'); " +
		"list (active intentions, all=true includes realized/retired); " +
		"realize (id - confirm the deferred task was done); " +
		"retire (id - cancel as obsolete); " +
		"observe (kind, text - push an event observation, e.g. kind='tool_result' " +
		"text='CI checks passed on #5257', to activate event-family intentions)."
}

func (t IntentionTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["put", "list", "realize", "retire", "observe"],
				"description": "Operation to perform"
			},
			"statement": {
				"type": "string",
				"description": "(put) The deferred task to perform when the cue activates, e.g. 'continue the release for #5257'"
			},
			"cue_family": {
				"type": "string",
				"enum": ["file", "time", "event"],
				"description": "(put) Activation family: file (glob change), time (cron expression), event (observed event text substring)"
			},
			"cue_pattern": {
				"type": "string",
				"description": "(put) glob path (file) | cron expression like '*/5 * * * *' (time) | case-insensitive substring such as 'CI green on #5257' (event)"
			},
			"origin_excerpt": {
				"type": "string",
				"description": "(put) Verbatim excerpt of the user utterance that requested this intention (required provenance)"
			},
			"origin_session": {
				"type": "string",
				"description": "(put) Session id the utterance came from; defaults to this workspace's capturing session"
			},
			"repeat": {
				"type": "boolean",
				"description": "(put) Re-arm after firing instead of fire-once (default false)"
			},
			"cooldown_sec": {
				"type": "integer",
				"description": "(put) Cooldown for repeat intentions, default 10s"
			},
			"id": {
				"type": "string",
				"description": "(realize/retire) Intention id from put/list"
			},
			"all": {
				"type": "boolean",
				"description": "(list) Include realized/retired intentions"
			},
			"kind": {
				"type": "string",
				"description": "(observe) Short surface tag, e.g. tool_result | im_message | ci_event"
			},
			"text": {
				"type": "string",
				"description": "(observe) Observed event content to match against event-family patterns"
			}
		},
		"required": ["action"]
	}`)
}

func (t IntentionTool) Execute(ctx context.Context, args json.RawMessage) (Result, error) {
	var a struct {
		Action        string `json:"action"`
		Statement     string `json:"statement"`
		CueFamily     string `json:"cue_family"`
		CuePattern    string `json:"cue_pattern"`
		OriginExcerpt string `json:"origin_excerpt"`
		OriginSession string `json:"origin_session"`
		Repeat        bool   `json:"repeat"`
		CooldownSec   int    `json:"cooldown_sec"`
		ID            string `json:"id"`
		All           bool   `json:"all"`
		Kind          string `json:"kind"`
		Text          string `json:"text"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("intention: invalid arguments: %v", err)}, nil
		}
	}

	if t.Store == nil {
		return Result{IsError: true, Content: "intention: store not available"}, nil
	}

	switch a.Action {
	case "put":
		in, err := t.Store.Put(intentions.Intention{
			Statement:     a.Statement,
			CueFamily:     intentions.CueFamily(a.CueFamily),
			CuePattern:    a.CuePattern,
			OriginSession: a.OriginSession,
			OriginExcerpt: a.OriginExcerpt,
			Repeat:        a.Repeat,
			CooldownSec:   a.CooldownSec,
		})
		if err != nil {
			return Result{IsError: true, Content: err.Error()}, nil
		}
		return Result{Content: fmt.Sprintf(
			"intention captured: id=%s cue=%s:%q statement=%q origin_session=%s excerpt=%q state=%s\n"+
				"It will surface in-session when its cue activates; confirm with action=realize id=%s when done.",
			in.ID, in.CueFamily, in.CuePattern, in.Statement, in.OriginSession, in.OriginExcerpt, in.State, in.ID)}, nil

	case "list":
		items := t.Store.List(a.All)
		var b strings.Builder
		if len(items) == 0 {
			return Result{Content: "no active intentions"}, nil
		}
		for _, in := range items {
			fmt.Fprintf(&b, "%s [%s] %s:%q -> %q (fires=%d, origin_session=%s, created=%s)\n",
				in.ID, in.State, in.CueFamily, in.CuePattern, in.Statement, in.FireCount, in.OriginSession, in.CreatedAt.Format("2006-01-02T15:04Z"))
		}
		return Result{Content: strings.TrimRight(b.String(), "\n")}, nil

	case "realize":
		if a.ID == "" {
			return Result{IsError: true, Content: "intention: realize requires id"}, nil
		}
		if err := t.Store.Realize(a.ID); err != nil {
			return Result{IsError: true, Content: err.Error()}, nil
		}
		return Result{Content: fmt.Sprintf("intention %s realized", a.ID)}, nil

	case "retire":
		if a.ID == "" {
			return Result{IsError: true, Content: "intention: retire requires id"}, nil
		}
		if err := t.Store.Retire(a.ID); err != nil {
			return Result{IsError: true, Content: err.Error()}, nil
		}
		return Result{Content: fmt.Sprintf("intention %s retired", a.ID)}, nil

	case "observe":
		if t.Engine == nil {
			return Result{IsError: true, Content: "intention: engine not available for observations"}, nil
		}
		t.Engine.Observe(a.Kind, a.Text)
		return Result{Content: "observation delivered to event-family intentions"}, nil

	default:
		return Result{IsError: true, Content: fmt.Sprintf("intention: unknown action %q (want put|list|realize|retire|observe)", a.Action)}, nil
	}
}
