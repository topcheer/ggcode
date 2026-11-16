#!/usr/bin/env python3
"""memstats_top: canonical reader for ggcode memstats sampler rows (#5710).

Reads ~/.ggcode/memstats-<pid>.jsonl (written by internal/memreclaim when
GGCODE_MEM_SAMPLE is on) and prints, per PID, the last sample's heap
summary plus a global top-N of heap allocation sites aggregated at
FUNCTION granularity (+0x PC offsets stripped, callers rolled up).

Why this exists: the fleet-side attribution tooling reported "0 MB" for
topSites because ad-hoc readers guessed field names (top_sites vs
topSites, bytes vs inuseBytes). This reader accepts both spellings on
old and new rows, so attribution cannot silently zero out again.

Usage:
  python3 scripts/memstats_top.py                 # all PIDs, top 20 by fn
  python3 scripts/memstats_top.py --pid 47147     # one PID
  python3 scripts/memstats_top.py --by-caller     # keep caller split
  python3 scripts/memstats_top.py --dir /path --top 40 --all-rows
"""
import argparse
import glob
import json
import os
import re
import sys

_OFFSET_RE = re.compile(r"\+0x[0-9a-f]+$")


def strip_offset(sym):
    return _OFFSET_RE.sub("", sym)


def site_rows(rec):
    """Yield (fn, caller, bytes) for every topSites entry, tolerating
    old/new field spellings and legacy rows without fn/caller."""
    rows = rec.get("topSites") or rec.get("top_sites") or []
    for s in rows:
        if not isinstance(s, dict):
            continue
        b = s.get("inuseBytes", s.get("inuse_bytes", s.get("bytes", 0)))
        fn = s.get("fn") or strip_offset(str(s.get("site", "")))
        caller = s.get("caller", "")
        if not fn or not b:
            continue
        yield fn, caller, int(b)


def fmt_mb(n):
    return "%.1fMB" % (n / (1024.0 * 1024.0))


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--dir", default=os.path.join(os.path.expanduser("~"), ".ggcode"),
                    help="directory holding memstats-*.jsonl (default ~/.ggcode)")
    ap.add_argument("--top", type=int, default=20, help="top-N sites to print")
    ap.add_argument("--pid", type=int, default=None, help="restrict to one PID")
    ap.add_argument("--all-rows", action="store_true",
                    help="aggregate every sampled row (default: last row per PID)")
    ap.add_argument("--by-caller", action="store_true",
                    help="keep the fn <- caller split instead of rolling up callers")
    args = ap.parse_args()

    files = sorted(glob.glob(os.path.join(args.dir, "memstats-*.jsonl")))
    if args.pid is not None:
        files = [f for f in files if f.endswith("memstats-%d.jsonl" % args.pid)]
    if not files:
        print("no memstats-*.jsonl under %s" % args.dir, file=sys.stderr)
        return 1

    agg = {}
    per_pid = []
    for path in files:
        pid = int(os.path.basename(path)[len("memstats-"):-len(".jsonl")])
        first_ts = last_ts = None
        last = None
        rows_used = 0
        with open(path, "r", encoding="utf-8", errors="replace") as f:
            for line in f:
                line = line.strip()
                if not line:
                    continue
                try:
                    rec = json.loads(line)
                except json.JSONDecodeError:
                    continue
                ts = rec.get("ts")
                if ts:
                    if first_ts is None:
                        first_ts = ts
                    last_ts = ts
                last = rec
                if args.all_rows:
                    for fn, caller, b in site_rows(rec):
                        key = (fn, caller) if args.by_caller else fn
                        agg[key] = agg.get(key, 0) + b
                    rows_used += 1
        if last is None:
            continue
        if not args.all_rows:
            for fn, caller, b in site_rows(last):
                key = (fn, caller) if args.by_caller else fn
                agg[key] = agg.get(key, 0) + b
            rows_used = 1
        per_pid.append((pid, last, first_ts, last_ts, rows_used))

    print("== per-PID (last sample) ==")
    for pid, rec, first_ts, last_ts, n in per_pid:
        span = ""
        if first_ts and last_ts and first_ts != last_ts:
            span = "  span %s..%s" % (first_ts, last_ts)
        print("pid %-7d heapInuse %10s  heapAlloc %10s  released %10s  gc %5d%s" % (
            pid,
            fmt_mb(rec.get("heapInuseBytes", rec.get("heap_inuse_bytes", 0))),
            fmt_mb(rec.get("heapAllocBytes", rec.get("heap_alloc_bytes", 0))),
            fmt_mb(rec.get("heapReleasedBytes", rec.get("heap_released_bytes", 0))),
            rec.get("numGC", rec.get("num_gc", 0)),
            span))
        # #5720 tail accounting: tailSites aggregates everything past the
        # top-N cut; sitesTotal is the sampled sum over ALL sites. Both are
        # absent on pre-#5720 rows (tolerated, same policy as fn/caller).
        tail = rec.get("tailSites") or rec.get("tail_sites")
        if isinstance(tail, dict):
            tb = tail.get("inuseBytes", tail.get("inuse_bytes", 0))
            print("           tail: %s across %s sites past the cut" % (
                fmt_mb(tb), tail.get("sites", "?")))
        st = rec.get("sitesTotal", rec.get("sites_total", 0))
        if st:
            print("           sitesTotal: %s (unscaled sample; compare heapInuse)" % fmt_mb(st))

    ranked = sorted(agg.items(), key=lambda kv: -kv[1])[: args.top]
    print()
    print("== top %d sites (%s, %s) ==" % (
        len(ranked),
        "fn <- caller" if args.by_caller else "fn, callers rolled up",
        "all rows" if args.all_rows else "last row per PID"))
    for key, b in ranked:
        print("%12s  %s" % (fmt_mb(b), " <- ".join(key) if isinstance(key, tuple) else key))
    total = sum(agg.values())
    print()
    print("attributed total: %s across %d pids" % (fmt_mb(total), len(per_pid)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
