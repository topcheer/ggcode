# Desktop Streaming UX Research — 2026-07

## Research Question
How do production AI chat interfaces (ChatGPT, Claude, Cursor) handle streaming text rendering, and what can ggcode desktop improve?

## Key Sources
1. **akashbuilds.com** — "Why React Apps Lag With Streaming Text and How ChatGPT Streams Smoothly" (2025)
2. **thefrontkit.com** — "AI Chat UI Best Practices for 2026"
3. **dev.to/pockit_tools** — "The Complete Guide to Streaming LLM Responses in Web Applications" (2025)

## Findings

### 1. Token Buffering & Batching (ChatGPT's Secret)
ChatGPT does NOT re-render on every token. Instead:
- Tokens are collected in a `useRef` buffer (no re-render)
- A `setInterval` or `requestAnimationFrame` loop flushes the buffer to state every 30-50ms
- Users perceive smooth word-by-word rendering, but React only re-renders ~20 times/sec
- Without this: 50+ tokens/sec x full message list re-render = jank

**Code pattern:**
```typescript
const bufferRef = useRef<string[]>([])

function onToken(token: string) {
  bufferRef.current.push(token) // no re-render
}

useEffect(() => {
  const interval = setInterval(() => {
    if (bufferRef.current.length > 0) {
      setMessages(prev => flushBuffer(prev, bufferRef.current))
      bufferRef.current = []
    }
  }, 50) // 20 updates/sec — feels instant
  return () => clearInterval(interval)
}, [])
```

### 2. Deferred Heavy Rendering During Streaming
- Defer code block syntax highlighting until streaming completes
- Defer Mermaid diagram rendering until closing fence arrives
- Buffer incomplete markdown before rendering (half-open `**bold` shouldn't break layout)
- Use `useDeferredValue` or `useTransition` for expensive computations

### 3. Layout Stability
- Each new token should NOT cause layout reflow of surrounding elements
- Use CSS `contain: content` on message containers
- Pre-allocate space for streaming content areas

### 4. Virtualization for Long Sessions
- Only render messages currently visible on screen
- Tools: `react-window`, `react-virtualized`
- Keeps DOM light even with hundreds of messages

### 5. Accessibility During Streaming
- `aria-live="polite"` on response container (ggcode has this)
- `aria-atomic="false"` so only new tokens are announced (ggcode has this)
- **Debounce screen reader announcements** — announcing every token is overwhelming; batch every few seconds
- Keyboard navigation: Tab order flows logically through all interactive elements

### 6. Stop/Retry/Edit Controls
- Stop generation button must be prominent during streaming (ggcode has this)
- Retry last response with one click (ggcode missing)
- Edit and resubmit previous prompt (ggcode missing — requires backend hook)

## ggcode Desktop Current State (2026-07)

### Already Implemented
| Feature | Status | Location |
|---------|--------|----------|
| Typing indicator (3 bouncing dots) | Done | ChatView.tsx L1967 |
| Streaming cursor (green pulse bar) | Done | ChatView.tsx L2500 |
| Stop/cancel button during streaming | Done | ChatView.tsx L2077 |
| Status bar (Working/Thinking + elapsed) | Done | ChatView.tsx L1362 |
| aria-live="polite" + role="log" | Done | ChatView.tsx L1847 |
| Message timestamps | Done | ChatView.tsx L2508 |
| Agent elapsed timer | Done | ChatView.tsx L369 |
| Auto-scroll with near-bottom detection | Done | ChatView.tsx |
| Scroll-to-bottom button + unread badge | Done | ChatView.tsx |

### Missing (Prioritized)
| Gap | Priority | Impact | Complexity |
|-----|----------|--------|------------|
| Token buffering/batching | HIGH | Eliminates streaming jank | Medium (hook + ChatView integration) |
| Message entrance animation | MEDIUM | Polish, smoother feel | Low (CSS only) |
| Tool call shimmer skeleton | MEDIUM | Better loading feedback | Low (CSS only) |
| Deferred markdown rendering | MEDIUM | Faster streaming | Medium (split render path) |
| Virtualization for long sessions | LOW | Only matters at 100+ msgs | High (react-window integration) |
| Retry last response button | LOW | UX convenience | Medium (backend hook needed) |
| Debounced ARIA announcements | LOW | Screen reader UX | Low |

## Implemented This Cycle

### 1. CSS Streaming UX (`style.css`)
- **Message entrance animation** (`.msg-enter`): fade + slide-in for new messages
- **Tool call shimmer** (`.tool-shimmer`): skeleton loading bar for pending tool results
- **Streaming gradient cursor** (`.streaming-cursor`): hue-shifting pulse bar (green→blue)
- **Reasoning glow** (`.reasoning-active`): subtle pulsing border on reasoning panel
- **`prefers-reduced-motion` support**: all animations disabled for accessibility

### 2. Token Buffering Hook (`useStreamBuffer.ts`)
- New file: `frontend/src/hooks/useStreamBuffer.ts`
- Collects text/reasoning chunks in a ref, flushes on `requestAnimationFrame`
- Passthrough mode when disabled (non-streaming events)
- Ready for integration into ChatView.tsx when team is available
- **Integration plan** (requires ChatView.tsx changes — needs team coordination):
  1. Replace direct `setMessages` in 'text' case with `buffer()`
  2. Replace direct `setMessages` in 'reasoning' case with `buffer()`
  3. Add `flush()` before tool_call_done, done, error events
  4. onFlush callback applies all buffered chunks via `setMessages`

## Next Steps
1. Integrate `useStreamBuffer` into ChatView.tsx (coordinate with gg_godev/ggcxf_pm)
2. Apply `.msg-enter` class to message wrappers in ChatView render
3. Apply `.tool-shimmer` to streaming tool calls without content
4. Apply `.streaming-cursor` instead of inline style pulse bar
5. Consider `useDeferredValue` for markdown content during active streaming
6. Research virtualization options if long sessions become a pain point
