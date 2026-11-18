package memreclaim

// #5751: two seats >1GB heapInuse with ~900MB unattributed by topSites -
// churn-type climb plus a sampling blind spot. The gap decomposes into
// (a) raw sampled bytes under-reporting sub-rate allocations (observed
// coverage 0.07-0.12 on live seats) and (b) records carrying no churn
// signal at all. This suite pins the sampler upgrades: profile-rate
// capture, the attribution block (gap/coverage/scaleFactor/
// classification), exact churn counters with interval deltas, and the
// alloc_space view with per-site deltas.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIssue5751ParseProfileRate(t *testing.T) {
	prof := "heap profile: 2: 16384 [2: 16385] @ heap/524288\n" +
		"1: 2048 [1: 2048] @ 0x1 0x2\n" +
		"#\t0x1  pkg.Fn+0x10  /f.go:1\n" +
		"#\t0x2  pkg.Caller+0x20  /g.go:2\n"
	agg := parseHeapProfile(strings.NewReader(prof), 10)
	if agg.Rate != 524288 {
		t.Fatalf("rate not parsed from profile header: %d", agg.Rate)
	}
	if len(agg.Top) != 1 || agg.Top[0].Site != "pkg.Fn <- pkg.Caller" || agg.Total != 2048 {
		t.Fatalf("legacy aggregation regressed under parseHeapProfile: %+v total=%d", agg.Top, agg.Total)
	}
	// bracket pair of the same block feeds the alloc view (#5751)
	if len(agg.AllocTop) != 1 || agg.AllocTop[0].AllocBytes != 2048 || agg.AllocTotal != 2048 {
		t.Fatalf("bracket (alloc_space) pair not aggregated: %+v total=%d", agg.AllocTop, agg.AllocTotal)
	}

	// Headerless input (unsampled or hand-trimmed): rate 0, sites still
	// aggregate - the header must never gate the walk.
	prof2 := "1: 2048 [1: 2048] @ 0x1 0x2\n#\t0x1  pkg.Fn+0x10  /f.go:1\n"
	agg2 := parseHeapProfile(strings.NewReader(prof2), 10)
	if agg2.Rate != 0 || len(agg2.Top) != 1 || agg2.Total != 2048 {
		t.Fatalf("headerless profile mishandled: rate=%d top=%d total=%d", agg2.Rate, len(agg2.Top), agg2.Total)
	}
}

// Pure churn sites (zero live bytes, cumulative alloc) must surface in
// the alloc ranking and stay out of the in-use ranking - the pre-#5751
// sampler dropped them entirely, which is half the attribution blind
// spot.
func TestIssue5751ChurnSiteVisibleInAllocViewOnly(t *testing.T) {
	prof := "heap profile: 3: 8192 [900: 40000000] @ heap/524288\n" +
		// live site: 8KB in use, 8KB ever allocated
		"1: 8192 [1: 8192] @ 0x1 0x2\n" +
		"#\t0x1  pkg.Retained+0x10  /f.go:1\n" +
		"#\t0x2  pkg.Main+0x20  /g.go:2\n" +
		// churn site: nothing live, 40MB cumulative
		"0: 0 [5000: 40000000] @ 0x3 0x4\n" +
		"#\t0x3  pkg.Wave+0x10  /h.go:3\n" +
		"#\t0x4  pkg.Main+0x20  /g.go:4\n"
	agg := parseHeapProfile(strings.NewReader(prof), 10)
	if len(agg.Top) != 1 || agg.Top[0].Fn != "pkg.Retained" {
		t.Fatalf("in-use ranking must exclude zero-live churn sites: %+v", agg.Top)
	}
	if len(agg.AllocTop) != 2 {
		t.Fatalf("alloc ranking must include both sites: %+v", agg.AllocTop)
	}
	if agg.AllocTop[0].Fn != "pkg.Wave" || agg.AllocTop[0].AllocBytes != 40000000 {
		t.Fatalf("churn site must top the alloc ranking: %+v", agg.AllocTop)
	}
	// the churn row also carries its (zero) in-use numbers
	if agg.AllocTop[0].Bytes != 0 || agg.AllocTop[0].Objects != 0 {
		t.Fatalf("churn row in-use values must be zero: %+v", agg.AllocTop[0])
	}
	if agg.AllocTotal != 40008192 {
		t.Fatalf("allocSitesTotal = %d, want 40008192", agg.AllocTotal)
	}
}

