package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/sessionidx"
	"github.com/topcheer/ggcode/internal/sessionidxd"
)

// whyMaxTaskHits bounds the checkpoint-provenance listing.
const whyMaxTaskHits = 3

// whyExcerptMax bounds the transcript excerpt length.
const whyExcerptMax = 400

// whyTranscriptLimit bounds the transcript fallback query.
const whyTranscriptLimit = 10

// Why answers "why does this code exist" (#5261): given a file path
// (optionally path:line), it resolves the chain anchor -> checkpoint
// EditIntent -> task description + rationale. When checkpoint provenance
// has been pruned, it degrades gracefully to a session-transcript search
// for the path, surfacing the original user-prompt excerpt instead.
// Fully local, no upload.
type Why struct {
	Checkpoints *checkpoint.Manager // nil until wired; then transcript-only fallback
	Registry    *Registry           // lazy SessionIndex lookup, same as SessionRecall
	Home        string              // ggcode home (dir holding sessions/)
	Workspace   string
}

func (t Why) Name() string { return "why" }

func (t Why) Description() string {
	return "Explain why a piece of code exists: accept a file path (optionally path:line), resolve it to the checkpoint edit intents (task description + rationale) that touched it, and fall back to session-transcript search when checkpoints were pruned. Use before modifying unfamiliar code to recover the original request and reasoning."
}

func (t Why) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"target": {
				"type": "string",
				"description": "File path (absolute or workspace-relative), optionally with :line (e.g. internal/auth/store.go:57)."
			}
		},
		"required": ["target"]
	}`)
}

type whyArgs struct {
	Target string `json:"target"`
}

var commitRe = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

var lineSuffixRe = regexp.MustCompile(`^[0-9]+$`)

func (t Why) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args whyArgs
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	target := strings.TrimSpace(args.Target)
	if target == "" {
		return Result{IsError: true, Content: "target is required"}, nil
	}

	path := target
	if idx := strings.LastIndex(target, ":"); idx > 0 && !commitRe.MatchString(target) {
		if lineSuffixRe.MatchString(target[idx+1:]) {
			path = target[:idx] // path:line -> keep the path part
		}
	}

	if t.Checkpoints != nil {
		if out := t.fromCheckpoints(path); out != "" {
			return Result{Content: out}, nil
		}
	}
	return Result{Content: t.fromTranscripts(path)}, nil
}

func (t Why) fromCheckpoints(path string) string {
	type hit struct {
		checkpoint.TaskSummary // anonymous: TaskID/TaskDesc/Edits/Files promoted
		rationale              string
	}
	var matches []hit
	for _, ts := range t.Checkpoints.TasksWithEdits() {
		for _, f := range ts.Files {
			if whyPathMatches(f, path) {
				rationale := ""
				for _, cp := range t.Checkpoints.EditsForTask(ts.TaskID) {
					if whyPathMatches(cp.FilePath, path) && cp.Intent.Rationale != "" {
						rationale = cp.Intent.Rationale
					}
				}
				matches = append(matches, hit{TaskSummary: ts, rationale: rationale})
				break
			}
		}
	}
	if len(matches) == 0 {
		return ""
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Edits > matches[j].Edits })

	var b strings.Builder
	b.WriteString("## Why this code exists (checkpoint provenance)\n")
	for i, m := range matches {
		if i >= whyMaxTaskHits {
			break
		}
		fmt.Fprintf(&b, "- Task %s: %s (edits=%d, files=%d)", m.TaskID, m.TaskDesc, m.Edits, len(m.Files))
		if m.rationale != "" {
			b.WriteString("\n  Rationale: " + m.rationale)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (t Why) fromTranscripts(path string) string {
	mgr := t.sessionIndex()
	if mgr == nil {
		return fmt.Sprintf("No checkpoint provenance available (pruned or not wired) and no session index in this process; nothing to resolve for %s.", path)
	}
	if self := mgr.Self(); self != nil {
		_ = self.Refresh()
	}
	home := t.Home
	if home == "" {
		home = filepath.Dir(mgr.Dir()) // sessions/ parent = ggcode home
	}
	terms := strings.Fields(strings.ToLower(whyQueryTerms(path)))
	results := mgr.QueryScoped(terms, "", whyTranscriptLimit, t.Workspace)
	if home != "" {
		if daemonRes, ok := sessionidxd.Query(home, mgr.Dir(), terms, "", mgr.LiveID(), whyTranscriptLimit, t.Workspace); ok {
			results = daemonRes
		}
	}
	for _, r := range results {
		snippet, role, _ := extractRecallView(r.Line)
		if role != "user" {
			continue
		}
		if len(snippet) > whyExcerptMax {
			snippet = snippet[:whyExcerptMax] + "…"
		}
		return fmt.Sprintf("## Why this code exists (transcript fallback — checkpoint provenance absent)\n- Original user request excerpt (session %s): %s", shortID(r.SessionID), snippet)
	}
	return fmt.Sprintf("No checkpoint provenance and no transcript hits for %s; the change may predate local history.", path)
}

func (t Why) sessionIndex() *sessionidx.Manager {
	if t.Registry == nil {
		return nil
	}
	return t.Registry.SessionIndex()
}

func whyPathMatches(file, target string) bool {
	return file == target || strings.HasSuffix(file, "/"+target) || strings.HasSuffix(target, "/"+file)
}

func whyQueryTerms(path string) string {
	path = strings.TrimPrefix(path, "./")
	parts := strings.Split(path, "/")
	if len(parts) > 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}
	return path
}
