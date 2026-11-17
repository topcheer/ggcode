package memreclaim

// sample.go: in-process memory sampling with variable-level detail (#5682).
//
// GGCODE_MEM_SAMPLE=1 enables it (default OFF). Each reclaim tick then
// appends ONE JSONL record to ~/.ggcode/memstats-<pid>.jsonl (~2-14KB):
//
//   - MemStats summary (heap alloc/in-use/idle/released, stack, sys, GC)
//   - topSites: the top-N heap consumers aggregated by allocation site
//     (in-use bytes + live object count per site, from the runtime heap
//     profile) - variable-level granularity without dumping the whole
//     profile.
//   - tailSites + sitesTotal (#5720): the top-N cut used to drop the long
//     tail with zero accounting - growth beyond the last visible row was
//     indistinguishable from "no growth". Records now carry a tail
//     aggregation bucket (site count / in-use bytes / live objects summed
//     over everything past the cut) plus sitesTotal, the sampled in-use
//     bytes summed over ALL sites (top + tail).
//
// ATTRIBUTION SCALE (#5701/#5720/#5751): per-site bytes come from the
// pprof debug=1 text form and are RAW UNSCALED SAMPLED BYTES. The runtime
// samples roughly one allocation per MemProfileRate bytes of allocation
// flow (geometric next-sample spacing; the rate is recorded per record
// as sampleRate, from the profile header "@ heap/N" - 524288 default,
// 1048576 observed on go1.27). A sampled object is recorded at its true
// size, but each object is only picked with probability ~min(1,
// size/rate), so raw sums under-report by roughly that factor per
// object: 1KB objects contribute ~1/1000th of their flow, 2MB objects
// ~86% (measured, go1.27). The raw sum therefore under-reports any
// workload dominated by small allocations (observed ~10x gap vs
// heapInuseBytes; #5751 measured coverage 0.07-0.12 on a live
// #5720-era seat). That blind spot is what made ~900MB of heap look
// "unattributed". Since #5751 every record carries an attribution
// block that makes the gap explicit and interpretable:
//
//   - attribution.gapBytes      heapInuse - sitesTotal (unattributed
//                               remainder, in true MemStats bytes)
//   - attribution.coverage      sitesTotal / heapInuse (raw sampled share)
//   - attribution.scaleFactor   heapInuse / sitesTotal - multiply any raw
//                               row by this for a first-order true-size
//                               estimate (uniform scaling assumes a
//                               similar object-size mix per site; per-site
//                               bias still favors big-object sites)
//   - attribution.classification churn-vs-retained heuristic for the
//                               interval (see classifyInterval)
//
// CHURN COUNTERS (#5751): MemStats is not sampled. totalAllocBytes,
// mallocs and frees are exact cumulative counters, and every record
// past the first carries allocDeltaBytes / gcDelta /
// heapInuseDeltaBytes (growth only; shrinks saturate at 0) relative to
// the previously written record. A seat where allocDeltaBytes runs
// hundreds of MB per interval while heapInuse oscillates is churn-type
// (allocation waves the GC keeps up with); monotone heapInuse growth
// with high-coverage rows is retained-type.
//
// ALLOC SITES (#5751): allocSites ranks the top-N sites by cumulative
// alloc_space (the "[objs: bytes]" bracket pair every debug=1 block
// already carries - one profile write yields both views), each row also
// carrying allocDeltaBytes - the alloc delta vs the previous record -
// and the site's current in-use numbers. A churn site shows allocBytes
// >> inuseBytes on its row. Reading two records T minutes apart answers
// "which sites allocated the most during those T minutes" straight from
// the JSONL, no pprof port needed. GGCODE_MEM_SAMPLE_ALLOC=0 suppresses
// the allocSites ranking (record-size control only - no cost saving;
// the bracket pair is parsed regardless).
//
// N defaults to 10 (GGCODE_MEM_SAMPLE_TOP). Dir override:
// GGCODE_MEM_SAMPLE_JSONL_DIR. Full profiles remain available on demand
// via the built-in pprof port.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultTopSites = 10