func TestIssue5751ClassifyInterval(t *testing.T) {
	cases := []struct {
		first         bool
		alloc, growth uint64
		want          string
	}{
		{true, 0, 0, "first-sample"},
		{false, 0, 0, "no-alloc"},
		{false, 100, 26, "retained-growth"}, // 26*4 > 100: >25% survived
		{false, 100, 9, "churn-dominant"},   // 9*10 < 100: <10% survived
		{false, 100, 15, "steady"},          // between thresholds
		{false, 1 << 30, 0, "churn-dominant"},
		{false, 1 << 30, 60 << 20, "churn-dominant"}, // the #5751 seat shape: 900MB alloc, +60MB heap
		{false, 100, 100, "retained-growth"},
	}
	for _, c := range cases {
		if got := classifyInterval(c.first, c.alloc, c.growth); got != c.want {
			t.Fatalf("classifyInterval(first=%v, alloc=%d, growth=%d) = %q, want %q", c.first, c.alloc, c.growth, got, c.want)
		}
	}
}

func readIssue5751Records(t *testing.T, dir string) []statsRecord {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("memstats-%d.jsonl", os.Getpid())))
	if err != nil {
		t.Fatalf("no stats file: %v", err)
	}
	var recs []statsRecord
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var r statsRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("bad JSONL record %q: %v", line, err)
		}
		recs = append(recs, r)
	}
	return recs
}

// Record-level e2e: two ticks of writeStatsLine with a forced MemStats
// shape mirroring the #5751 seats (heapInuse >> raw sampled sites,
// ~900MB allocated in the interval, small heap growth). The second
// record must carry the churn deltas and a churn-dominant attribution
// block whose gap accounts for the difference between heapInuse and the
// raw sampled total.
func TestIssue5751RecordAttributionAndChurnDeltas(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GGCODE_MEM_SAMPLE_JSONL_DIR", dir)
	t.Setenv("GGCODE_MEM_SAMPLE_ALLOC", "0") // deltas under test are the MemStats ones
	sampler.reset()

	var ms runtime.MemStats
	ms.HeapInuse = 1113 << 20
	ms.TotalAlloc = 500 << 20
	ms.NumGC = 100
	writeStatsLine(&ms, true)

	ms.TotalAlloc += 900 << 20
	ms.NumGC += 40
	ms.HeapInuse += 60 << 20
	writeStatsLine(&ms, true)

	recs := readIssue5751Records(t, dir)
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d", len(recs))
	}
	r1, r2 := recs[0], recs[1]

	// First record: no deltas, first-sample classification.
	if r1.AllocDelta != 0 || r1.GCDelta != 0 || r1.HeapInuseDelta != 0 {
		t.Fatalf("first record must carry no interval deltas: %+v", r1)
	}
	if r1.Attribution == nil || r1.Attribution.Classification != "first-sample" {
		t.Fatalf("first record classification wrong: %+v", r1.Attribution)
	}

	// Exact churn counters survive the JSON round-trip.
	if r2.TotalAlloc != 1400<<20 {
		t.Fatalf("totalAllocBytes not recorded: %d", r2.TotalAlloc)
	}
	if r2.AllocDelta != 900<<20 {
		t.Fatalf("allocDeltaBytes = %d, want %d", r2.AllocDelta, 900<<20)
	}
	if r2.GCDelta != 40 {
		t.Fatalf("gcDelta = %d, want 40", r2.GCDelta)
	}
	if r2.HeapInuseDelta != 60<<20 {
		t.Fatalf("heapInuseDeltaBytes = %d, want %d", r2.HeapInuseDelta, 60<<20)
	}

	// Attribution: gap + sitesTotal == heapInuse (when sampled <= inuse),
	// coverage/scaleFactor consistent, churn-dominant classification.
	if r2.Attribution == nil {
		t.Fatal("no attribution block on record")
	}
	attr := r2.Attribution
	if r2.SitesTotal <= r2.HeapInuseB && attr.GapBytes != r2.HeapInuseB-r2.SitesTotal {
		t.Fatalf("gapBytes = %d, want heapInuse-sitesTotal = %d", attr.GapBytes, r2.HeapInuseB-r2.SitesTotal)
	}
	if r2.SitesTotal > 0 {
		if attr.Coverage <= 0 || attr.Coverage >= 1 {
			t.Fatalf("coverage out of (0,1): %f", attr.Coverage)
		}
		wantScale := float64(r2.HeapInuseB) / float64(r2.SitesTotal)
		if attr.ScaleFactor != wantScale {
			t.Fatalf("scaleFactor = %f, want %f", attr.ScaleFactor, wantScale)
		}
	} else if attr.Coverage != 0 || attr.ScaleFactor != 0 {
		t.Fatalf("empty profile must yield zero coverage/scaleFactor: %+v", attr)
	}
	if attr.Classification != "churn-dominant" {
		t.Fatalf("900MB alloc / +60MB heap must classify churn-dominant, got %q", attr.Classification)
	}
	// The seat shape: forced 1.1GB heapInuse vs a tiny real test-process
	// profile -> the gap IS the unattributed ~900MB the issue chased.
	if attr.GapBytes < 900<<20 {
		t.Fatalf("gapBytes = %d, want >=900MB under the forced seat shape", attr.GapBytes)
	}

	// Shrinking heap saturates growth at 0 (no underflow into huge deltas).
	ms.HeapInuse -= 500 << 20
	ms.TotalAlloc += 1 << 20
	writeStatsLine(&ms, true)
	r3 := readIssue5751Records(t, dir)[2]
	if r3.HeapInuseDelta != 0 {
		t.Fatalf("shrink must saturate heapInuseDeltaBytes at 0, got %d", r3.HeapInuseDelta)
	}
	if r3.AllocDelta != 1<<20 {
		t.Fatalf("allocDeltaBytes after shrink = %d, want 1MB", r3.AllocDelta)
	}
}

