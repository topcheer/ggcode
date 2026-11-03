package agent

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"
)

const (
	returnCountThreshold = 6

	maxReturnCountWarnings = 3
)

type returnCountInstance struct {
	funcName string
	recvType string
	pos      token.Position
	count    int
}

func (i returnCountInstance) rcFingerprint() string {
	return fmt.Sprintf("%s|%s|%d", i.recvType, i.funcName, i.count)
}

func checkExcessiveReturns(filePath, oldContent, newContent string) []string {
	if !strings.HasSuffix(filePath, ".go") {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	isTestFile := strings.HasSuffix(filePath, "_test.go")

	newInstances := findExcessiveReturns(newContent, isTestFile)
	if len(newInstances) == 0 {
		return nil
	}

	var oldCounts map[string]int
	if strings.TrimSpace(oldContent) != "" {
		for _, iss := range findExcessiveReturns(oldContent, isTestFile) {
			if oldCounts == nil {
				oldCounts = make(map[string]int)
			}

			oldCounts[iss.rcFingerprint()]++
		}
	}

	var warnings []string
	newCount := 0
	for _, inst := range newInstances {
		if oldCounts[inst.rcFingerprint()] > 0 {
			oldCounts[inst.rcFingerprint()]--
			continue
		}
		newCount++
		if len(warnings) < maxReturnCountWarnings {
			warnings = append(warnings, fmt.Sprintf(
				"Too many return statements: function `%s` at %s has %d return statements. "+
					"Functions with %d+ returns are harder to maintain and reason about - "+
					"consider extracting helper functions or restructuring control flow. "+
					"(SonarQube S114, Structured Programming)",
				inst.funcName, inst.pos.String(), inst.count,
				returnCountThreshold,
			))
		}
	}

	if newCount > maxReturnCountWarnings {
		warnings = append(warnings, fmt.Sprintf(
			"...and %d more function(s) with %d+ return statements",
			newCount-maxReturnCountWarnings, returnCountThreshold,
		))
	}

	return warnings
}

func findExcessiveReturns(src string, isTestFile bool) []returnCountInstance {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	file, fset, err := parseGoSource("", src, 0)
	if err != nil || file == nil {
		return nil
	}

	var instances []returnCountInstance

	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}

		if isTestFile && isTestOrBenchFunction(fn.Name.Name) {
			return true
		}

		if fn.Body == nil {
			return true
		}

		count := countReturns(fn.Body)
		if count >= returnCountThreshold {
			pos := fset.Position(fn.Pos())
			instances = append(instances, returnCountInstance{
				funcName: fn.Name.Name,
				recvType: recvTypeText(fn.Recv),
				pos:      pos,
				count:    count,
			})
		}

		return true
	})

	return instances
}

func countReturns(node ast.Node) int {
	count := 0
	ast.Inspect(node, func(n ast.Node) bool {
		if _, ok := n.(*ast.ReturnStmt); ok {
			count++
		}

		if _, ok := n.(*ast.FuncLit); ok && n != node {
			return false
		}
		return true
	})
	return count
}
