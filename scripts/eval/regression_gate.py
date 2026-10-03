#!/usr/bin/env python3
"""Regression gate for continuous agent evaluation (r442).

Compares the latest eval run's aggregate score against a committed baseline
file (tests/eval-baselines/<task_set>.json). Exits 1 when the score drops
more than the threshold (default 5%), so CI can block regressions the way
unit tests block code regressions. Baselines move forward explicitly via
--update-baseline (committed by a human or a maintenance PR, never silently
inside a gated run).

Usage:
    python scripts/eval/regression_gate.py --trend .tmp/agent-eval/trend.jsonl \
        --task-set eval-workbench --baseline tests/eval-baselines/eval-workbench.json
    python scripts/eval/regression_gate.py ... --update-baseline  # write new baseline
"""

import argparse
import json
import sys
from pathlib import Path

DEFAULT_THRESHOLD = 0.05


def load_trend(path: str) -> list[dict]:
    p = Path(path)
    if not p.exists():
        return []
    entries = []
    for line in p.read_text().splitlines():
        line = line.strip()
        if line:
            entries.append(json.loads(line))
    return entries


def latest_for(entries: list[dict], task_set: str) -> dict | None:
    for e in reversed(entries):
        if e.get("task_set") == task_set and e.get("task_version"):
            return e
    # Fall back to the last entry regardless of set (single-set trend files).
    return entries[-1] if entries else None


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--trend", required=True, help="trend.jsonl written by run_eval.py")
    ap.add_argument("--task-set", default="eval-workbench")
    ap.add_argument("--baseline", required=True, help="baseline JSON path")
    ap.add_argument("--threshold", type=float, default=DEFAULT_THRESHOLD,
                    help="max allowed fractional drop (default 0.05 = 5%%)")
    ap.add_argument("--update-baseline", action="store_true",
                    help="write the latest run as the new baseline instead of gating")
    ap.add_argument("--window", type=int, default=1,
                    help="average the latest N runs for stability (default 1)")
    args = ap.parse_args()

    entries = load_trend(args.trend)
    if not entries:
        print(f"FAIL: no trend entries in {args.trend}")
        return 1

    selected = [e for e in entries if e.get("task_set") == args.task_set] or entries
    window = selected[-args.window:]
    scores = [e["score"]["knight_score"] for e in window if e.get("score")]
    if not scores:
        print("FAIL: no knight_score in trend window")
        return 1
    current = sum(scores) / len(scores)

    baseline_path = Path(args.baseline)
    if args.update_baseline:
        baseline_path.parent.mkdir(parents=True, exist_ok=True)
        baseline_path.write_text(json.dumps({
            "task_set": args.task_set,
            "task_version": window[-1].get("task_version"),
            "knight_score": round(current, 4),
            "window": len(window),
            "source_runs": [e.get("run_id") for e in window],
        }, indent=2) + "\n")
        print(f"baseline updated: {baseline_path} knight_score={current:.4f}")
        return 0

    if not baseline_path.exists():
        # No baseline yet: adopt the current score as the reference WITHOUT
        # gating (first run must not fail the pipeline), but say so loudly.
        print(f"WARN: no baseline at {baseline_path}; adopting {current:.4f} as reference. "
              f"Commit it via --update-baseline.")
        return 0
    baseline = json.loads(baseline_path.read_text())
    ref = baseline["knight_score"]
    drop = (ref - current) / ref if ref > 0 else 0.0

    verdict = "PASS" if drop <= args.threshold else "FAIL"
    print(f"{verdict}: current={current:.4f} baseline={ref:.4f} drop={drop*100:.2f}% "
          f"(threshold {args.threshold*100:.0f}%, window {len(window)})")
    if verdict == "FAIL":
        print("Agent eval regression detected. Inspect the failing run's scorecard "
              "and task diff; fix or explicitly raise the baseline.")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
