#!/usr/bin/env python3
"""Deterministic eval-harness integrity gate (r358; regression-blocks CI door).

2026 agent CI/CD guidance (baeseokjae five-gate model; futureagi) marks
regression blocks as a gate traditional pipelines lack. The full
LLM-in-the-loop eval (`make knight-eval`) is too costly/flaky for CI, but
the HARNESS itself can be gated deterministically:

  1. Task-template integrity: both TASKS sets must have unique non-empty
     ids, valid types, non-empty descriptions, sane timeouts, and typed
     expect_* flags. A malformed template would silently skew every eval
     run - that is a regression of the measurement instrument.
  2. Scoring smoke: compute_knight_score on a FIXED input must return the
     hand-computed expected dict. If someone re-weights the score formula,
     this gate forces the change to be visible in CI.

Exit 0 = harness intact. Zero third-party deps (stdlib only).
"""

import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

VALID_TYPES = {"code_edit", "test_debug", "docs"}
REQUIRED_FIELDS = ("id", "type", "description", "timeout_sec")


def validate_template(module_name: str) -> list[str]:
    """Return a list of violation strings for one TASKS module."""
    import importlib

    mod = importlib.import_module(module_name)
    tasks = getattr(mod, "TASKS", None)
    if not isinstance(tasks, list) or not tasks:
        return [f"{module_name}: TASKS missing/empty"]

    errors = []
    seen: set[str] = set()
    for i, task in enumerate(tasks):
        if not isinstance(task, dict):
            errors.append(f"{module_name}[{i}]: not a dict")
            continue
        for field in REQUIRED_FIELDS:
            v = task.get(field)
            if v is None or (isinstance(v, str) and not v.strip()):
                errors.append(f"{module_name}[{i}] ({task.get('id', '?')}): field {field!r} empty/missing")
        tid = task.get("id")
        if isinstance(tid, str) and tid:
            if tid in seen:
                errors.append(f"{module_name}: duplicate id {tid!r}")
            seen.add(tid)
        if task.get("type") not in VALID_TYPES:
            errors.append(f"{module_name}[{i}] ({tid or '?'}): invalid type {task.get('type')!r}")
        timeout = task.get("timeout_sec")
        if not isinstance(timeout, int) or isinstance(timeout, bool) or timeout <= 0:
            errors.append(f"{module_name}[{i}] ({tid or '?'}): timeout_sec must be a positive int, got {timeout!r}")
        for flag in ("expect_ask_user", "expect_knight"):
            if flag in task and not isinstance(task[flag], bool):
                errors.append(f"{module_name}[{i}] ({tid or '?'}): {flag} must be bool, got {task[flag]!r}")
    return errors


def smoke_compute_knight_score() -> list[str]:
    """Fixed-input smoke of the scoring formula with a hand-computed value."""
    from run_eval import compute_knight_score

    errors = []
    empty = compute_knight_score([], [])
    if empty.get("knight_score") != 0 or "note" not in empty:
        errors.append(f"empty-input contract broken: {empty!r}")

    baseline = [{
        "task_id": "t1", "success": True, "elapsed_sec": 100,
        "tool_calls": 20, "rounds": 5, "tool_errors": 4,
    }]
    knight = [{
        "task_id": "t1", "success": True, "elapsed_sec": 50,
        "tool_calls": 10, "rounds": 4, "tool_errors": 0, "knight_reports": 1,
    }]
    got = compute_knight_score(baseline, knight)
    expected = 0.395  # 0.35*0 + 0.20*0.5 + 0.15*0.5 + 0.10*0.2 + 0.10*1.0 + 0.10*1.0
    if abs(got.get("knight_score", -1) - expected) > 1e-9:
        errors.append(
            f"score formula drifted: got {got.get('knight_score')!r}, expected {expected} "
            f"(if re-weighted intentionally, update the hand-computed expectation here)"
        )
    if got.get("n_tasks") != 1 or got.get("knight_success_rate") != 1.0:
        errors.append(f"aux fields wrong: {got!r}")
    return errors


def main() -> int:
    all_errors = []
    for module in ("task_templates", "task_templates_teamclaw"):
        try:
            all_errors += validate_template(module)
        except Exception as exc:  # import failure = harness broken
            all_errors.append(f"{module}: import failed: {exc}")
    try:
        all_errors += smoke_compute_knight_score()
    except Exception as exc:
        all_errors.append(f"compute_knight_score smoke raised: {exc}")

    if all_errors:
        print("[eval-check] FAILED:")
        for e in all_errors:
            print(f"  - {e}")
        return 1
    print("[eval-check] task templates valid, scoring formula intact")
    return 0


if __name__ == "__main__":
    sys.exit(main())
