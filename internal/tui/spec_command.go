package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/spec"
)

// handleSpecCommand implements the /spec slash command family for
// spec-driven development (Spec Kit / Kiro style):
//
//	/spec                       list specs and the active one
//	/spec <slug> [title]        create a spec skeleton and activate it
//	/spec activate <slug>       activate an existing spec
//	/spec off                   deactivate the active spec
func (m *Model) handleSpecCommand(args []string) tea.Cmd {
	wd := workingDirFromModel(m)
	if len(args) == 0 {
		m.printSpecList(wd)
		return nil
	}
	switch args[0] {
	case "off":
		if err := spec.ClearActive(wd); err != nil {
			m.chatWriteSystem(nextSystemID(), m.t("spec.err", err))
			return nil
		}
		m.chatWriteSystem(nextSystemID(), m.t("spec.off"))
		return nil
	case "activate":
		if len(args) < 2 {
			m.chatWriteSystem(nextSystemID(), m.t("spec.usage"))
			return nil
		}
		slug, err := spec.SanitizeSlug(args[1])
		if err != nil {
			m.chatWriteSystem(nextSystemID(), m.t("spec.err", err))
			return nil
		}
		if err := spec.SetActive(wd, slug); err != nil {
			m.chatWriteSystem(nextSystemID(), m.t("spec.notfound", slug))
			return nil
		}
		m.chatWriteSystem(nextSystemID(), m.t("spec.activated", slug))
		return nil
	default:
		slug, err := spec.SanitizeSlug(args[0])
		if err != nil {
			m.chatWriteSystem(nextSystemID(), m.t("spec.usage"))
			return nil
		}
		title := strings.Join(args[1:], " ")
		if _, err := spec.Create(wd, slug, title); err != nil {
			// Existing spec: treat as activate request instead of failing hard.
			if strings.Contains(err.Error(), "already exists") {
				if aerr := spec.SetActive(wd, slug); aerr != nil {
					m.chatWriteSystem(nextSystemID(), m.t("spec.err", err))
					return nil
				}
				m.chatWriteSystem(nextSystemID(), m.t("spec.activated", slug))
				return nil
			}
			m.chatWriteSystem(nextSystemID(), m.t("spec.err", err))
			return nil
		}
		if err := spec.SetActive(wd, slug); err != nil {
			m.chatWriteSystem(nextSystemID(), m.t("spec.err", err))
			return nil
		}
		m.chatWriteSystem(nextSystemID(), m.t("spec.created", slug))
		return nil
	}
}

// printSpecList renders the spec table plus active marker and progress.
func (m *Model) printSpecList(wd string) {
	specs, err := spec.LoadAll(wd)
	if err != nil {
		m.chatWriteSystem(nextSystemID(), m.t("spec.err", err))
		return
	}
	var b strings.Builder
	b.WriteString(m.t("spec.list_header"))
	if len(specs) == 0 {
		b.WriteString(m.t("spec.none"))
		b.WriteString("\n")
	}
	active := spec.Active(wd)
	for _, s := range specs {
		marker := "  "
		if active != nil && active.Slug == s.Slug {
			marker = "* "
		}
		p := s.Progress()
		fmt.Fprintf(&b, "%s%s — %s [%d/%d]\n", marker, s.Slug, s.Title, p.Done, p.Total)
	}
	b.WriteString(m.t("spec.usage"))
	m.chatWriteSystem(nextSystemID(), strings.TrimRight(b.String(), "\n"))
}