// samplingEnabled: on when GGCODE_MEM_SAMPLE=1/true OR the marker file
// ~/.ggcode/mem-sample exists (same pattern as ~/.ggcode/pprof-port - one
// global switch for the whole fleet, no per-process env plumbing).
func samplingEnabled() bool {
	switch os.Getenv("GGCODE_MEM_SAMPLE") {
	case "1", "true":
		return true
	}
	if home, err := os.UserHomeDir(); err == nil {
		if _, err := os.Stat(filepath.Join(home, ".ggcode", "mem-sample")); err == nil {
			return true
		}
	}
	return false
}

func topSitesLimit() int {
	if v := os.Getenv("GGCODE_MEM_SAMPLE_TOP"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultTopSites
}

// allocSamplingEnabled: the allocSites ranking rides along in the same
// profile write; GGCODE_MEM_SAMPLE_ALLOC=0 only suppresses the rows
// (record-size control).
func allocSamplingEnabled() bool {
	return os.Getenv("GGCODE_MEM_SAMPLE_ALLOC") != "0"
}

type siteStat struct {
	// Site is the aggregation key: the innermost frame, offset-stripped,
	// plus its immediate caller when known ("pkg.Func <- pkg.Caller").
	// #5710 widened attribution: the old key was the raw innermost symbol
	// WITH its +0x PC offset, which split one function across several rows
	// (addPostings+0x3cf vs +0x3a3) and carried no caller context.
	//
	// Bytes/Objects are raw unscaled sampled values (profile sampling
	// rate, recorded as sampleRate) - see the ATTRIBUTION SCALE block in
	// the file header (#5720/#5751).
	Site    string `json:"site"`
	Fn      string `json:"fn"`               // innermost frame, offsets stripped
	Caller  string `json:"caller,omitempty"` // second frame, offsets stripped
	Bytes   uint64 `json:"inuseBytes"`       // in-use bytes allocated from this site
	Objects uint64 `json:"liveObjects"`

	// #5751 alloc-space view (allocSites rows; omitempty keeps in-use
	// rows byte-identical to pre-#5751 records).
	AllocBytes      uint64 `json:"allocBytes,omitempty"`      // cumulative alloc_space
	AllocObjects    uint64 `json:"allocObjects,omitempty"`    // cumulative allocation events
	AllocDeltaBytes uint64 `json:"allocDeltaBytes,omitempty"` // alloc_space delta vs previous record
}

// tailStat aggregates every heap site past the top-N cut (#5720): the
// long tail was previously dropped with zero accounting. Bytes/Objects
// carry the same unscaled-sample caveat as siteStat.
type tailStat struct {
	Sites       uint64 `json:"sites"`       // number of aggregated sites past the cut
	InuseBytes  uint64 `json:"inuseBytes"`  // summed sampled in-use bytes
	LiveObjects uint64 `json:"liveObjects"` // summed live object count
}

// allocTailStat is tailStat for the alloc-space view (#5751): separate
// type so the JSON keys say allocBytes instead of the misleading
// inuseBytes.
type allocTailStat struct {
	Sites        uint64 `json:"sites"`
	AllocBytes   uint64 `json:"allocBytes"`
	AllocObjects uint64 `json:"allocObjects"`
}

// attributionStat (#5751) makes the heapInuse-vs-sitesTotal gap an
// explicit, self-describing part of every record instead of tribal
// knowledge in this file's header comment.
type attributionStat struct {
	GapBytes       uint64  `json:"gapBytes"`       // heapInuse - sitesTotal (clamped at 0)
	Coverage       float64 `json:"coverage"`       // sitesTotal / heapInuse
	ScaleFactor    float64 `json:"scaleFactor"`    // heapInuse / sitesTotal; 0 when sitesTotal is 0
	Classification string  `json:"classification"` // see classifyInterval
}

type statsRecord struct {
	TS           string `json:"ts"`
	PID          int    `json:"pid"`
	HeapAllocB   uint64 `json:"heapAllocBytes"`
	HeapInuseB   uint64 `json:"heapInuseBytes"`
	HeapIdleB    uint64 `json:"heapIdleBytes"`
	HeapReleased uint64 `json:"heapReleasedBytes"`
	StackSysB    uint64 `json:"stackSysBytes"`
	SysB         uint64 `json:"sysBytes"`
	NumGC        uint32 `json:"numGC"`

	// #5751: exact cumulative counters (MemStats is not sampled - this is
	// ground truth, unlike every per-site number above).
	TotalAlloc uint64 `json:"totalAllocBytes"`
	Mallocs    uint64 `json:"mallocs"`
	Frees      uint64 `json:"frees"`

	// #5751: interval deltas vs the previously written record (absent on
	// the first record). heapInuseDeltaBytes reports growth only; a
	// shrinking heap saturates at 0.
	AllocDelta     uint64 `json:"allocDeltaBytes,omitempty"`
	GCDelta        uint32 `json:"gcDelta,omitempty"`
	HeapInuseDelta uint64 `json:"heapInuseDeltaBytes,omitempty"`

	TopSites   []siteStat `json:"topSites,omitempty"`
	TailSites  *tailStat  `json:"tailSites,omitempty"`  // #5720: sites past the top-N cut, aggregated
	SitesTotal uint64     `json:"sitesTotal,omitempty"` // #5720: sampled in-use bytes over ALL sites (top+tail)
	SampleRate uint64     `json:"sampleRate,omitempty"` // #5751: pprof heap sampling rate from the profile header

	// #5751 alloc-space view.
	AllocSites  []siteStat       `json:"allocSites,omitempty"`
	AllocTail   *allocTailStat   `json:"allocTailSites,omitempty"`
	AllocTotal  uint64           `json:"allocSitesTotal,omitempty"`
	Attribution *attributionStat `json:"attribution,omitempty"`
}

// samplerState carries the previous record's counters so each new record
// can express interval deltas (#5751). Mutex-guarded: the reclaim loop is
// the only production writer, but tests interleave.
type samplerState struct {
	mu              sync.Mutex
	havePrev        bool
	prevTotalAlloc  uint64
	prevNumGC       uint32
	prevHeapInuse   uint64
	prevAllocBySite map[string]uint64
}

var sampler samplerState

// reset clears interval-delta state; the next written record behaves like
// a first sample (no deltas). In-package tests use it for hermetic runs.
func (s *samplerState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.havePrev = false
	s.prevTotalAlloc = 0
	s.prevNumGC = 0
	s.prevHeapInuse = 0
	s.prevAllocBySite = nil
}

// classifyInterval labels one sampling interval from its exact deltas
// (#5751):
//
//	retained-growth: the heap grew by more than 25% of the bytes
//	                 allocated during the interval (accumulation)
//	churn-dominant:  less than 10% of allocated bytes survived the
//	                 interval (allocation waves the GC keeps up with)
//	steady:          in between (normal working-set wiggle)
//	no-alloc / first-sample: degenerate cases
//
// Coarse by construction (GC pacing and sampling skew the deltas); it
// routes triage, it does not replace reading the rows.
func classifyInterval(first bool, allocDelta, heapInuseGrowth uint64) string {
	switch {
	case first:
		return "first-sample"
	case allocDelta == 0:
		return "no-alloc"
	case heapInuseGrowth*4 > allocDelta:
		return "retained-growth"
	case heapInuseGrowth*10 < allocDelta:
		return "churn-dominant"
	default:
		return "steady"
	}
}

// writeStatsLine samples MemStats + heap top-sites and appends one JSONL
// record. No-op unless GGCODE_MEM_SAMPLE is enabled or force is set (tests).
func writeStatsLine(ms *runtime.MemStats, force bool) {
	if !force && !samplingEnabled() {
		return
	}
	dir := os.Getenv("GGCODE_MEM_SAMPLE_JSONL_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		dir = filepath.Join(home, ".ggcode")
	}
	agg := heapProfileAgg(topSitesLimit())
	rec := statsRecord{
		TS:           time.Now().UTC().Format(time.RFC3339),
		PID:          os.Getpid(),
		HeapAllocB:   ms.HeapAlloc,
		HeapInuseB:   ms.HeapInuse,
		HeapIdleB:    ms.HeapIdle,
		HeapReleased: ms.HeapReleased,
		StackSysB:    ms.StackSys,
		SysB:         ms.Sys,
		NumGC:        ms.NumGC,
		TotalAlloc:   ms.TotalAlloc,
		Mallocs:      ms.Mallocs,
		Frees:        ms.Frees,
		TopSites:     agg.Top,
		SitesTotal:   agg.Total,
		SampleRate:   agg.Rate,
	}
	if agg.Tail.Sites > 0 {
		rec.TailSites = &agg.Tail
	}
	// #5751 alloc view: ranked from the same walk's bracket pairs; a
	// churn site shows allocBytes >> inuseBytes on its row. Rows carry
	// both views, so no join is needed.
	if allocSamplingEnabled() && (len(agg.AllocTop) > 0 || agg.AllocTotal > 0) {
		rec.AllocSites = agg.AllocTop
		rec.AllocTotal = agg.AllocTotal
		if agg.AllocTail.Sites > 0 {
			rec.AllocTail = &agg.AllocTail
		}
	}

	sampler.mu.Lock()
	first := !sampler.havePrev
	if !first {
		if ms.TotalAlloc > sampler.prevTotalAlloc {
			rec.AllocDelta = ms.TotalAlloc - sampler.prevTotalAlloc
		}
		if ms.NumGC > sampler.prevNumGC {
			rec.GCDelta = ms.NumGC - sampler.prevNumGC
		}
		if ms.HeapInuse > sampler.prevHeapInuse {
			rec.HeapInuseDelta = ms.HeapInuse - sampler.prevHeapInuse
		}
		for i := range rec.AllocSites {
			s := &rec.AllocSites[i]
			if prev, ok := sampler.prevAllocBySite[s.Site]; ok && s.AllocBytes > prev {
				s.AllocDeltaBytes = s.AllocBytes - prev
			}
		}
	}
	sampler.havePrev = true
	sampler.prevTotalAlloc = ms.TotalAlloc
	sampler.prevNumGC = ms.NumGC
	sampler.prevHeapInuse = ms.HeapInuse
	if len(rec.AllocSites) > 0 {
		cur := make(map[string]uint64, len(rec.AllocSites))
		for _, s := range rec.AllocSites {
			cur[s.Site] = s.AllocBytes
		}
		sampler.prevAllocBySite = cur
	} else if sampler.prevAllocBySite != nil {
		sampler.prevAllocBySite = nil // alloc view disabled mid-flight: stop stale deltas
	}
	sampler.mu.Unlock()

	// #5751 attribution block: gap/coverage/scaleFactor/classification.
	if rec.HeapInuseB > 0 {
		attr := &attributionStat{
			Classification: classifyInterval(first, rec.AllocDelta, rec.HeapInuseDelta),
		}
		if rec.SitesTotal >= rec.HeapInuseB {
			attr.GapBytes = 0 // sampled sum somehow exceeds true in-use; nothing unattributed
		} else {
			attr.GapBytes = rec.HeapInuseB - rec.SitesTotal
		}
		if rec.SitesTotal > 0 {
			attr.Coverage = float64(rec.SitesTotal) / float64(rec.HeapInuseB)
			attr.ScaleFactor = float64(rec.HeapInuseB) / float64(rec.SitesTotal)
		}
		rec.Attribution = attr
	}

	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	path := filepath.Join(dir, fmt.Sprintf("memstats-%d.jsonl", os.Getpid()))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(b, '\n'))
}

