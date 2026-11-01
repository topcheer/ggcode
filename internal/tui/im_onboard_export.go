package tui

// Desktop onboarding shim (#4864): exports the merged #4862 wizard
// registry (single source of truth) in a stable shape for the wailskit
// binding. The wizard itself lives in im_onboard.go; this file only
// projects it - no parallel content.

// IMOnboardGuideStep is one step of the exported guide.
type IMOnboardGuideStep struct {
	Text string `json:"text"`
	Note string `json:"note,omitempty"`
	URL  string `json:"url,omitempty"`
}

// IMOnboardGuidePlatform is one platform's exported guide.
type IMOnboardGuidePlatform struct {
	Key   string               `json:"key"`
	Title string               `json:"title"`
	Panel string               `json:"panel"`
	QR    bool                 `json:"qr"`
	Steps []IMOnboardGuideStep `json:"steps"`
}

// IMOnboardGuideExported projects imOnboardWizard() for cross-package
// consumers. Key and Title derive from the registry key (entries are
// keyed by platform id; the display title is the capitalized form).
func IMOnboardGuideExported() []IMOnboardGuidePlatform {
	wiz := imOnboardWizard()
	out := make([]IMOnboardGuidePlatform, 0, len(wiz))
	for key, entry := range wiz {
		steps := make([]IMOnboardGuideStep, 0, len(entry.Steps))
		for _, s := range entry.Steps {
			steps = append(steps, IMOnboardGuideStep{Text: s.Text, Note: s.Note, URL: s.URL})
		}
		out = append(out, IMOnboardGuidePlatform{
			Key:   key,
			Title: entry.Platform,
			Panel: entry.Panel,
			QR:    entry.QR,
			Steps: steps,
		})
	}
	return out
}
