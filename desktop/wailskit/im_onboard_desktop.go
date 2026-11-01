package wailskit

import (
	"github.com/topcheer/ggcode/internal/tui"
)

// IMOnboardGuideEntry is the desktop view of one platform's onboarding
// guide, derived from the merged #4862 TUI wizard registry (single source
// of truth, #4864): steps + anti-confusion notes + entry deep-link URLs +
// panel command + QR-pairing flag.
type IMOnboardGuideEntry struct {
	Key      string                   `json:"key"`
	Title    string                   `json:"title"`
	PanelCmd string                   `json:"panelCmd"` // TUI panel; desktop routes to Settings IM card
	Steps    []tui.IMOnboardGuideStep `json:"steps"`
	Notes    []string                 `json:"notes"` // anti-confusion traps ("this ID is NOT that ID")
	URLs     []string                 `json:"urls"`  // entry deep-links (browser-openable on desktop)
	QRAuth   bool                     `json:"qrAuth"`
}

// GetIMOnboardGuide returns the per-platform onboarding guide for the
// desktop onboarding step and Settings IM card. Content is the SAME
// registry the TUI cockpit wizard renders (internal/tui imOnboardWizard),
// so TUI and desktop never drift apart (#4864: single source of truth).
func GetIMOnboardGuide() []IMOnboardGuideEntry {
	src := tui.IMOnboardGuideExported()
	entries := make([]IMOnboardGuideEntry, 0, len(src))
	for _, p := range src {
		e := IMOnboardGuideEntry{
			Key:      p.Key,
			Title:    p.Title,
			PanelCmd: p.Panel,
			QRAuth:   p.QR,
			Steps:    p.Steps,
		}
		for _, s := range p.Steps {
			if s.Note != "" {
				e.Notes = append(e.Notes, s.Note)
			}
			if s.URL != "" {
				e.URLs = append(e.URLs, s.URL)
			}
		}
		entries = append(entries, e)
	}
	return entries
}
