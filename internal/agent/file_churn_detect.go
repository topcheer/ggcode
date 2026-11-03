package agent

import (
	"fmt"
	"strings"
)

const (
	churnThreshold = 3

	churnMaxTracked = 50

	churnMaxWarningFiles = 3
)

type churnState struct {
	editCounts map[string]int
	fired      bool
}

func newChurnState() *churnState {
	return &churnState{
		editCounts: make(map[string]int),
	}
}

func (c *churnState) reset() {
	c.editCounts = make(map[string]int)
	c.fired = false
}

func (c *churnState) recordVerifySuccess(cmd string) {

	scopes := commandPathScopes1561(cmd)
	if len(scopes) == 0 {
		for p := range c.editCounts {
			delete(c.editCounts, p)
		}
		return
	}
	for p := range c.editCounts {
		for _, sc := range scopes {
			if pathUnderScope1561(p, sc) {
				delete(c.editCounts, p)
				break
			}
		}
	}
}

func commandPathScopes1561(cmd string) []string {
	var scopes []string
	for _, tok := range strings.Fields(strings.ToLower(cmd)) {
		if tok == "./..." || tok == "." || tok == "..." {
			return nil
		}
		if strings.HasPrefix(tok, "./") || strings.HasPrefix(tok, "/") ||
			(strings.Contains(tok, "/") && !strings.Contains(tok, "=")) {
			scopes = append(scopes, strings.TrimSuffix(tok, "/..."))
		}
	}
	return scopes
}

func pathUnderScope1561(p, scope string) bool {
	scope = strings.TrimSuffix(scope, "/...")
	scope = strings.TrimPrefix(scope, "./")
	if scope == "" || scope == "/" || scope == "." {
		return true
	}
	pp := strings.ToLower(p)
	if strings.HasPrefix(pp, scope+"/") || pp == scope {
		return true
	}

	return strings.Contains(pp, "/"+scope+"/")
}

func (c *churnState) recordEdit(paths []string) {
	for _, p := range paths {
		if p == "" {
			continue
		}
		c.editCounts[p]++

		if len(c.editCounts) > churnMaxTracked {
			var minKey string
			var minVal int = -1
			for k, v := range c.editCounts {
				if minVal == -1 || v < minVal {
					minKey = k
					minVal = v
				}
			}
			if minKey != "" && minKey != p {
				delete(c.editCounts, minKey)
			}
		}
	}
}

func (c *churnState) check() string {
	if c.fired {
		return ""
	}

	var churned []string
	for path, count := range c.editCounts {
		if count >= churnThreshold {
			churned = append(churned, path)
		}
	}

	if len(churned) == 0 {
		return ""
	}

	c.fired = true
	return c.formatGuidance(churned)
}

func (c *churnState) formatGuidance(churnedFiles []string) string {
	type fileCount struct {
		path  string
		count int
	}
	var fcs []fileCount
	for _, fp := range churnedFiles {
		fcs = append(fcs, fileCount{fp, c.editCounts[fp]})
	}

	for i := 0; i < len(fcs); i++ {
		for j := i + 1; j < len(fcs); j++ {
			if fcs[j].count > fcs[i].count {
				fcs[i], fcs[j] = fcs[j], fcs[i]
			}
		}
	}

	if len(fcs) > churnMaxWarningFiles {
		fcs = fcs[:churnMaxWarningFiles]
	}

	var sb strings.Builder
	sb.WriteString("[Assumption Invalidation Alert] You have edited the following file(s) ")
	sb.WriteString(fmt.Sprintf("3+ times in this run:\n"))
	for _, fc := range fcs {
		sb.WriteString(fmt.Sprintf("  - %s (%d edits)\n", fc.path, fc.count))
	}
	sb.WriteString("\n")
	sb.WriteString("Repeated edits to the same file indicate that your initial assumptions ")
	sb.WriteString("about its structure or content were wrong. Each re-edit is a patch on a ")
	sb.WriteString("flawed mental model.\n\n")
	sb.WriteString("Recommended actions:\n")
	sb.WriteString("1. STOP editing and re-read the file holistically to update your understanding\n")
	sb.WriteString("2. Check if the file has dependencies or interfaces you're violating\n")
	sb.WriteString("3. Consider whether the approach itself needs to change rather than patching further\n")
	sb.WriteString("4. If fixing one issue keeps breaking another, the root cause may be architectural\n")
	sb.WriteString("Continuing to patch without understanding will compound errors.")

	return sb.String()
}
