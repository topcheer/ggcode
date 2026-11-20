package chat

import (
	"regexp"
	"testing"
)

// #5831/#5867 effect evidence: render-path regexes were compiled inline on
// every TUI frame. These benchmarks quantify the per-call cost difference
// between the old inline-compile pattern and the hoisted package vars.
// Run: go test -tags goolm ./internal/chat/ -bench RenderRegex -benchmem -run xxx

const benchSearchResult = "Showing 1 of 42 matches\ninternal/foo.go:12: hit line one\ninternal/bar.go:34: hit line two"

func BenchmarkRenderSearchCountPkgVar(b *testing.B) {
	t := &BaseToolItem{result: benchSearchResult}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = t.renderSearchCount()
	}
}

// BenchmarkRenderSearchCountInline replays the pre-8be998aa6 code shape
// (compile inside the function on every call) for A/B comparison.
func BenchmarkRenderSearchCountInline(b *testing.B) {
	t := &BaseToolItem{result: benchSearchResult}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		re := regexp.MustCompile(`Showing \d+ of (\d+) matches?|Found (\d+) matches?|(?:Showing|Found|Listed|Returned) \d+ of (\d+) results?`)
		_ = re.FindStringSubmatch(t.result)
	}
}

func BenchmarkFileLineCountRePkgVar(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = fileLineCountRe.FindAllString("     1\tline\n     2\tline\n", -1)
	}
}

func BenchmarkFileLineCountReInline(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		re := regexp.MustCompile(`(?m)^\s+\d+\s`)
		_ = re.FindAllString("     1\tline\n     2\tline\n", -1)
	}
}
