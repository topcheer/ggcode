package tui

// LLM session title refinement (config auto_title_llm, default off).
//
// Layered on top of the deterministic heuristics in
// internal/agentruntime/auto_title.go: after the first or second completed
// run, when the heuristic pipeline left a weak title (empty or generic),
// fire ONE cheap side LLM call that distills the task from the first turn.
// The agentruntime helpers gate every step; any failure keeps the
// heuristic title (fail-open), and the sanitized result is applied on the
// main tea loop via llmTitleReadyMsg — the goroutine only captures
// immutable strings plus the provider/program pointers, never the live
// session, so no message-slice race is possible.

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/session"
)

// llmTitleTimeout bounds the side call; a title is metadata and must never
// hang around waiting on a slow provider.
const llmTitleTimeout = 20 * time.Second

// llmTitleReadyMsg carries the raw LLM reply back to the main tea loop,
// where sanitize/validate/apply all happen synchronously.
type llmTitleReadyMsg struct {
	title        string
	currentTitle string // title snapshot at fire time; drop if it changed meanwhile
}

// maybeLLMRefineSessionTitle is called from handleAgentDoneMsg right after
// the heuristic maybeRefineSessionTitle. It fires the optional LLM title
// call when: enabled via config, a provider is available, we are still in
// the first two assistant turns, and the heuristic title is weak.
func (m Model) maybeLLMRefineSessionTitle() {
	if m.session == nil || m.sessionStore == nil || m.agent == nil || m.program == nil {
		return
	}
	if m.config == nil || !m.config.AutoTitleLLM {
		return
	}
	prov := m.agent.Provider()
	if prov == nil {
		return
	}
	assistantTurns := 0
	for _, msg := range m.session.Messages {
		if msg.Role == "assistant" {
			assistantTurns++
		}
	}
	if assistantTurns < 1 || assistantTurns > 2 {
		return
	}
	current := m.session.Title
	if !agentruntime.TitleWantsLLMRefine(current) {
		return
	}
	userExcerpt, assistantExcerpt := firstTurnExcerpts(m.session)
	prompt, ok := agentruntime.BuildLLMTitlePrompt(userExcerpt, assistantExcerpt)
	if !ok {
		return
	}
	safego.Go("tui.llmTitle", func() {
		ctx, cancel := context.WithTimeout(context.Background(), llmTitleTimeout)
		defer cancel()
		resp, err := prov.Chat(ctx, []provider.Message{{
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: prompt}},
		}}, nil)
		if err != nil {
			debug.Log("tui", "llm title call failed (keeping heuristic title): %v", err)
			return
		}
		var raw string
		if resp != nil {
			for _, block := range resp.Message.Content {
				if block.Type == "text" && block.Text != "" {
					if raw != "" {
						raw += " "
					}
					raw += block.Text
				}
			}
		}
		if raw == "" {
			debug.Log("tui", "llm title call returned empty text (keeping heuristic title)")
			return
		}
		m.program.Send(llmTitleReadyMsg{title: raw, currentTitle: current})
	})
}

// handleLLMTitleReadyMsg applies a validated LLM title on the main loop.
// The currentTitle guard drops stale replies when the title changed while
// the call was in flight (heuristic refine, /title).
func (m Model) handleLLMTitleReadyMsg(msg llmTitleReadyMsg) (Model, tea.Cmd) {
	if m.session == nil || m.sessionStore == nil {
		return m, nil
	}
	if m.session.Title != msg.currentTitle {
		debug.Log("tui", "llm title dropped: title changed during call (%q != %q)",
			msg.currentTitle, m.session.Title)
		return m, nil
	}
	candidate := agentruntime.SanitizeLLMTitle(msg.title)
	if !agentruntime.LLMTitleAcceptable(m.session.Title, candidate) {
		debug.Log("tui", "llm title rejected after sanitize: %q", candidate)
		return m, nil
	}
	oldTitle := m.session.Title
	m.session.Title = candidate
	m.session.UpdatedAt = time.Now()

	ses := m.session
	store := m.sessionStore
	safego.Go("tui.appendMetaToDisk", func() {
		_ = store.AppendMetaToDisk(ses)
	})

	debug.Log("tui", "llm-refined session title: %q -> %q", oldTitle, candidate)
	return m, nil
}

// firstTurnExcerpts extracts the first user message and first assistant
// reply (text blocks only, tool traffic ignored) for the title prompt.
// Called on the main loop so the goroutine only closes over immutable
// strings.
func firstTurnExcerpts(ses *session.Session) (string, string) {
	var userExcerpt, assistantExcerpt string
	for _, msg := range ses.Messages {
		if msg.Role != "user" {
			continue
		}
		for _, block := range msg.Content {
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
				userExcerpt = block.Text
				break
			}
		}
		if userExcerpt != "" {
			break
		}
	}
	for _, msg := range ses.Messages {
		if msg.Role != "assistant" {
			continue
		}
		var parts []string
		for _, block := range msg.Content {
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
				parts = append(parts, block.Text)
			}
		}
		assistantExcerpt = strings.Join(parts, " ")
		break
	}
	return userExcerpt, assistantExcerpt
}
