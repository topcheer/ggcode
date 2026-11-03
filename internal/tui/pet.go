package tui

import (
	"math/rand/v2"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type petAnimMsg struct{}

func petFrameDuration() time.Duration {
	return time.Duration(700+rand.IntN(700)) * time.Millisecond
}

func startPetAnim() tea.Cmd {
	return tea.Tick(petFrameDuration(), func(time.Time) tea.Msg {
		return petAnimMsg{}
	})
}

type petState struct {
	blinkLeft int
	mood      string
}

func (m Model) renderPet() string {
	if !m.petEnabled || m.pet == nil {
		return ""
	}

	var face string
	switch {
	case m.pet.blinkLeft > 0:
		face = "(´• ω •`)"
	case m.pet.mood == "happy":
		face = "(◕ ᴗ ◕✿)"
	case m.pet.mood == "sleepy":
		face = "(˘ω˘ )"
	default:
		face = "(◕ ᴗ ◕)"
	}

	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FF69B4")).
		Padding(0, 1)

	return style.Render(face)
}

func (m Model) handlePetAnimMsg(_ petAnimMsg) (Model, tea.Cmd) {
	if !m.petEnabled || m.pet == nil {
		return m, nil
	}

	if m.pet.blinkLeft > 0 {
		m.pet.blinkLeft--
	}

	if m.pet.blinkLeft == 0 && rand.IntN(100) < 15 {
		m.pet.blinkLeft = 2
	}

	if m.pet.mood != "" && rand.IntN(100) < 8 {
		m.pet.mood = ""
	}

	return m, startPetAnim()
}
