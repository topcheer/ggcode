package memreclaim

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// #5720 pins (spun off #5701): the top-N cut used to drop the long tail
// with zero accounting - growth beyond the last visible row was
// indistinguishable from "no growth". Records must now carry a tailSites
// aggregation bucket plus sitesTotal, while parseHeapTopSites truncation
// semantics stay identical (the #5710 pins depend on them).

const issue5701Profile = `heap profile: 21: 2100 [21: 2100] @ heap/524288
# runtime.MemStats
# Alloc = 2100
6: 600 [6: 600] @ 0x1 0x2
#	0x1  pkg.a+0x1  /p/a.go:1
#	0x2  pkg.caller1+0x1  /p/c.go:1
5: 500 [5: 500] @ 0x3 0x4
#	0x3  pkg.b+0x1  /p/b.go:1
#	0x4  pkg.caller2+0x1  /p/c.go:2
4: 400 [4: 400] @ 0x5 0x6
#	0x5  pkg.c+0x1  /p/c.go:3
#	0x6  pkg.caller3+0x1  /p/c.go:4
3: 300 [3: 300] @ 0x7 0x8
#	0x7  pkg.d+0x1  /p/d.go:1
#	0x8  pkg.caller4+0x1  /p/c.go:5
2: 200 [2: 200] @ 0x9 0xa
#	0x9  pkg.e+0x1  /p/e.go:1
#	0xa  pkg.caller5+0x1  /p/c.go:6
1: 100 [1: 100] @ 0xb 0xc
#	0xb  pkg.f+0x1  /p/f.go:1
#	0xc  pkg.caller6+0x1  /p/c.go:7
`

// Tail bucket math: with n=3 the tail must sum exactly the three sites
// past the cut, and sitesTotal must cover all six.
func TestIssue5701TailBucketSumsLongTail(t *testing.T) {
	top, tail, total := parseHeapTopSitesFull(strings.NewReader(issue5701Profile), 3)
	if len(top) != 3 {
		t.Fatalf("#5720: top must hold exactly n rows, got %d", len(top))
	}
	if top[0].Bytes != 600 || top[2].Bytes != 400 {
		t.Fatalf("#5720: wrong ranking: %+v", top)
	}
	if tail.Sites != 3 {
		t.Fatalf("#5720: tail must aggregate 3 sites past the cut, got %d", tail.Sites)
	}
	if tail.InuseBytes != 300+200+100 {
		t.Fatalf("#5720: tail bytes want 600, got %d", tail.InuseBytes)
	}
	if tail.LiveObjects != 3+2+1 {
		t.Fatalf("#5720: tail objects want 6, got %d", tail.LiveObjects)
	}
	if total != 2100 {
		t.Fatalf("#5720: sitesTotal want 2100 (all six sites), got %d", total)
	}
}

// sitesTotal must equal Σ(top) + tail even under uneven cuts.
func TestIssue5701SitesTotalEqualsTopPlusTail(t *testing.T) {
	for _, n := range []int{1, 3, 6, 10} {
		top, tail, total := parseHeapTopSitesFull(strings.NewReader(issue5701Profile), n)
		var sum uint64
		for _, s := range top {
			sum += s.Bytes
		}
		sum += tail.InuseBytes
		if total != sum || total != 2100 {
			t.Fatalf("#5720: n=%d sitesTotal(%d) != Σtop+tail(%d)", n, total, sum)
		}
		if n >= 6 && tail.Sites != 0 {
			t.Fatalf("#5720: n>=site-count must leave an empty tail, got %d sites", tail.Sites)
		}
	}
}

// parseHeapTopSites keeps its documented truncation semantics (#5710 pins
// depend on them): same ranking, same cut, no tail leakage into the slice.
func TestIssue5701ParseHeapTopSitesSemanticsUnchanged(t *testing.T) {
	sites := parseHeapTopSites(strings.NewReader(issue5701Profile), 3)
	if len(sites) != 3 || sites[0].Bytes != 600 {
		t.Fatalf("#5720: parseHeapTopSites semantics drifted: %+v", sites)
	}
}

// Header doc drift pin: the documented default must match the code default
// (the doc claimed 20 for a 10 default — #5701 claim-seat finding).
func TestIssue5701DefaultTopSitesDocPin(t *testing.T) {
	if defaultTopSites != 10 {
		t.Fatalf("#5720: defaultTopSites drifted from 10: %d", defaultTopSites)
	}
}

// Record-level e2e: writeStatsLine must emit JSONL carrying sitesTotal and
// (when a tail exists) tailSites, with sitesTotal == ΣtopSites + tail bytes.
func TestIssue5701RecordCarriesTailAccounting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GGCODE_MEM_SAMPLE_JSONL_DIR", dir)
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	writeStatsLine(&ms, true)

	path := filepath.Join(dir, "memstats-*.jsonl")
	matches, _ := filepath.Glob(path)
	if len(matches) != 1 {
		t.Fatalf("#5720: expected one JSONL file, got %v", matches)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	var rec statsRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("#5720: record not valid JSON: %v", err)
	}
	var sum uint64
	for _, s := range rec.TopSites {
		sum += s.Bytes
	}
	if rec.TailSites != nil {
		sum += rec.TailSites.InuseBytes
	}
	if rec.SitesTotal == 0 {
		t.Fatalf("#5720: sitesTotal missing from record")
	}
	if sum != 0 && sum != rec.SitesTotal {
		t.Fatalf("#5720: record sitesTotal(%d) != Σtop+tail(%d)", rec.SitesTotal, sum)
	}
}
