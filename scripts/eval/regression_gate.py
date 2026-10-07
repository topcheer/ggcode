#!/usr/bin/env python3
"""Regression gate for continuous agent evaluation (r442).

Compares the latest eval run's aggregate score against a committed baseline
file (tests/eval-baselines/<task_set>.json). Exits 1 when the score drops
more than the threshold (default 5%), so CI can block regressions the way
unit tests block code regressions. Baselines move forward explicitly via
--update-baseline (committed by a human or a maintenance PR, never silently
inside a gated run).

r101: the gate additionally asserts every sub-dimension the baseline pins
(per-dimension assertions beat aggregate scoring - a success_delta collapse
can hide inside the weighted mean behind time/tool gains). Baselines carry
an optional "dimensions" block; --update-baseline records window means.

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
DEFAULT_DIM_THRESHOLD = 0.10

# Sub-dimensions emitted by run_eval.py alongside knight_score (see
# compute_knight_score). The gate used to read only the aggregate, so a
# success_delta collapse could hide behind the other 0.65 of the weight
# (r101; futureagi 2026 "a CI gate that beats aggregate scoring").
DIMENSIONS = [
    "success_delta", "pass_at_k", "time_improvement", "tool_reduction",
    "turn_reduction", "skill_rate", "trust_score",
]


def dim_mean(window: list[dict], dim: str) -> float | None:
    """Mean of a score sub-dimension across the window, None when absent."""
    vals = [e["score"][dim] for e in window
            if isinstance(e.get("score"), dict)
            and isinstance(e["score"].get(dim), (int, float))]
    return sum(vals) / len(vals) if vals else None


def dimension_assertions(window: list[dict], baseline: dict,
                         default_thr: float) -> list[tuple[str, float, float, bool]]:
    """Per-dimension gate assertions for dims the baseline pins.

    Absolute drop (ref - current), not fractional: dimensions like
    success_delta legitimately range negative where a fractional comparison
    is meaningless. success_delta and pass_at_k use a stricter threshold
    (half) because task success is what users feel first - and single-sample
    Bernoulli noise on a 5-task run has sigma ~0.22, so the gate only makes
    sense on pass^k (--repeat) or windowed data. Window entries lacking the
    dimension are skipped, never counted as drops.
    """
    refs = baseline.get("dimensions") or {}
    results = []
    for dim, ref in refs.items():
        current = dim_mean(window, dim)
        if current is None:
            continue
        thr = default_thr / 2 if dim in ("success_delta", "pass_at_k") else default_thr
        results.append((dim, float(ref), current, (float(ref) - current) <= thr))
    return results


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
    ap.add_argument("--dim-threshold", type=float, default=DEFAULT_DIM_THRESHOLD,
                    help="max allowed ABSOLUTE drop per pinned sub-dimension "
                         "(default 0.10; success_delta uses half of this)")
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
        dims = {}
        for dim in DIMENSIONS:
            m = dim_mean(window, dim)
            if m is not None:
                dims[dim] = round(m, 4)
        baseline_path.write_text(json.dumps({
            "task_set": args.task_set,
            "task_version": window[-1].get("task_version"),
            "knight_score": round(current, 4),
            "dimensions": dims,
            "window": len(window),
            "source_runs": [e.get("run_id") for e in window],
        }, indent=2) + "\n")
        print(f"baseline updated: {baseline_path} knight_score={current:.4f} "
              f"dimensions={sorted(dims)}")
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

    dim_results = dimension_assertions(window, baseline, args.dim_threshold)
    failed_dims = []
    for dim, d_ref, d_cur, ok in dim_results:
        thr = args.dim_threshold / 2 if dim in ("success_delta", "pass_at_k") else args.dim_threshold
        print(f"  {'ok  ' if ok else 'FAIL'} {dim}: current={d_cur:.4f} "
              f"baseline={d_ref:.4f} drop={d_ref - d_cur:.4f} (max {thr:.2f})")
        if not ok:
            failed_dims.append(dim)

    if verdict == "FAIL" or failed_dims:
        print("Agent eval regression detected. Inspect the failing run's scorecard "
              "and task diff; fix or explicitly raise the baseline.")
        if failed_dims:
            print(f"Failed dimensions: {', '.join(failed_dims)} (per-dimension "
                  "assertions catch collapses the aggregate mean hides).")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
