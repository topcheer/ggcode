package harness

// Spec-Driven Development & PRD-to-Code Pipeline
//
// Research: GitHub Spec Kit / Kiro (spec → requirements → design → tasks → code),
// Devin (PR → plan → implement → review), Cursor Composer Mode, and Claude Code
// harness system all implement structured pipelines that decompose a product
// specification into executable coding tasks.
//
// ggcode already has:
//   - harness system (worktree isolation + task execution + review/promote)
//   - planner (complexity detection → plan suggestion)
//   - spec skill (prompt template only, not a structured pipeline)
//
// The gap: there is no structured spec parser that can take a markdown PRD/spec
// and decompose it into harness tasks with acceptance criteria and dependency
// chains. This file fills that gap with a pure-computation parser:
//
//   1. SPEC PARSING: Parse markdown specs into structured SpecRequirements,
//      each with an ID, title, description, acceptance criteria, and
//      dependency references.
//
//   2. TASK DECOMPOSITION: Convert SpecRequirements into harness Tasks,
//      preserving dependency chains (DependsOn) and attaching acceptance
//      criteria as structured fields.
//
//   3. TRACEABILITY: Each generated Task carries a SpecRef linking it back to
//      the originating requirement ID, enabling Code-to-Spec traceability.
//
// All operations are pure computation (string parsing) — no I/O, no blocking,
// no external dependencies. Fully compatible with existing Task/EnqueueTask.

import (
	"fmt"
	"strings"
)

// --- Spec Document Model ---

// SpecDocument represents a parsed product/feature specification.
type SpecDocument struct {
	Title        string            // From the top-level H1 heading
	Overview     string            // From the "## Overview" or "## Background" section
	Requirements []SpecRequirement // Structured requirements extracted from the spec
	DesignNotes  string            // From "## Design Decisions" or "## Technical Design"
}

// SpecRequirement represents a single structured requirement within a spec.
type SpecRequirement struct {
	ID                 string   // e.g., "REQ-1", "FR-2", auto-generated if absent
	Title              string   // Short title from the heading
	Description        string   // Body text describing the requirement
	AcceptanceCriteria []string // Bullet points under "Acceptance:" or "Criteria:"
	DependsOn          []string // References to other requirement IDs (e.g., "REQ-1")
	Priority           string   // "high", "medium", "low" (from "Priority:" line)
}

// --- Spec Parser ---

// ParseSpec parses a markdown specification into a structured SpecDocument.
//
// Supported spec format (inspired by GitHub Spec Kit / Kiro):
//
//	# Feature Title
//
//	## Overview
//	Description text...
//
//	## Requirements
//
//	### REQ-1: First requirement
//	Description of the requirement.
//	- Acceptance: Must validate input
//	- Acceptance: Must return 200 on success
//	- Depends on: REQ-0
//	- Priority: high
//
//	### REQ-2: Second requirement
//	...
//
//	## Design Decisions
//	Technical notes...
//
// The parser is lenient: missing sections are simply empty, and requirement IDs
// are auto-generated when absent. Acceptance criteria are extracted from bullet
// points starting with "Acceptance:" or "Criteria:" or "- Must" / "- Should".
func ParseSpec(content string) *SpecDocument {
	doc := &SpecDocument{}
	lines := splitLines(content)

	var currentSection string
	var currentReq *SpecRequirement

	finishReq := func() {
		if currentReq != nil {
			currentReq.Description = strings.TrimSpace(currentReq.Description)
			doc.Requirements = append(doc.Requirements, *currentReq)
			currentReq = nil
		}
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// H1 → spec title
		if strings.HasPrefix(trimmed, "# ") && !strings.HasPrefix(trimmed, "##") {
			doc.Title = strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
			continue
		}

		// H2 → section switch
		if strings.HasPrefix(trimmed, "## ") {
			finishReq()
			sectionName := strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
			currentSection = strings.ToLower(sectionName)
			continue
		}

		// H3 → requirement (within Requirements section)
		if strings.HasPrefix(trimmed, "### ") {
			isReqSection := strings.Contains(currentSection, "requirement") ||
				strings.Contains(currentSection, "feature") ||
				strings.Contains(currentSection, "acceptance")
			if isReqSection {
				finishReq()
				heading := strings.TrimSpace(strings.TrimPrefix(trimmed, "### "))
				id, title := parseRequirementHeading(heading)
				if id == "" {
					id = autoReqID(len(doc.Requirements) + 1)
				}
				currentReq = &SpecRequirement{
					ID:    id,
					Title: title,
				}
			}
			continue
		}

		// Content routing
		switch {
		case currentReq != nil:
			// We're inside a requirement — parse its sub-fields
			parseRequirementLine(currentReq, trimmed)
		case currentSection == "overview" || currentSection == "background":
			if trimmed != "" {
				if doc.Overview != "" {
					doc.Overview += "\n"
				}
				doc.Overview += trimmed
			}
		case strings.Contains(currentSection, "design") || strings.Contains(currentSection, "decision"):
			if trimmed != "" {
				if doc.DesignNotes != "" {
					doc.DesignNotes += "\n"
				}
				doc.DesignNotes += trimmed
			}
		}
	}
	finishReq()

	// Resolve dependency cross-references
	doc.resolveDependencies()

	return doc
}

