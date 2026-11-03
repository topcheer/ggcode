package memory

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const conventionDriftCap = 5

var makeTargetLine = regexp.MustCompile(`^make\s+([a-zA-Z0-9_.-]+)\s*$`)

var scriptLine = regexp.MustCompile(`^(?:bash|sh)\s+(\.?/?[\w./-]+\.(?:sh|bash))`)

var makefileTargetLine = regexp.MustCompile(`^([a-zA-Z0-9_.%-]+)\s*:(?:[^=].*)?$`)

func ScanConventionDrift(content, workingDir string) []string {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	var findings []string
	seen := make(map[string]bool)
	add := func(f string) {
		if !seen[f] && len(findings) < conventionDriftCap {
			seen[f] = true
			findings = append(findings, f)
		}
	}

	targets := makefileTargets(filepath.Join(workingDir, "Makefile"))

	hasMakefile := len(targets) > 0

	for _, block := range fencedBlocks(content) {
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if m := makeTargetLine.FindStringSubmatch(line); m != nil && hasMakefile {
				if !targets[m[1]] {
					add("make target \"" + m[1] + "\" is referenced but not defined in Makefile")
				}
				continue
			}
			if m := scriptLine.FindStringSubmatch(line); m != nil {
				p := m[1]
				if !filepath.IsAbs(p) {
					p = filepath.Join(workingDir, p)
				}
				if _, err := os.Stat(p); err != nil {
					add("script \"" + m[1] + "\" does not exist")
				}
			}
		}
	}
	return findings
}

func fencedBlocks(content string) []string {
	var blocks []string
	var cur []string
	in := false
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if in {
				blocks = append(blocks, strings.Join(cur, "\n"))
				cur = nil
			}
			in = !in
			continue
		}
		if in {
			cur = append(cur, line)
		}
	}
	if in && len(cur) > 0 {
		blocks = append(blocks, strings.Join(cur, "\n"))
	}
	return blocks
}

func makefileTargets(path string) map[string]bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	targets := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		if m := makefileTargetLine.FindStringSubmatch(line); m != nil {

			if strings.HasPrefix(m[1], ".") || strings.HasPrefix(m[1], "%") {
				continue
			}
			targets[m[1]] = true
		}
	}
	return targets
}
