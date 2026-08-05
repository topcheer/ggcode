package agent

// Accessibility (a11y) Intelligence Check
//
// Research basis: WCAG 2.2 (2023), EU Accessibility Act (2025), and ADA
// compliance lawsuits have made a11y a first-class concern in frontend
// development. Industry tools like axe-core (Deque), Lighthouse, and Pa11y
// provide runtime auditing, but no AI coding agent detects a11y issues at
// WRITE TIME -- before the code is even saved.
//
// Competitor analysis:
//   - GitHub Copilot: occasional inline a11y hints, inconsistent
//   - Cursor: relies on extensions (eslint-plugin-jsx-a11y), no built-in
//   - Cline: no write-time a11y detection
//   - Claude Code: relies on agent self-judgment (unreliable)
//   - axe-core/Lighthouse: runtime-only, requires a running app
//
// ggcode's approach: deterministic, zero-LLM-cost pattern detection at
// write time for HTML/Vue/Svelte/JSX/TSX files. We focus on the highest-
// impact, lowest-false-positive checks from WCAG 2.2 Level A/AA:
//
//   1. Missing alt text on <img> tags (WCAG 1.1.1 Non-text Content)
//   2. Interactive div/span without role/tabindex (WCAG 4.1.2 Name/Role)
//   3. Input without label association (WCAG 1.3.1 Info and Relationships)
//   4. Heading level skip h1->h3 (WCAG 1.3.1 / 2.4.6)
//   5. Suspicious aria-* attribute values (WCAG 4.1.2)

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// --- Compiled patterns for performance ---

var imgTagRe = regexp.MustCompile(`(?i)<img\b([^>]*)/?>`)

var divSpanClickRe = regexp.MustCompile(`(?i)<(div|span)\b([^>]*\bonclick\b[^>]*)>`)

var inputTagRe = regexp.MustCompile(`(?i)<input\b([^>]*)/?>`)

var headingRe = regexp.MustCompile(`(?i)<h([1-6])\b`)

var ariaAttrRe = regexp.MustCompile(`(?i)\baria-([a-z]+)\s*=\s*["']([^"']*)["']`)

// validAriaRoles is the set of ARIA roles from the WAI-ARIA 1.2 spec.
var validAriaRoles = map[string]bool{
	"alert": true, "alertdialog": true, "application": true, "article": true,
	"banner": true, "button": true, "cell": true, "checkbox": true,
	"columnheader": true, "combobox": true, "complementary": true,
	"contentinfo": true, "definition": true, "dialog": true, "directory": true,
	"document": true, "feed": true, "figure": true, "form": true,
	"grid": true, "gridcell": true, "group": true, "heading": true,
	"img": true, "link": true, "list": true, "listbox": true,
	"listitem": true, "log": true, "main": true, "marquee": true,
	"math": true, "menu": true, "menubar": true, "menuitem": true,
	"menuitemcheckbox": true, "menuitemradio": true, "navigation": true,
	"none": true, "note": true, "option": true, "presentation": true,
	"progressbar": true, "radio": true, "radiogroup": true, "region": true,
	"row": true, "rowgroup": true, "rowheader": true, "scrollbar": true,
	"search": true, "searchbox": true, "separator": true, "slider": true,
	"spinbutton": true, "status": true, "switch": true, "tab": true,
	"table": true, "tablist": true, "tabpanel": true, "term": true,
	"textbox": true, "timer": true, "toolbar": true, "tooltip": true,
	"tree": true, "treegrid": true, "treeitem": true,
}

// ariaBooleanStates maps aria-* attributes that must be "true" or "false".
var ariaBooleanStates = map[string]bool{
	"aria-hidden": true, "aria-disabled": true, "aria-expanded": true,
	"aria-checked": true, "aria-selected": true, "aria-pressed": true,
	"aria-readonly": true, "aria-required": true, "aria-busy": true,
	"aria-modal": true, "aria-multiline": true, "aria-multiselectable": true,
}

const maxA11yWarnings = 6

// checkAccessibility performs accessibility checks on HTML/JSX/TSX content.
func checkAccessibility(filePath, _, content string) []string {
	ext := strings.ToLower(filepath.Ext(filePath))
	isJSX := ext == ".jsx" || ext == ".tsx" || ext == ".js" || ext == ".ts"
	isMarkup := ext == ".html" || ext == ".htm" || ext == ".vue" || ext == ".svelte" || ext == ".svg"

	if !isJSX && !isMarkup {
		return nil
	}
	if !strings.Contains(content, "<") {
		return nil
	}

	var warnings []string
	warnings = append(warnings, checkMissingAlt(content)...)
	warnings = append(warnings, checkClickableDiv(content)...)
	if isMarkup {
		warnings = append(warnings, checkInputWithoutLabel(content)...)
	}
	warnings = append(warnings, checkHeadingHierarchy(content)...)
	warnings = append(warnings, checkInvalidAria(content)...)

	if len(warnings) == 0 {
		return nil
	}
	if len(warnings) > maxA11yWarnings {
		extra := len(warnings) - maxA11yWarnings
		warnings = warnings[:maxA11yWarnings]
		warnings = append(warnings, fmt.Sprintf("[%d more accessibility issues truncated]", extra))
	}

	result := []string{
		fmt.Sprintf("Accessibility (WCAG 2.2) check found %d issue(s) in %s:", len(warnings), filepath.Base(filePath)),
	}
	result = append(result, warnings...)
	return result
}