// parseRequirementHeading extracts the ID and title from a heading like
// "REQ-1: Login page" or "FR-2. OAuth callback" or just "Login page".
func parseRequirementHeading(heading string) (id, title string) {
	heading = strings.TrimSpace(heading)
	// Common ID prefixes: REQ-N, FR-N, AC-N, US-N (user story), TASK-N
	prefixes := []string{"REQ-", "FR-", "AC-", "US-", "TASK-"}
	upper := strings.ToUpper(heading)
	for _, pfx := range prefixes {
		if strings.HasPrefix(upper, pfx) {
			rest := heading[len(pfx):]
			// Find the end of the ID (digits, then optional separator)
			j := 0
			for j < len(rest) && (rest[j] >= '0' && rest[j] <= '9') {
				j++
			}
			if j > 0 {
				id = pfx + rest[:j]
				remainder := strings.TrimSpace(rest[j:])
				remainder = strings.TrimPrefix(remainder, ":")
				remainder = strings.TrimPrefix(remainder, ".")
				title = strings.TrimSpace(remainder)
				return
			}
		}
	}
	return "", heading
}

// parseRequirementLine parses body lines within a requirement section.
func parseRequirementLine(req *SpecRequirement, line string) {
	// Bullet points with Acceptance/Criteria labels
	lower := strings.ToLower(line)

	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
		body := strings.TrimSpace(line[2:])
		bodyLower := strings.ToLower(body)

		// Acceptance criteria extraction
		if strings.HasPrefix(bodyLower, "acceptance:") ||
			strings.HasPrefix(bodyLower, "criteria:") ||
			strings.HasPrefix(bodyLower, "acceptance criteria:") {
			criteria := strings.TrimSpace(body[strings.IndexByte(body, ':')+1:])
			if criteria != "" {
				req.AcceptanceCriteria = append(req.AcceptanceCriteria, criteria)
			}
			return
		}

		// "Must" / "Should" bullets are implicit acceptance criteria
		if strings.HasPrefix(bodyLower, "must ") || strings.HasPrefix(bodyLower, "should ") {
			req.AcceptanceCriteria = append(req.AcceptanceCriteria, body)
			return
		}

		// Dependency declaration
		if strings.HasPrefix(bodyLower, "depends on:") || strings.HasPrefix(bodyLower, "depends:") {
			depStr := strings.TrimSpace(body[strings.IndexByte(body, ':')+1:])
			for _, dep := range strings.Split(depStr, ",") {
				dep = strings.TrimSpace(dep)
				if dep != "" {
					req.DependsOn = append(req.DependsOn, dep)
				}
			}
			return
		}

		// Priority declaration
		if strings.HasPrefix(bodyLower, "priority:") {
			priority := strings.TrimSpace(body[strings.IndexByte(body, ':')+1:])
			req.Priority = strings.ToLower(priority)
			return
		}

		// Generic bullet → append to description
		if req.Description != "" {
			req.Description += "\n"
		}
		req.Description += body
		return
	}

	// Non-bullet lines: "Depends on: X" or "Priority: high" or "Acceptance: Y"
	if idx := strings.IndexByte(line, ':'); idx > 0 {
		key := strings.TrimSpace(lower[:idx])
		val := strings.TrimSpace(line[idx+1:])
		switch key {
		case "depends on", "depends":
			for _, dep := range strings.Split(val, ",") {
				dep = strings.TrimSpace(dep)
				if dep != "" {
					req.DependsOn = append(req.DependsOn, dep)
				}
			}
			return
		case "priority":
			req.Priority = strings.ToLower(val)
			return
		case "acceptance", "criteria", "acceptance criteria":
			if val != "" {
				req.AcceptanceCriteria = append(req.AcceptanceCriteria, val)
			}
			return
		}
	}

	// Regular text → description
	if line != "" {
		if req.Description != "" {
			req.Description += "\n"
		}
		req.Description += line
	}
}

