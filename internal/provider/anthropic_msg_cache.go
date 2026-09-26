package provider

import (
	"os"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/topcheer/ggcode/internal/debug"
)

// Incremental conversation cache breakpoint for the Anthropic Messages API.
//
// Prompt-cache prefix coverage is hierarchical: tools → system → messages.
// Before this change, buildParams placed cache_control breakpoints on the
// tool block and the system blocks only, so the entire conversation history
// was re-processed at full input price on every turn. The standard agent
// pattern (Claude Code, Manus "Context Engineering for AI Agents", Anthropic
// prompt-caching docs "Cache Control" example) is to additionally place ONE
// breakpoint on the tail of the conversation — typically the latest
// tool_result — so each turn extends the cached prefix incrementally instead
// of repaying it.
//
// The breakpoint moves forward one message per turn: cache entries are
// prefix-addressed, so a stored prefix from turn N remains a valid hit for
// turn N+1's request as long as the earlier bytes are unchanged (append-only
// message discipline, see #1672/#2445).
//
// The Anthropic API caps requests at 4 cache breakpoints. This file counts
// the breakpoints already present (tools, server tools, memory tool, system)
// and only adds the message breakpoint when budget remains — a missed
// optimization never invalidates the request.
//
// Kill switch: GGCODE_MSG_CACHE_BREAKPOINT=off restores the previous
// behavior (breakpoints on tools/system only).

// anthropicMaxCacheBreakpoints is the API-wide limit per request.
const anthropicMaxCacheBreakpoints = 4

// messageCacheBreakpointEnabled reports whether the incremental conversation
// breakpoint is active. On by default; GGCODE_MSG_CACHE_BREAKPOINT=off opts out.
func messageCacheBreakpointEnabled() bool {
	return os.Getenv("GGCODE_MSG_CACHE_BREAKPOINT") != "off"
}

// countCacheBreakpoints counts cache_control markers already attached to a
// built request's tool and system blocks, so the message-layer breakpoint
// stays within the 4-breakpoint budget.
func countCacheBreakpoints(params *anthropic.MessageNewParams) int {
	n := 0
	for _, b := range params.System {
		if b.CacheControl != (anthropic.CacheControlEphemeralParam{}) {
			n++
		}
	}
	for _, u := range params.Tools {
		if toolUnionHasCacheControl(&u) {
			n++
		}
	}
	return n
}

// toolUnionHasCacheControl reports whether a tool union carries a breakpoint.
// Mirrors the variants handled by setToolUnionCacheControl.
func toolUnionHasCacheControl(u *anthropic.ToolUnionParam) bool {
	switch {
	case u.OfTool != nil:
		return u.OfTool.CacheControl != (anthropic.CacheControlEphemeralParam{})
	case u.OfWebSearchTool20250305 != nil:
		return u.OfWebSearchTool20250305.CacheControl != (anthropic.CacheControlEphemeralParam{})
	case u.OfWebFetchTool20250910 != nil:
		return u.OfWebFetchTool20250910.CacheControl != (anthropic.CacheControlEphemeralParam{})
	case u.OfMemoryTool20250818 != nil:
		return u.OfMemoryTool20250818.CacheControl != (anthropic.CacheControlEphemeralParam{})
	case u.OfToolSearchToolRegex20251119 != nil:
		return u.OfToolSearchToolRegex20251119.CacheControl != (anthropic.CacheControlEphemeralParam{})
	case u.OfToolSearchToolBm25_20251119 != nil:
		return u.OfToolSearchToolBm25_20251119.CacheControl != (anthropic.CacheControlEphemeralParam{})
	}
	return false
}

// applyIncrementalCacheBreakpoint attaches one cache_control marker to the
// last cache-eligible block of the last message. Thinking/redacted_thinking
// blocks and raw server-tool echo blocks cannot carry cache_control and are
// skipped; the marker lands on the nearest preceding eligible block
// (tool_result, text, image, tool_use). Returns true when applied.
func applyIncrementalCacheBreakpoint(msgs []anthropic.MessageParam) bool {
	for mi := len(msgs) - 1; mi >= 0; mi-- {
		blocks := msgs[mi].Content
		for bi := len(blocks) - 1; bi >= 0; bi-- {
			blk := &msgs[mi].Content[bi]
			switch {
			case blk.OfToolResult != nil:
				blk.OfToolResult.CacheControl = anthropic.NewCacheControlEphemeralParam()
				return true
			case blk.OfText != nil:
				blk.OfText.CacheControl = anthropic.NewCacheControlEphemeralParam()
				return true
			case blk.OfImage != nil:
				blk.OfImage.CacheControl = anthropic.NewCacheControlEphemeralParam()
				return true
			case blk.OfToolUse != nil:
				blk.OfToolUse.CacheControl = anthropic.NewCacheControlEphemeralParam()
				return true
			}
			// thinking / redacted_thinking / server-tool unions: not
			// cache_control-eligible; keep scanning backwards.
		}
	}
	return false
}

// addMessageCacheBreakpoint is the buildParams integration point: budget-check
// and apply the incremental conversation breakpoint.
func addMessageCacheBreakpoint(params *anthropic.MessageNewParams) {
	if !messageCacheBreakpointEnabled() {
		return
	}
	used := countCacheBreakpoints(params)
	if used >= anthropicMaxCacheBreakpoints {
		debug.Log("anthropic", "msg cache breakpoint skipped: %d breakpoints already at budget", used)
		return
	}
	if applyIncrementalCacheBreakpoint(params.Messages) {
		debug.Log("anthropic", "msg cache breakpoint applied (%d/%d budget used)", used+1, anthropicMaxCacheBreakpoints)
	}
}