// checkMissingAlt detects <img> tags without an alt attribute.
func checkMissingAlt(content string) []string {
	matches := imgTagRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	var warnings []string
	for _, m := range matches {
		if !a11yHasAttr(m[1], "alt") {
			warnings = append(warnings,
				"  - <img> tag missing 'alt' attribute (WCAG 1.1.1). Add descriptive alt text, or alt=\"\" for decorative images.")
		}
	}
	return warnings
}

// checkClickableDiv detects <div>/<span> with onclick but no keyboard access.
func checkClickableDiv(content string) []string {
	matches := divSpanClickRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	var warnings []string
	for _, m := range matches {
		attrs := m[2]
		if !a11yHasAttr(attrs, "role") && !a11yHasAttr(attrs, "tabindex") &&
			!a11yHasAttr(attrs, "onkeydown") && !a11yHasAttr(attrs, "onkeyup") &&
			!a11yHasAttr(attrs, "onkeypress") {
			warnings = append(warnings,
				fmt.Sprintf("  - <%s onclick> without role/tabindex/onkeydown (WCAG 4.1.2). Keyboard users cannot activate this. Use <button> or add role=\"button\" tabindex=\"0\".", m[1]))
		}
	}
	return warnings
}

// inputTypeSkipsLabel lists input types that don't need a label.
var inputTypeSkipsLabel = map[string]bool{
	"hidden": true, "submit": true, "button": true, "reset": true, "image": true,
}

// inputHasLabel checks whether an input element has an accessible name via
// <label for>, aria-label, aria-labelledby, or title.
func inputHasLabel(attrs string, labelTargets map[string]bool) bool {
	if id := a11yGetAttr(attrs, "id"); id != "" && labelTargets[strings.ToLower(id)] {
		return true
	}
	return a11yHasAttr(attrs, "aria-label") || a11yHasAttr(attrs, "aria-labelledby") || a11yHasAttr(attrs, "title")
}

// checkInputWithoutLabel detects <input> elements without an associated label.
func checkInputWithoutLabel(content string) []string {
	matches := inputTagRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}

	labelForRe := regexp.MustCompile(`(?i)<label\b[^>]*\bfor\s*=\s*["']([^"']+)["']`)
	labelTargets := make(map[string]bool)
	for _, lm := range labelForRe.FindAllStringSubmatch(content, -1) {
		labelTargets[strings.ToLower(lm[1])] = true
	}

	typeAttrRe := regexp.MustCompile(`(?i)\btype\s*=\s*["']([^"']+)["']`)
	var warnings []string
	for _, m := range matches {
		attrs := m[1]
		if tm := typeAttrRe.FindStringSubmatch(attrs); tm != nil {
			if inputTypeSkipsLabel[strings.ToLower(tm[1])] {
				continue
			}
		}
		if !inputHasLabel(attrs, labelTargets) {
			warnings = append(warnings,
				"  - <input> without associated <label> (WCAG 1.3.1/3.3.2). Add <label for=\"id\"> or aria-label.")
		}
	}
	return warnings
}

// checkHeadingHierarchy detects skipped heading levels (e.g., h1 to h3).
func checkHeadingHierarchy(content string) []string {
	matches := headingRe.FindAllStringSubmatch(content, -1)
	if len(matches) < 2 {
		return nil
	}
	var warnings []string
	prevLevel := 0
	for _, m := range matches {
		var level int
		fmt.Sscanf(m[1], "%d", &level)
		if prevLevel > 0 && level > prevLevel+1 {
			warnings = append(warnings,
				fmt.Sprintf("  - Heading level skip: h%d -> h%d (WCAG 1.3.1). Screen reader users may miss content.", prevLevel, level))
		}
		prevLevel = level
	}
	return warnings
}

// checkInvalidAria detects suspicious ARIA attribute values and invalid roles.
func checkInvalidAria(content string) []string {
	var warnings []string

	// Check role="..." attribute (not aria-prefixed, but part of WAI-ARIA spec)
	roleRe := regexp.MustCompile(`(?i)\brole\s*=\s*["']([^"']+)["']`)
	for _, m := range roleRe.FindAllStringSubmatch(content, -1) {
		for _, role := range strings.Fields(strings.ToLower(m[1])) {
			if !validAriaRoles[role] {
				warnings = append(warnings,
					fmt.Sprintf("  - Invalid ARIA role: \"%s\" (WCAG 4.1.2). Use a valid WAI-ARIA role (e.g., button, link, navigation).", role))
			}
		}
	}

	// Check aria-* attributes
	matches := ariaAttrRe.FindAllStringSubmatch(content, -1)
	for _, m := range matches {
		attrName := "aria-" + strings.ToLower(m[1])
		value := strings.ToLower(strings.TrimSpace(m[2]))

		if ariaBooleanStates[attrName] {
			if value != "" && value != "true" && value != "false" {
				warnings = append(warnings,
					fmt.Sprintf("  - Invalid aria value: %s=\"%s\" should be \"true\" or \"false\" (WCAG 4.1.2).", attrName, m[2]))
			}
		}
	}
	return warnings
}

// a11yHasAttr checks if an attribute name exists in the attribute string.
func a11yHasAttr(attrs, name string) bool {
	re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(name) + `\s*=`)
	return re.MatchString(attrs)
}

// a11yGetAttr extracts the value of an attribute.
func a11yGetAttr(attrs, name string) string {
	re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(name) + `\s*=\s*["']([^"']*)["']`)
	m := re.FindStringSubmatch(attrs)
	if m != nil {
		return m[1]
	}
	return ""
}
