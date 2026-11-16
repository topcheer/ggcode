package memreclaim

import (
	"strings"
	"testing"
)

// #5710 sampler widening pins: topSites attribution must aggregate by
// (innermost fn, immediate caller) with +0x PC offsets stripped, so one
// function sampled at several PCs merges into one row and the caller
// context distinguishes workload paths. The pre-widening key was the raw
// "pkg.Func+0x3cf" symbol - the same addPostings showed up as two split
// rows and there was no way to tell who allocated.

const issue5710Profile = `heap profile: 6: 900 [10: 1600] @ heap/524288
# runtime.MemStats
# Alloc = 900
# HeapInuse = 900
2: 500 [2: 500] @ 0x1 0x2
#	0x1  github.com/topcheer/ggcode/internal/sessionidx.addPostings+0x3cf  /p/sessionidx.go:1
#	0x2  github.com/topcheer/ggcode/internal/sessionidx.(*Index).indexLine+0x11  /p/sessionidx.go:2
3: 300 [3: 300] @ 0x3 0x4
#	0x3  github.com/topcheer/ggcode/internal/sessionidx.addPostings+0x3a3  /p/sessionidx.go:3
#	0x4  github.com/topcheer/ggcode/internal/sessionidx.(*Index).indexLine+0x12  /p/sessionidx.go:4
1: 100 [1: 100] @ 0x5 0x6
#	0x5  github.com/topcheer/ggcode/internal/sessionidx.addPostings+0x3cf  /p/sessionidx.go:1
#	0x6  github.com/topcheer/ggcode/internal/sessionidx.(*Manager).TailMissingSources+0x55  /p/manager.go:5
1: 50 [1: 50] @ 0x7
#	0x7  runtime.mallocgc+0x0  /p/runtime.go:6
`

// Same fn + same caller at different PC offsets must merge; a different
// caller must stay a separate row; offsets must never leak into any key.
func TestIssue5710SamplerMergesOffsetsSplitsByCaller(t *testing.T) {
	sites := parseHeapTopSites(strings.NewReader(issue5710Profile), 10)
	if len(sites) != 3 {
		t.Fatalf("#5710: want 3 aggregated rows (addPostings<-indexLine merged, addPostings<-tail, mallocgc), got %d: %+v", len(sites), sites)
	}
	byKey := map[string]siteStat{}
	for _, s := range sites {
		byKey[s.Site] = s
	}
	merged := byKey["github.com/topcheer/ggcode/internal/sessionidx.addPostings <- github.com/topcheer/ggcode/internal/sessionidx.(*Index).indexLine"]
	if merged.Bytes != 800 {
		t.Fatalf("#5710: same-fn same-caller offsets did not merge: want 800 bytes, got %d", merged.Bytes)
	}
	if merged.Objects != 5 {
		t.Fatalf("#5710: merged object count wrong: want 5, got %d", merged.Objects)
	}
	tail := byKey["github.com/topcheer/ggcode/internal/sessionidx.addPostings <- github.com/topcheer/ggcode/internal/sessionidx.(*Manager).TailMissingSources"]
	if tail.Bytes != 100 {
		t.Fatalf("#5710: caller split missing: want 100-byte tail row, got %+v", tail)
	}
	for _, s := range sites {
		if strings.Contains(s.Site, "+0x") || strings.Contains(s.Fn, "+0x") || strings.Contains(s.Caller, "+0x") {
			t.Fatalf("#5710: PC offset leaked into attribution key: %+v", s)
		}
		if s.Fn == "" {
			t.Fatalf("#5710: fn field must always be populated: %+v", s)
		}
	}
}

// Descending order and top-N must survive the wider key space.
func TestIssue5710SamplerRanksAndTruncates(t *testing.T) {
	sites := parseHeapTopSites(strings.NewReader(issue5710Profile), 2)
	if len(sites) != 2 {
		t.Fatalf("#5710: top-N not honored: %d rows for n=2", len(sites))
	}
	if sites[0].Bytes != 800 || sites[1].Bytes != 100 {
		t.Fatalf("#5710: wrong ranking: %+v", sites)
	}
}

// Blocks with unsymbolized frames must be skipped, not zero-rows.
func TestIssue5710SamplerSkipsUnsymbolized(t *testing.T) {
	prof := `1: 4096 [1: 4096] @ 0x1
#	0x1
2: 8192 [2: 8192] @ 0x2 0x3
#	0x2
#	0x3
`
	sites := parseHeapTopSites(strings.NewReader(prof), 10)
	if len(sites) != 0 {
		t.Fatalf("#5710: unsymbolized blocks must yield no rows, got %+v", sites)
	}
}

// stripSymOff must only strip a genuine trailing +0x suffix (function
// names containing '+' elsewhere must survive).
func TestIssue5710StripSymOff(t *testing.T) {
	if got := stripSymOff("pkg.Func+0x3cf"); got != "pkg.Func" {
		t.Fatalf("strip failed: %q", got)
	}
	if got := stripSymOff("pkg.Func"); got != "pkg.Func" {
		t.Fatalf("bare symbol mangled: %q", got)
	}
	if got := stripSymOff("weird+name"); got != "weird+name" {
		t.Fatalf("non-offset plus mangled: %q", got)
	}
}

// The live-profile path must still produce symbolized, offset-free keys
// (end-to-end through pprof, not just the synthetic walker).
func TestIssue5710HeapTopSitesLiveOffsetsStripped(t *testing.T) {
	sites := heapTopSites(5)
	if len(sites) == 0 {
		t.Fatal("no sites from live heap profile")
	}
	for _, s := range sites {
		if strings.Contains(s.Site, "+0x") || s.Fn == "" {
			t.Fatalf("#5710: live site key malformed: %+v", s)
		}
	}
}