// resolveDependencies normalizes dependency references and removes self-references.
func (doc *SpecDocument) resolveDependencies() {
	knownIDs := make(map[string]bool, len(doc.Requirements))
	for i := range doc.Requirements {
		knownIDs[strings.ToUpper(doc.Requirements[i].ID)] = true
	}
	for i := range doc.Requirements {
		var normalized []string
		for _, dep := range doc.Requirements[i].DependsOn {
			dep = strings.TrimSpace(strings.ToUpper(dep))
			if dep == "" || dep == doc.Requirements[i].ID {
				continue // skip self-reference
			}
			normalized = append(normalized, dep)
		}
		doc.Requirements[i].DependsOn = dedupStrings(normalized)
	}
}

// autoReqID generates an ID for requirements without explicit IDs.
func autoReqID(n int) string {
	return fmt.Sprintf("REQ-%d", n)
}

// --- Spec → Tasks Converter ---

// SpecToTasksOptions controls how spec requirements are converted to tasks.
type SpecToTasksOptions struct {
	// EntryPoint is the harness entry point assigned to generated tasks.
	EntryPoint string

	// ContextName and ContextPath are assigned to all generated tasks.
	ContextName string
	ContextPath string

	// If true, requirements with no explicit DependsOn are chained
	// sequentially (each depends on the previous). This ensures tasks
	// execute in requirement order. If false, tasks are independent.
	SequentialChain bool
}

// SpecToTasks converts a parsed SpecDocument into a list of harness Tasks,
// preserving requirement IDs as SpecRefs and mapping inter-requirement
// dependencies to Task.DependsOn.
//
// The returned tasks are NOT yet persisted — call EnqueueTask to save them.
// The caller should capture the task IDs from EnqueueTask and use the
// returned ReqIDToTaskID map to resolve cross-task dependencies.
func SpecToTasks(doc *SpecDocument, opts SpecToTasksOptions) ([]*Task, map[string]string) {
	if doc == nil {
		return nil, nil
	}

	// First pass: create tasks (without dependency resolution)
	tasks := make([]*Task, 0, len(doc.Requirements))
	reqIDToIndex := make(map[string]int) // req ID → index in tasks slice

	for i := range doc.Requirements {
		req := &doc.Requirements[i]
		task, _ := NewTask(req.Title, opts.EntryPoint)
		task.Goal = buildTaskGoal(req)
		task.SpecRef = req.ID
		task.AcceptanceCriteria = append([]string(nil), req.AcceptanceCriteria...)
		task.ContextName = opts.ContextName
		task.ContextPath = opts.ContextPath
		task.Metadata = buildSpecMetadata(req, doc)

		tasks = append(tasks, task)
		reqIDToIndex[strings.ToUpper(req.ID)] = i
	}

	// Second pass: resolve dependencies (req ID → task ID)
	reqIDToTaskID := make(map[string]string, len(tasks))
	for i := range tasks {
		reqIDToTaskID[strings.ToUpper(doc.Requirements[i].ID)] = tasks[i].ID
	}

	for i := range doc.Requirements {
		req := &doc.Requirements[i]
		task := tasks[i]

		for _, depReqID := range req.DependsOn {
			depTaskID, ok := reqIDToTaskID[strings.ToUpper(depReqID)]
			if ok {
				task.DependsOn = append(task.DependsOn, depTaskID)
			}
		}

		// Sequential chaining: if no deps and not first, depend on previous
		if opts.SequentialChain && len(task.DependsOn) == 0 && i > 0 {
			task.DependsOn = append(task.DependsOn, tasks[i-1].ID)
		}

		task.DependsOn = dedupStrings(task.DependsOn)
	}

	return tasks, reqIDToTaskID
}

// buildTaskGoal creates a descriptive goal string from a requirement.
func buildTaskGoal(req *SpecRequirement) string {
	var b strings.Builder
	if req.Title != "" {
		b.WriteString(req.Title)
	}
	if req.Description != "" {
		if b.Len() > 0 {
			b.WriteString(": ")
		}
		// Truncate long descriptions for the goal field
		desc := req.Description
		if len(desc) > 300 {
			desc = desc[:297] + "..."
		}
		b.WriteString(desc)
	}
	if b.Len() == 0 {
		return req.ID
	}
	return b.String()
}

// buildSpecMetadata creates a metadata string for the task (used in task.Metadata).
func buildSpecMetadata(req *SpecRequirement, doc *SpecDocument) string {
	var parts []string
	parts = append(parts, "spec_ref="+req.ID)
	if req.Priority != "" {
		parts = append(parts, "priority="+req.Priority)
	}
	if doc.Title != "" {
		parts = append(parts, "spec="+truncateForMetadata(doc.Title, 80))
	}
	return strings.Join(parts, " ")
}