// Alloc-space view: allocSites must rank cumulative allocation sites and
// carry per-site allocDeltaBytes between two ticks - the JSONL
// equivalent of the issue's "alloc_space delta over 10min" ask.
func TestIssue5751AllocSitesPerSiteDelta(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GGCODE_MEM_SAMPLE_JSONL_DIR", dir)
	sampler.reset()

	payload := make([][]byte, 64)
	for i := range payload {
		payload[i] = make([]byte, 1<<20)
	}
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	writeStatsLine(&ms, true)

	more := make([][]byte, 64)
	for i := range more {
		more[i] = make([]byte, 1<<20)
	}
	runtime.GC()
	runtime.ReadMemStats(&ms)
	writeStatsLine(&ms, true)
	runtime.KeepAlive(payload)
	runtime.KeepAlive(more)

	recs := readIssue5751Records(t, dir)
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d", len(recs))
	}
	r2 := recs[1]
	if len(r2.AllocSites) == 0 {
		t.Fatal("no allocSites in record")
	}
	if r2.AllocTotal == 0 {
		t.Fatal("allocSitesTotal missing")
	}
	var deltaSum uint64
	for _, s := range r2.AllocSites {
		if s.Site == "" || s.AllocBytes == 0 {
			t.Fatalf("bad alloc row: %+v", s)
		}
		if s.AllocBytes < s.AllocDeltaBytes {
			t.Fatalf("delta exceeds cumulative alloc: %+v", s)
		}
		deltaSum += s.AllocDeltaBytes
	}
	if deltaSum == 0 {
		t.Fatal("no per-site alloc delta recorded across two ticks")
	}
	// The test's own 128MB of 1MB allocations must appear in the alloc
	// ranking (objects >= sampling rate are always recorded).
	var ours uint64
	for _, s := range r2.AllocSites {
		if strings.Contains(s.Fn, "TestIssue5751AllocSitesPerSiteDelta") {
			ours = s.AllocBytes
		}
	}
	if ours < 100<<20 {
		t.Fatalf("this test's 128MB allocation site missing/under-counted in allocSites: %d", ours)
	}
}

// The blind spot itself: same-shape sites differing only in object size.
// Sampling is geometric on the allocation flow (~one pick per rate
// bytes), so 2MB objects land at ~86% of true size on go1.27 while 1KB
// objects land at ~1/1000th. Pins the mechanism the attribution block
// exists to expose (thresholds loose - sampling is stochastic).
func TestIssue5751RawSamplingUnderReportsSmallObjects(t *testing.T) {
	if runtime.MemProfileRate > 0 && runtime.MemProfileRate < 4096 {
		t.Skipf("MemProfileRate=%d: near-exhaustive sampling, bias too small to pin", runtime.MemProfileRate)
	}
	// noinline helpers keep the two families as distinct profile sites
	// (inlining would merge both under the test function's frame).
	small := alloc5751Small(40 * 1024) // 40MB of 1KB objects
	big := alloc5751Big(20)            // 40MB of 2MB objects
	runtime.GC()
	runtime.KeepAlive(small)
	runtime.KeepAlive(big)

	var smallSum, bigSum uint64
	for _, s := range heapTopSites(30) {
		// symbolized frames are package-qualified
		// (memreclaim.alloc5751Big), so match by suffix.
		if strings.HasSuffix(s.Fn, ".alloc5751Small") {
			smallSum = s.Bytes
		}
		if strings.HasSuffix(s.Fn, ".alloc5751Big") {
			bigSum = s.Bytes
		}
	}
	if bigSum == 0 {
		t.Fatal("2MB-object site not found in in-use top sites")
	}
	if bigSum < 28<<20 {
		t.Fatalf("big-object site must be recorded near its ~86%% geometric-flow expectation: %d bytes for 40MB", bigSum)
	}
	if smallSum >= bigSum/4 {
		t.Fatalf("small-object site not under-reported (blind spot gone?): small=%d big=%d - if sampling semantics changed, revisit the #5751 attribution docs", smallSum, bigSum)
	}
}

//go:noinline
func alloc5751Small(n int) [][]byte {
	s := make([][]byte, n)
	for i := range s {
		s[i] = make([]byte, 1024)
	}
	return s
}

//go:noinline
func alloc5751Big(n int) [][]byte {
	b := make([][]byte, n)
	for i := range b {
		b[i] = make([]byte, 2<<20)
	}
	return b
}
