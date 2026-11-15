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
//
// N defaults to 20 (GGCODE_MEM_SAMPLE_TOP). Dir override:
// GGCODE_MEM_SAMPLE_JSONL_DIR. Full profiles remain available on demand
// via the built-in pprof port.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
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
	Site    string `json:"site"`       // innermost symbolized frame "pkg.Func"
	Bytes   uint64 `json:"inuseBytes"` // in-use bytes allocated from this site
	Objects uint64 `json:"liveObjects"`
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
		TopSites:     heapTopSites(topSitesLimit()),
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
// The innermost symbolized frame (first # line) identifies the site.
func heapTopSites(n int) []siteStat {
	var buf bytes.Buffer
	if err := pprof.Lookup("heap").WriteTo(&buf, 1); err != nil {
		return nil
	}
	agg := map[string]*siteStat{}
	flush := func(site string, objs, bts uint64) {
		if site == "" {
			return // unsymbolized block
		}
		e := agg[site]
		if e == nil {
			e = &siteStat{Site: site}
			agg[site] = e
		}
		e.Bytes += bts
		e.Objects += objs
	}

	var curSite string
	var curObjs, curBytes uint64
	haveBlock := false
	sc := bufio.NewScanner(&buf)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "#\t"):
			if haveBlock && curSite == "" {
				// first symbolized frame: "0xaddr  sym+off  file:line"
				if fields := strings.Fields(line[2:]); len(fields) >= 2 {
					curSite = fields[1]
				}
			}
		case strings.HasPrefix(line, "#"), line == "":
			continue // MemStats dump or blank
		default:
			// new block header: "objs: bytes [cumobjs: cumbytes] @ ..."
			flush(curSite, curObjs, curBytes)
			head := line
			if i := strings.IndexByte(head, '['); i > 0 {
				head = head[:i]
			}
			parts := strings.Split(strings.TrimSpace(head), ": ")
			if len(parts) != 2 {
				curSite, haveBlock = "", false
				continue
			}
			objs, err1 := strconv.ParseUint(parts[0], 10, 64)
			bts, err2 := strconv.ParseUint(parts[1], 10, 64)
			if err1 != nil || err2 != nil {
				curSite, haveBlock = "", false
				continue
			}
			curSite, curObjs, curBytes, haveBlock = "", objs, bts, true
		}
	}
	flush(curSite, curObjs, curBytes)

	out := make([]siteStat, 0, len(agg))
	for _, e := range agg {
		if e.Bytes == 0 {
			continue // zero-byte samples carry no signal
		}
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bytes > out[j].Bytes })
	if len(out) > n {
		out = out[:n]
	}
	return out
}