func truncateForMetadata(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// --- Spec Validation ---

// SpecValidationResult reports issues found in a spec document.
type SpecValidationResult struct {
	Errors   []string // Blocking issues
	Warnings []string // Non-blocking issues
}

// HasErrors returns true if there are blocking validation errors.
func (r *SpecValidationResult) HasErrors() bool {
	return len(r.Errors) > 0
}

// ValidateSpec checks a parsed spec for common issues:
//   - Requirements with unresolvable dependency references
//   - Requirements with no acceptance criteria (warning)
//   - Requirements with no description (warning)
//   - Circular dependency chains (error)
func ValidateSpec(doc *SpecDocument) SpecValidationResult {
	var result SpecValidationResult
	if doc == nil {
		result.Errors = append(result.Errors, "spec document is nil")
		return result
	}

	if len(doc.Requirements) == 0 {
		result.Warnings = append(result.Warnings, "no requirements found in spec")
		return result
	}

	knownIDs := make(map[string]bool, len(doc.Requirements))
	for i := range doc.Requirements {
		knownIDs[strings.ToUpper(doc.Requirements[i].ID)] = true
	}

	for i := range doc.Requirements {
		req := &doc.Requirements[i]

		// Check dependency references
		for _, dep := range req.DependsOn {
			if !knownIDs[strings.ToUpper(dep)] {
				result.Errors = append(result.Errors,
					fmt.Sprintf("%s depends on unknown requirement %s", req.ID, dep))
			}
		}

		// Warning: no acceptance criteria
		if len(req.AcceptanceCriteria) == 0 {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("%s has no acceptance criteria", req.ID))
		}

		// Warning: no description
		if strings.TrimSpace(req.Description) == "" && strings.TrimSpace(req.Title) == "" {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("%s has no description", req.ID))
		}
	}

	// Check for circular dependencies
	if cycle := detectCircularDeps(doc); cycle != "" {
		result.Errors = append(result.Errors, "circular dependency detected: "+cycle)
	}

	return result
}

// detectCircularDeps uses DFS to detect cycles in the requirement dependency graph.
// Returns a description of the cycle if found, empty string otherwise.
func detectCircularDeps(doc *SpecDocument) string {
	// Build adjacency list: req ID → depends-on IDs
	graph := make(map[string][]string)
	for i := range doc.Requirements {
		id := strings.ToUpper(doc.Requirements[i].ID)
		for _, dep := range doc.Requirements[i].DependsOn {
			graph[id] = append(graph[id], strings.ToUpper(dep))
		}
	}

	const (
		white = 0 // unvisited
		gray  = 1 // in progress (on current DFS path)
		black = 2 // fully processed
	)
	color := make(map[string]int)

	var visit func(node string, path []string) string
	visit = func(node string, path []string) string {
		color[node] = gray
		path = append(path, node)
		for _, neighbor := range graph[node] {
			if color[neighbor] == gray {
				// Found cycle — extract the cycle path
				start := 0
				for start < len(path) && path[start] != neighbor {
					start++
				}
				return strings.Join(append(path[start:], neighbor), " → ")
			}
			if color[neighbor] == white {
				if cycle := visit(neighbor, path); cycle != "" {
					return cycle
				}
			}
		}
		color[node] = black
		return ""
	}

	// Visit all nodes
	for i := range doc.Requirements {
		id := strings.ToUpper(doc.Requirements[i].ID)
		if color[id] == white {
			if cycle := visit(id, nil); cycle != "" {
				return cycle
			}
		}
	}
	return ""
}

// --- Spec Formatting ---

// FormatSpecSummary renders a human-readable summary of a parsed spec.
func FormatSpecSummary(doc *SpecDocument) string {
	if doc == nil || len(doc.Requirements) == 0 {
		return "Empty spec (no requirements found)."
	}
	var b strings.Builder
	if doc.Title != "" {
		b.WriteString(doc.Title + "\n")
	}
	b.WriteString(fmt.Sprintf("%d requirements:\n", len(doc.Requirements)))
	for i := range doc.Requirements {
		req := &doc.Requirements[i]
		b.WriteString(fmt.Sprintf("  [%s] %s", req.ID, req.Title))
		if len(req.AcceptanceCriteria) > 0 {
			b.WriteString(fmt.Sprintf(" (%d criteria)", len(req.AcceptanceCriteria)))
		}
		if len(req.DependsOn) > 0 {
			b.WriteString(" ← " + strings.Join(req.DependsOn, ", "))
		}
		if req.Priority != "" {
			b.WriteString(" [" + req.Priority + "]")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// --- Helpers ---

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Split(s, "\n")
}

func dedupStrings(s []string) []string {
	seen := make(map[string]bool, len(s))
	result := s[:0]
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	return result
}
