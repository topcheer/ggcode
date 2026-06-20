package tui

import (
	"math/rand/v2"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// petAnimMsg is sent periodically by the pet timer to trigger a blink frame.
type petAnimMsg struct{}

// petFrameDuration returns the next interval for the pet animation timer.
// Blink rate is irregular (700-1400ms) to feel more lifelike.
func petFrameDuration() time.Duration {
	return time.Duration(700+rand.IntN(700)) * time.Millisecond
}

// startPetAnim returns a command that waits for the next pet animation frame.
func startPetAnim() tea.Cmd {
	return tea.Tick(petFrameDuration(), func(time.Time) tea.Msg {
		return petAnimMsg{}
	})
}

// petState holds the animation state for the terminal pet.
type petState struct {
	blinkLeft int    // remaining blink ticks (0 = eyes open phase)
	mood      string // "" = normal, "happy" = after successful response, "sleepy" = idle long
}

// renderPet returns the ASCII art for the pet at the current animation state.
// The pet is rendered in a fixed-width area so it can be placed beside the
// composer input box without layout shifts.
func (m Model) renderPet() string {
	if !m.petEnabled || m.pet == nil {
		return ""
	}

	var face string
	switch {
	case m.pet.blinkLeft > 0:
		face = "(´• ω •`)" // closed eyes (blinking)
	case m.pet.mood == "happy":
		face = "(◕ ᴗ ◕✿)" // happy face with flower
	case m.pet.mood == "sleepy":
		face = "(˘ω˘ )" // sleepy
	default:
		face = "(◕ ᴗ ◕)" // normal eyes open
	}

	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FF69B4")).
		Padding(0, 1)

	return style.Render(face)
}

// handlePetAnimMsg processes the pet animation tick.
func (m Model) handlePetAnimMsg(_ petAnimMsg) (Model, tea.Cmd) {
	if !m.petEnabled || m.pet == nil {
		return m, nil
	}

	// Decrement blink counter
	if m.pet.blinkLeft > 0 {
		m.pet.blinkLeft--
	}

	// 15% chance to start a blink (2 ticks = ~1.5s with eyes closed)
	if m.pet.blinkLeft == 0 && rand.IntN(100) < 15 {
		m.pet.blinkLeft = 2
	}

	// Clear transient moods after a while
	if m.pet.mood != "" && rand.IntN(100) < 8 {
		m.pet.mood = ""
	}

	return m, startPetAnim()
}
