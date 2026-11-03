package agent

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/codehealth"
	"github.com/topcheer/ggcode/internal/debug"
)

const (
	maxComplexityGateWarnings = 5

	maxComplexityGateFires = 3

	complexityGateThreshold = 20

	complexityGateMaxLength = 120

	complexityGateMaxNesting = 5

	complexityGateAnalyzeThreshold = 1
	complexityGateAnalyzeMaxFuncs  = 1000
)

type complexityGateState struct {
	reported map[string]bool
	fires    int
}

func newComplexityGateState() *complexityGateState {
	return &complexityGateState{}
}

var complexityBaselineFn = gitBaselineMetrics

func (a *Agent) checkComplexityGate(runStats *RunStats) string {
	if a.complexityGate.fires >= maxComplexityGateFires {
		return ""
	}

	goFiles := filterGoSourceFiles(runStats.FilesEdited)
	if len(goFiles) == 0 {
		return ""
	}

	workingDir := a.WorkingDir()
	allHotspots, newKeys := a.collectComplexityHotspots(goFiles, workingDir)

	if len(allHotspots) == 0 {
		debug.Log("complexity-gate", "passed: no new critical complexity in %d edited Go file(s)", len(goFiles))
		return ""
	}

	if a.complexityGate.reported == nil {
		a.complexityGate.reported = make(map[string]bool)
	}
	for _, key := range newKeys {
		a.complexityGate.reported[key] = true
	}
	a.complexityGate.fires++

	reported := allHotspots
	if len(reported) > maxComplexityGateWarnings {
		reported = reported[:maxComplexityGateWarnings]
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf(
		"Code quality advisory: %d function(s) in your edited files have high complexity. "+
			"Consider refactoring before finalizing:\n",
		len(allHotspots),
	))
	for _, fn := range reported {
		parts := []string{fmt.Sprintf("complexity=%d", fn.Complexity)}
		if fn.Length > complexityGateMaxLength {
			parts = append(parts, fmt.Sprintf("length=%d lines", fn.Length))
		}
		if fn.NestingDepth > complexityGateMaxNesting {
			parts = append(parts, fmt.Sprintf("nesting=%d", fn.NestingDepth))
		}
		b.WriteString(fmt.Sprintf("- %s (%s): %s\n", fn.Function, filepath.Base(fn.File), strings.Join(parts, ", ")))
	}
	if len(allHotspots) > len(reported) {
		b.WriteString(fmt.Sprintf("... and %d more\n", len(allHotspots)-len(reported)))
	}
	b.WriteString("\nThese are advisory - refactoring is recommended but not required for completion.")

	debug.Log("complexity-gate", "fired: %d hotspot(s) in %d file(s)", len(allHotspots), len(goFiles))
	return b.String()
}

func (a *Agent) collectComplexityHotspots(goFiles []string, workingDir string) ([]codehealth.FuncMetrics, []string) {
	allHotspots := make([]codehealth.FuncMetrics, 0, len(goFiles))
	var newKeys []string
	for _, relPath := range goFiles {
		absPath := relPath
		if !filepath.IsAbs(absPath) && workingDir != "" {
			absPath = filepath.Join(workingDir, relPath)
		}

		if codehealth.IsGenerated(absPath) {
			debug.Log("complexity-gate", "skipping generated file %s", relPath)
			continue
		}

		opts := codehealth.DefaultOptions()
		opts.ThresholdComplexity = complexityGateAnalyzeThreshold
		opts.MaxFunctions = complexityGateAnalyzeMaxFuncs
		report, err := codehealth.Analyze(absPath, opts)
		if err != nil {
			debug.Log("complexity-gate", "failed to analyze %s: %v", relPath, err)
			continue
		}
		if report == nil {
			continue
		}

		baseline, hasBaseline := complexityBaselineFn(absPath, workingDir)

		for _, fn := range report.TopFunctions {
			if !isComplexityHotspot(fn) {
				continue
			}
			if hasBaseline {
				if base, ok := baseline[fn.Function]; ok && !hotspotWorsened(fn, base) {

					continue
				}
			}
			key := relPath + ":" + fn.Function
			if a.complexityGate.reported[key] {
				continue
			}

			fn.File = relPath
			allHotspots = append(allHotspots, fn)
			newKeys = append(newKeys, key)
		}
	}
	return allHotspots, newKeys
}

func hotspotWorsened(fn, base codehealth.FuncMetrics) bool {
	return fn.Complexity > base.Complexity ||
		fn.Length > base.Length ||
		fn.NestingDepth > base.NestingDepth
}

func gitBaselineMetrics(absPath, workingDir string) (map[string]codehealth.FuncMetrics, bool) {
	if workingDir == "" {
		return nil, false
	}
	out, err := exec.Command("git", "-C", workingDir, "rev-parse", "--is-inside-work-tree").Output()
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		debug.Log("complexity-gate", "no git baseline for %s (not a repo: %v)", absPath, err)
		return nil, false
	}

	rel := absPath
	if r, rerr := filepath.Rel(workingDir, absPath); rerr == nil && !strings.HasPrefix(r, "..") {
		rel = r
	}

	spec := "HEAD:./" + filepath.ToSlash(rel)
	var stderr bytes.Buffer
	cmd := exec.Command("git", "-C", workingDir, "show", spec)
	cmd.Stderr = &stderr
	content, err := cmd.Output()
	if err != nil {
		msg := stderr.String()
		if strings.Contains(msg, "does not exist in") || strings.Contains(msg, "exists on disk, but not in") {

			return map[string]codehealth.FuncMetrics{}, true
		}
		debug.Log("complexity-gate", "baseline unavailable for %s: %v (%s)", rel, err, strings.TrimSpace(msg))
		return nil, false
	}

	report, err := codehealth.AnalyzeSource(rel, content, codehealth.Options{
		ThresholdComplexity: complexityGateAnalyzeThreshold,
		MaxFunctions:        complexityGateAnalyzeMaxFuncs,
	})
	if err != nil {
		debug.Log("complexity-gate", "failed to parse HEAD revision of %s: %v", rel, err)
		return nil, false
	}
	baseline := make(map[string]codehealth.FuncMetrics, len(report.TopFunctions))
	for _, fn := range report.TopFunctions {
		baseline[fn.Function] = fn
	}
	return baseline, true
}

func isComplexityHotspot(fn codehealth.FuncMetrics) bool {
	return fn.Complexity >= complexityGateThreshold ||
		fn.Length > complexityGateMaxLength ||
		fn.NestingDepth > complexityGateMaxNesting
}

func filterGoSourceFiles(paths []string) []string {
	var result []string
	for _, p := range paths {
		if !strings.HasSuffix(p, ".go") {
			continue
		}
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		result = append(result, p)
	}
	return result
}
