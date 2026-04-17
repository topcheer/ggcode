# TUI Startup Review (2025-04-18)

## Symptoms (from user report)
1. Mouse clicks produce control sequence characters on terminal
2. Typed text appears directly on terminal instead of TUI input box  
3. Enter key recognized as ctrl+j (0x0A / \n instead of \r)
4. Characters get swallowed — need to type hheelllloo to get hello
5. Happens on any terminal type, intermittently

## Analysis: Bubble Tea v2 Startup Sequence

### Key Timeline in `Program.Run()`
1. `initTerminal()` → `term.MakeRaw(stdin)` — raw mode enabled
2. `initInputReader()` → readLoop goroutine starts reading stdin
3. `startRenderer()` → calls `renderer.start()` but `s.lastView == nil` → **AltScreen/MouseMode NOT set**
4. Sends `ansi.RequestModeSynchronizedOutput + ansi.RequestModeUnicodeCore`
5. `model.Init()` → returns `tea.Batch(blink, RequestWindowSize)`
6. `p.render(model)` → stores View(AltScreen=true) into `s.view`
7. First ticker tick (~16ms later) → `renderer.flush()` → detects alt screen change → writes ANSI sequences

### Key Finding: renderer.start() skips setup when lastView is nil
File: `bubbletea/v2@v2.0.6/cursed_renderer.go:87`
```go
func (s *cursedRenderer) start() {
    if s.lastView == nil {
        return  // <-- AltScreen/MouseMode NOT set here!
    }
}
```

### Mitigation in ggcode
- `inputDrainUntil` — 50ms drain window after `setProgramMsg`
- `startupInputGateWindow` — 500ms startup gate suppressing non-printable keys
- `looksLikeStartupGarbage` — clears input value if it looks like terminal response
- `shouldIgnoreTerminalProbeKey` — filters terminal probe key events
- `looksLikeTerminalResponseInput` — post-update cleanup of accumulated garbage

### Potential Root Causes
1. **Race between raw mode and alt screen**: ~16ms window where raw mode is on but no alt screen
2. **Terminal response misparse**: DECRPM/CPR responses leaking as KeyPressMsg
3. **renderer.flush timing**: First flush in ticker goroutine may race with eventLoop
4. **Suspended/restarted renderer**: If renderer stops and restarts, terminal state may be inconsistent

## Code Locations
- `cmd/ggcode/root.go` → `run()`: TUI startup orchestration (line ~250-530)
- `internal/tui/repl.go` → `Run()`: REPL event loop (line ~195-300)
- `internal/tui/model.go` → `Init()` (line 723), `Update()` (line 982), startup gates
- `internal/tui/view.go` → `View()`: AltScreen=true, MouseMode=MouseModeCellMotion (line 76-83)