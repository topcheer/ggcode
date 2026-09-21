package tui

// Aux-model session title upgrade.
//
// After the first user message of a session persists, kick a one-shot
// auxiliary-model call to generate a better session title. Fire-and-forget:
// any failure keeps the existing title (store heuristic or placeholder),
// and a user-chosen title is never overwritten.

import (
	"context"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/session"
)

// maybeScheduleAuxTitle schedules the aux-model title upgrade at most once
// per session. Called from the persist handler for user messages.
func (r *REPL) maybeScheduleAuxTitle(msg provider.Message) {
	cfg := r.cfg
	if cfg == nil {
		cfg = r.model.config
	}
	if cfg == nil || r.agent == nil {
		return
	}
	text := firstTextBlock(msg)
	if strings.TrimSpace(text) == "" {
		return
	}
	ses := r.getCurrentSession()
	if ses == nil {
		return
	}
	sid := ses.ID
	if _, done := r.auxTitleTried.Load(sid); done {
		return
	}
	r.auxTitleTried.Store(sid, true)
	if !agentruntime.NeedsLLMTitleUpgrade(ses.Title, text) {
		return
	}
	auxP, err := agentruntime.AuxProvider(cfg)
	if err != nil {
		// Expected when no aux model is configured; stay silent and keep
		// the deterministic title.
		debug.Log("tui", "aux title: %v", err)
		return
	}
	store := r.store
	safego.Go("tui.auxTitle", func() {
		ctx, cancel := context.WithTimeout(context.Background(), agentruntime.AuxTitleTimeout)
		defer cancel()
		title, err := agentruntime.GenerateLLMTitle(ctx, auxP, text)
		if err != nil {
			debug.Log("tui", "aux title generation failed: %v", err)
			return
		}
		// Apply under the same sessionMutex the checkpoint handler uses,
		// then persist outside the lock (same pattern, repl.go).
		mu := r.model.sessionMutex()
		mu.Lock()
		cur := r.getCurrentSession()
		if cur == nil || cur.ID != sid || !agentruntime.ShouldAutoTitle(cur.Title) {
			// Session switched or the user titled it meanwhile - drop ours.
			mu.Unlock()
			return
		}
		cur.Title = title
		cur.UpdatedAt = time.Now()
		mu.Unlock()

		if jsonlStore, ok := store.(*session.JSONLStore); ok && jsonlStore != nil {
			if err := jsonlStore.AppendMetaToDisk(cur); err != nil {
				debug.Log("tui", "aux title persist failed: %v", err)
			}
		}
		debug.Log("tui", "session title upgraded via aux model: %q", title)
	})
}

// firstTextBlock extracts the first non-empty text block of a message.
func firstTextBlock(msg provider.Message) string {
	for _, b := range msg.Content {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			return b.Text
		}
	}
	return ""
}