// heapTopSites aggregates the runtime heap profile (pprof debug=1 text
// form) by allocation site and returns the top-N by in-use bytes.
//
// Block form:
//
//	12: 4096 [12: 4096] @ 0x1 0x2 0x3
//	#	0x1  pkg.Func+0x10  /path/file.go:42
//	#	0x2  pkg.Caller+0x20  /path/other.go:7
//
// #5710 widening: the innermost symbolized frame (first # line) names the
// site and the SECOND frame names its immediate caller. The pair is the
// aggregation key, both with +0x PC offsets stripped, so one function
// sampled at several PCs merges into a single row (the old key split
// addPostings+0x3cf from +0x3a3) and workload paths stay distinguishable
// (who called the allocator).
func heapTopSites(n int) []siteStat {
	top, _, _ := heapTopSitesFull(n)
	return top
}

// heapTopSitesFull additionally returns the #5720 tail bucket (sites past
// the top-N cut, aggregated) and sitesTotal (sampled in-use bytes over all
// aggregated sites), so records can account for the long tail.
func heapTopSitesFull(n int) ([]siteStat, tailStat, uint64) {
	agg := heapProfileAgg(n)
	return agg.Top, agg.Tail, agg.Total
}

// heapProfileAgg renders the runtime heap profile (debug=1 text form) once
// and aggregates BOTH views it carries (#5751): the primary block values
// are in-use sampled bytes/objects (topSites since #5682), the bracket
// pair "[objs: bytes]" is the cumulative alloc_space view (allocSites).
// Go's legacy text writer always emits this shape for "heap" - a second
// Lookup("allocs") write would return the same text.
func heapProfileAgg(n int) profileAgg {
	var buf bytes.Buffer
	p := pprof.Lookup("heap")
	if p == nil {
		return profileAgg{}
	}
	if err := p.WriteTo(&buf, 1); err != nil {
		return profileAgg{}
	}
	return parseHeapProfile(&buf, n)
}

