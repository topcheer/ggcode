package tui

// Update handles all Bubble Tea messages and is defined in model_update.go for file-size
// manageability. See model.go for the Model struct definition and other methods.

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/safego"
)

func (m Model) Update(msg tea.Msg) (model tea.Model, cmd tea.Cmd) {
	defer func() {
		next, ok := model.(Model)
		if !ok {
			return
		}
		model, cmd = next.withTerminalTitleCmd(cmd)
	}()

	m.syncAsyncStateCaches()
	model = m

	// #FREEZE 2026-09-19: consume the usage-probe dirty flag here instead
	// of setActiveRuntimeSelection calling program.Send directly - Send
	// before p.Run deadlocks on the nil-ctx select (first-frame freeze
	// when an old session restored a vendor). Update running at all
	// proves the program loop is live; the probe result is delivered via
	// a goroutine Send which is safe at this point.
	if m.usageProbeDirty {
		m.usageProbeDirty = false
		m2, probeCmd := m.handleUsageSidebarRefreshMsg()
		m = m2
		if probeCmd != nil {
			prog := m.program
			go safego.Run("usage.dirtyProbe", func() {
				if msg := probeCmd(); msg != nil && prog != nil {
					prog.Send(msg)
				}
			})
		}
	}
	// Handle spinner ticks first
	var spinnerCmd tea.Cmd
	if m.spinner.IsActive() {
		spinnerCmd = m.spinner.Update(msg)
	}

	// #2422: the 133-case type switch is now an exact-type dispatch table
	// (model_update_dispatch.go). Semantics are identical for concrete
	// message types; interface-typed cases live just below.
	if m2, hit, c := m.dispatchUpdate(msg, spinnerCmd); hit {
		return m2, c
	}

	// tea.MouseMsg is an interface type, so it cannot be an exact-type
	// table key - it stays a residual type check. It must run AFTER the
	// table so tea.MouseWheelMsg (a MouseMsg implementer with its own
	// table entry) keeps taking its dedicated branch first, exactly as
	// the old switch ordered them.
	if _, isMouse := msg.(tea.MouseMsg); isMouse {
		// Option/Alt+mouse: release mouse to terminal for native text selection
		return m, nil
	}

	// Skip spinnerMsg and blinkMsg — they fire every tick and would flood the log.
	if _, isSpinner := msg.(spinnerMsg); !isSpinner {
		msgType := fmt.Sprintf("%T", msg)
		if !strings.Contains(msgType, "Blink") {
			// Don't log every bubbletea internal message — cursor blink alone generates ~160 lines/min
		}
	}
	_, isKeyPress := msg.(tea.KeyPressMsg)
	if !isKeyPress {
		// Non-keyboard messages still need to reach the textinput so its
		// virtual cursor can process blink scheduling messages
		// (cursor.initialBlinkMsg / cursor.BlinkMsg). Without this the
		// composer cursor never blinks and, depending on the cursor's
		// initial IsBlinked state, may not be visible at all.
		// textinput.Update ignores message types it doesn't handle, so this
		// forward is safe — the input value is only mutated on
		// KeyPressMsg/PasteMsg, which take dedicated branches earlier in
		// this Update function.
		var fwdCmd tea.Cmd
		m.input, fwdCmd = m.input.Update(msg)
		return m, combineCmds(spinnerCmd, fwdCmd)
	}
	var keyCmd tea.Cmd
	// During startup input drain, suppress all keyboard input.
	if !m.inputDrainUntil.IsZero() && time.Now().Before(m.inputDrainUntil) {
		// Don't log dropped keypresses during input drain
		return m, spinnerCmd
	}
	// Before inputReady, discard all keyboard input (same reason as KeyPressMsg handler).
	if !m.inputReady {
		// Don't log dropped keypresses when not ready
		return m, spinnerCmd
	}

	oldValue := m.input.Value()
	m.input, keyCmd = m.input.Update(msg)
	newValue := m.input.Value()
	if oldValue != newValue {
		// Don't log input field changes
	}

	// Update autocomplete state based on current input
	m.updateAutoComplete()

	// Clear input hint when user types
	if oldValue != newValue {
		m.inputHint = ""
	}

	return m, combineCmds(spinnerCmd, keyCmd)
}
