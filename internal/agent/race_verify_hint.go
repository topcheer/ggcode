package agent

import (
	"go/ast"
	"path/filepath"
	"strconv"
	"strings"
)

func checkRaceVerifyHint(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	if strings.HasSuffix(filePath, "_test.go") {
		return nil
	}

	oldScore := countConcurrencyPrimitives(oldContent)
	newScore := countConcurrencyPrimitives(newContent)

	if newScore > oldScore {
		return raceVerifyHintMessages()
	}

	{
		oldSync := countSyncPrimitives(oldContent)
		newSync := countSyncPrimitives(newContent)
		if newSync < oldSync {
			oldGoroutines := countGoroutineLaunches(oldContent)
			newGoroutines := countGoroutineLaunches(newContent)
			if newGoroutines >= oldGoroutines && newGoroutines > 0 {
				return raceVerifyHintMessages()
			}
		}
	}
	return nil
}

func countSyncPrimitives(src string) int {
	if strings.TrimSpace(src) == "" {
		return 0
	}
	n := 0
	for _, marker := range []string{
		"sync.Mutex", "sync.RWMutex", "sync.WaitGroup", "sync.Once",
		"sync.Cond", "sync.Map",
		".Lock()", ".Unlock()", ".RLock()", ".RUnlock()",
		".Wait()", ".Add(", ".Done()",
		"atomic.", "chan ",
	} {
		n += strings.Count(src, marker)
	}
	return n
}

func raceVerifyHintMessages() []string {
	return []string{
		"Concurrency code modified -- data races are invisible to static analysis. " +
			"Verify with `go test -race ./...` to catch concurrent read/write hazards " +
			"that compile fine but fail non-deterministically at runtime.",
	}
}

func countGoroutineLaunches(src string) int {
	if strings.TrimSpace(src) == "" {
		return 0
	}
	file, _, err := parseGoSource("", src, 0)
	if err != nil || file == nil {
		return strings.Count(src, "go ")
	}
	count := 0
	ast.Inspect(file, func(n ast.Node) bool {
		if _, ok := n.(*ast.GoStmt); ok {
			count++
		}
		return true
	})
	return count
}

func countConcurrencyPrimitives(src string) int {
	if strings.TrimSpace(src) == "" {
		return 0
	}

	file, _, err := parseGoSource("", src, 0)
	if err != nil || file == nil {

		return countConcurrencyPatternsString(src)
	}

	count := 0

	aliasToPath := map[string]string{"sync": "sync", "atomic": "sync/atomic"}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		local := ""
		if imp.Name != nil {
			local = imp.Name.Name
			if local == "_" || local == "." {
				continue
			}
		} else {

			parts := strings.Split(path, "/")
			local = parts[len(parts)-1]
		}
		if path == "sync" || path == "sync/atomic" {
			aliasToPath[local] = path
		}
	}

	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.GoStmt:

			count++

		case *ast.SelectorExpr:

			if ident, ok := n.X.(*ast.Ident); ok {
				pkg := aliasToPath[ident.Name]
				sel := n.Sel.Name
				switch pkg {
				case "sync":
					switch sel {
					case "Mutex", "RWMutex", "WaitGroup", "Map",
						"Once", "Cond", "Pool":
						count++
					}
				case "sync/atomic":

					count++
				}
			}

		case *ast.SendStmt:

			count++
		}
		return true
	})

	return count
}

func countConcurrencyPatternsString(src string) int {
	count := 0

	count += strings.Count(src, "go func(") + strings.Count(src, "go func (")

	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "go ") && !strings.HasPrefix(trimmed, "go func") {

			if strings.Contains(trimmed, "(") {
				count++
			}
		}
	}

	for _, pat := range []string{
		"sync.Mutex", "sync.RWMutex", "sync.WaitGroup",
		"sync.Map", "sync.Once", "sync.Cond", "sync.Pool",
	} {
		count += strings.Count(src, pat)
	}

	count += strings.Count(src, "atomic.")
	return count
}