// stripSymOff removes the trailing "+0x..." PC offset from a symbolized
// profile frame ("pkg.Func+0x3cf" -> "pkg.Func").
func stripSymOff(sym string) string {
	if i := strings.LastIndex(sym, "+0x"); i > 0 {
		return sym[:i]
	}
	return sym
}

// siteKey builds the aggregation key from the innermost frame and its
// caller ("" when the block carried only one symbolized frame).
func siteKey(fn, caller string) string {
	if caller == "" {
		return fn
	}
	return fn + " <- " + caller
}

// parseHeapTopSites walks the pprof debug=1 heap-profile text form and
// aggregates blocks by (innermost frame, immediate caller), both
// offset-stripped. Blocks with no symbolized frame are skipped. Split
// from heapTopSites so the walk is testable on synthetic profiles.
func parseHeapTopSites(r io.Reader, n int) []siteStat {
	top, _, _ := parseHeapTopSitesFull(r, n)
	return top
}

// parseHeapTopSitesFull is parseHeapTopSites plus the #5720 tail bucket:
// the sites past the top-N cut, aggregated into one tailStat, and
// sitesTotal - the sampled in-use bytes summed over ALL aggregated sites
// (top + tail). Truncation semantics for the top slice are identical to
// parseHeapTopSites (the #5710 pins depend on them).
func parseHeapTopSitesFull(r io.Reader, n int) ([]siteStat, tailStat, uint64) {
	agg := parseHeapProfile(r, n)
	return agg.Top, agg.Tail, agg.Total
}

