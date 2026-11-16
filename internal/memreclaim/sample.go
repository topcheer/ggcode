package memreclaim

// sample.go: in-process memory sampling with variable-level detail (#5682).
//
// GGCODE_MEM_SAMPLE=1 enables it (default OFF). Each reclaim tick then
// appends ONE JSONL record to ~/.ggcode/memstats-<pid>.jsonl (~2-8KB):
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
// CAVEAT - attribution scale (#5701/#5720): per-site bytes come from the
// pprof debug=1 text form and are RAW UNSCALED SAMPLED BYTES (512KB
// sampling rate). sitesTotal therefore under-reports true heap usage
// (observed ~10x gap vs heapInuseBytes, concentrated in small-allocation
// churn). Compare sitesTotal and heapInuseBytes before concluding "growth
// outside attribution".
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

type siteStat struct {
	// Site is the aggregation key: the innermost frame, offset-stripped,
	// plus its immediate caller when known ("pkg.Func <- pkg.Caller").
	// #5710 widened attribution: the old key was the raw innermost symbol
	// WITH its +0x PC offset, which split one function across several rows
	// (addPostings+0x3cf vs +0x3a3) and carried no caller context.
	//
	// Bytes/Objects are raw unscaled sampled values (512KB pprof sampling
	// rate) - see the CAVEAT block in the file header (#5720).
	Site    string `json:"site"`
	Fn      string `json:"fn"`               // innermost frame, offsets stripped
	Caller  string `json:"caller,omitempty"` // second frame, offsets stripped
	Bytes   uint64 `json:"inuseBytes"`       // in-use bytes allocated from this site
	Objects uint64 `json:"liveObjects"`
}

// tailStat aggregates every heap site past the top-N cut (#5720): the
// long tail was previously dropped with zero accounting. Bytes/Objects
// carry the same unscaled-sample caveat as siteStat.
type tailStat struct {
	Sites       uint64 `json:"sites"`       // number of aggregated sites past the cut
	InuseBytes  uint64 `json:"inuseBytes"`  // summed sampled in-use bytes
	LiveObjects uint64 `json:"liveObjects"` // summed live object count
}

type statsRecord struct {
	TS           string     `json:"ts"`
	PID          int        `json:"pid"`
	HeapAllocB   uint64     `json:"heapAllocBytes"`
	HeapInuseB   uint64     `json:"heapInuseBytes"`
	HeapIdleB    uint64     `json:"heapIdleBytes"`
	HeapReleased uint64     `json:"heapReleasedBytes"`
	StackSysB    uint64     `json:"stackSysBytes"`
	SysB         uint64     `json:"sysBytes"`
	NumGC        uint32     `json:"numGC"`
	TopSites     []siteStat `json:"topSites,omitempty"`
	TailSites    *tailStat  `json:"tailSites,omitempty"`  // #5720: sites past the top-N cut, aggregated
	SitesTotal   uint64     `json:"sitesTotal,omitempty"` // #5720: sampled in-use bytes over ALL sites (top+tail)
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
	top, tail, total := heapTopSitesFull(topSitesLimit())
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
		TopSites:     top,
		SitesTotal:   total,
	}
	if tail.Sites > 0 {
		rec.TailSites = &tail
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
	var buf bytes.Buffer
	if err := pprof.Lookup("heap").WriteTo(&buf, 1); err != nil {
		return nil, tailStat{}, 0
	}
	return parseHeapTopSitesFull(&buf, n)
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
	agg := map[string]*siteStat{}
	flush := func(fn, caller string, objs, bts uint64) {
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
	}

	var curFn, curCaller string
	var curObjs, curBytes uint64
	haveBlock := false
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "#	"):
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
			// new block header: "objs: bytes [cumobjs: cumbytes] @ ..."
			flush(curFn, curCaller, curObjs, curBytes)
			head := line
			if i := strings.IndexByte(head, '['); i > 0 {
				head = head[:i]
			}
			parts := strings.Split(strings.TrimSpace(head), ": ")
			if len(parts) != 2 {
				curFn, curCaller, haveBlock = "", "", false
				continue
			}
			objs, err1 := strconv.ParseUint(parts[0], 10, 64)
			bts, err2 := strconv.ParseUint(parts[1], 10, 64)
			if err1 != nil || err2 != nil {
				curFn, curCaller, haveBlock = "", "", false
				continue
			}
			curFn, curCaller, curObjs, curBytes, haveBlock = "", "", objs, bts, true
		}
	}
	flush(curFn, curCaller, curObjs, curBytes)

	out := make([]siteStat, 0, len(agg))
	for _, e := range agg {
		if e.Bytes == 0 {
			continue // zero-byte samples carry no signal
		}
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bytes > out[j].Bytes })
	var total uint64
	for _, e := range out {
		total += e.Bytes
	}
	var tail tailStat
	if len(out) > n {
		for _, e := range out[n:] {
			tail.Sites++
			tail.InuseBytes += e.Bytes
			tail.LiveObjects += e.Objects
		}
		out = out[:n]
	}
	return out, tail, total
}