// profileAgg is the fully aggregated heap profile: the in-use view
// (legacy topSites ranking + tail + total) and the alloc view derived
// from the same walk's bracket pairs (#5751).
type profileAgg struct {
	Top   []siteStat
	Tail  tailStat
	Total uint64
	Rate  uint64

	AllocTop   []siteStat
	AllocTail  allocTailStat
	AllocTotal uint64
}

// parseHeapProfile walks the pprof debug=1 heap-profile text form once and
// aggregates BOTH views (#5751):
//
//	12: 4096 [30: 90000] @ 0x1 0x2 0x3
//	#	0x1  pkg.Func+0x10  /path/file.go:42
//
// The primary pair (12: 4096) is in-use objects/bytes; the bracket pair
// ([30: 90000]) is cumulative allocated objects/bytes - Go's legacy
// writer emits both for every block. The header line
// ("heap profile: N: B [N: B] @ heap/RATE") yields the sampling rate.
// The in-use ranking, tail bucket and total keep the #5710/#5720
// semantics byte-for-byte; sites with zero in-use bytes but non-zero
// cumulative allocation (pure churn sites) appear only in the alloc
// ranking.
func parseHeapProfile(r io.Reader, n int) profileAgg {
	agg := map[string]*siteStat{}
	var rate uint64
	flush := func(fn, caller string, objs, bts, allocObjs, allocBts uint64) {
		if fn == "" {
			return // unsymbolized block
		}
		key := siteKey(fn, caller)
		e := agg[key]
		if e == nil {
			e = &siteStat{Site: key, Fn: fn, Caller: caller}
			agg[key] = e
		}
		e.Bytes += bts
		e.Objects += objs
		e.AllocBytes += allocBts
		e.AllocObjects += allocObjs
	}

	var curFn, curCaller string
	var curObjs, curBytes, curAllocObjs, curAllocBytes uint64
	haveBlock := false
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "heap profile:"):
			// header: "heap profile: N: B [N: B] @ heap/RATE" (#5751)
			if i := strings.Index(line, "@ heap/"); i >= 0 {
				rest := strings.TrimSpace(line[i+len("@ heap/"):])
				if j := strings.IndexAny(rest, " \t\r"); j >= 0 {
					rest = rest[:j]
				}
				if v, err := strconv.ParseUint(rest, 10, 64); err == nil {
					rate = v
				}
			}
		case strings.HasPrefix(line, "#\t"):
			if haveBlock {
				// "0xaddr  sym+off  file:line": the first symbolized
				// frame names the site, the second its caller.
				if fields := strings.Fields(line[2:]); len(fields) >= 2 {
					if sym := stripSymOff(fields[1]); curFn == "" {
						curFn = sym
					} else if curCaller == "" {
						curCaller = sym
					}
				}
			}
		case strings.HasPrefix(line, "#"), line == "":
			continue // MemStats dump or blank
		default:
			// new block header: "objs: bytes [allocobjs: allocbytes] @ ..."
			flush(curFn, curCaller, curObjs, curBytes, curAllocObjs, curAllocBytes)
			curFn, curCaller, curObjs, curBytes = "", "", 0, 0
			curAllocObjs, curAllocBytes, haveBlock = 0, 0, false
			head := line
			bracket := ""
			if i := strings.IndexByte(head, '['); i > 0 {
				if j := strings.IndexByte(head[i+1:], ']'); j >= 0 {
					bracket = head[i+1 : i+1+j]
				}
				head = head[:i]
			}
			parts := strings.Split(strings.TrimSpace(head), ": ")
			if len(parts) != 2 {
				continue
			}
			objs, err1 := strconv.ParseUint(parts[0], 10, 64)
			bts, err2 := strconv.ParseUint(parts[1], 10, 64)
			if err1 != nil || err2 != nil {
				continue
			}
			curObjs, curBytes, haveBlock = objs, bts, true
			// bracket pair: cumulative alloc view (#5751); parse failures
			// leave alloc at zero - the in-use view still aggregates.
			if ap := strings.Split(strings.TrimSpace(bracket), ": "); len(ap) == 2 {
				if ao, err1 := strconv.ParseUint(ap[0], 10, 64); err1 == nil {
					if ab, err2 := strconv.ParseUint(ap[1], 10, 64); err2 == nil {
						curAllocObjs, curAllocBytes = ao, ab
					}
				}
			}
		}
	}
	flush(curFn, curCaller, curObjs, curBytes, curAllocObjs, curAllocBytes)

	// In-use ranking: legacy #5710/#5720 semantics - only sites with
	// non-zero sampled in-use bytes carry signal.
	inuseRows := make([]siteStat, 0, len(agg))
	allocRows := make([]siteStat, 0, len(agg))
	for _, e := range agg {
		if e.Bytes > 0 {
			inuseRows = append(inuseRows, *e)
		}
		if e.AllocBytes > 0 {
			allocRows = append(allocRows, *e)
		}
	}
	sort.Slice(inuseRows, func(i, j int) bool { return inuseRows[i].Bytes > inuseRows[j].Bytes })
	sort.Slice(allocRows, func(i, j int) bool { return allocRows[i].AllocBytes > allocRows[j].AllocBytes })

	var total uint64
	for _, e := range inuseRows {
		total += e.Bytes
	}
	var tail tailStat
	if len(inuseRows) > n {
		for _, e := range inuseRows[n:] {
			tail.Sites++
			tail.InuseBytes += e.Bytes
			tail.LiveObjects += e.Objects
		}
		inuseRows = inuseRows[:n]
	}

	var allocTotal uint64
	for _, e := range allocRows {
		allocTotal += e.AllocBytes
	}
	var allocTail allocTailStat
	if len(allocRows) > n {
		for _, e := range allocRows[n:] {
			allocTail.Sites++
			allocTail.AllocBytes += e.AllocBytes
			allocTail.AllocObjects += e.AllocObjects
		}
		allocRows = allocRows[:n]
	}
	return profileAgg{
		Top: inuseRows, Tail: tail, Total: total, Rate: rate,
		AllocTop: allocRows, AllocTail: allocTail, AllocTotal: allocTotal,
	}
}
