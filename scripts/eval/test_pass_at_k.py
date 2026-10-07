#!/usr/bin/env python3
"""Tests for r102 pass^k: repeat execution, collapse semantics, gate dimension.

tau-bench pass^k lineage: a task passes at k only if every repeat passed.
"""

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from run_eval import collapse_repeats, compute_knight_score  # noqa: E402


def _res(tid, success=True, elapsed=10.0, tools=5, verify="artifact_verified"):
    return {
        "task_id": tid, "task_type": "code", "success": success,
        "timed_out": False, "completed": success, "elapsed_sec": elapsed,
        "tool_calls": tools, "tool_errors": 0, "ask_user_count": 0,
        "knight_reports": 0, "user_messages": 1, "rounds": 1,
        "rework_count": 0, "verify_status": verify, "verify_detail": "",
    }


def test_collapse_all_pass():
    out = collapse_repeats([_res("cli-01#r1"), _res("cli-01#r2", elapsed=20.0)])
    assert len(out) == 1
    m = out[0]
    assert m["task_id"] == "cli-01"
    assert m["success"] is True          # pass@2
    assert m["repeats"] == 2
    assert not m.get("flaky")
    assert m["elapsed_sec"] == 15.0      # mean across repeats


def test_collapse_min_pass_semantics():
    out = collapse_repeats([_res("cli-01#r1", success=True),
                            _res("cli-01#r2", success=False)])
    assert out[0]["success"] is False    # one repeat failed -> task failed
    assert out[0]["flaky"] is True       # partial pass flagged


def test_collapse_timeout_or_ed():
    out = collapse_repeats([_res("cli-01#r1"),
                            _res("cli-01#r2", success=False, verify="verify_failed")])
    assert out[0]["timed_out"] is False
    assert out[0]["verify_status"] == "verify_failed"  # worst verify wins


def test_passthrough_without_suffix():
    src = _res("cli-01")
    out = collapse_repeats([src])
    assert len(out) == 1 and out[0]["task_id"] == "cli-01"
    assert out[0]["success"] is True
    assert "repeats" not in out[0]


def test_suffix_split_ignores_non_repeat_hashes():
    # An id whose '#r' suffix is not digits is NOT a repeat marker.
    out = collapse_repeats([_res("cli-01#rare")])
    assert out[0]["task_id"] == "cli-01#rare"


def test_score_exposes_pass_at_k():
    collapsed = collapse_repeats([
        _res("a#r1"), _res("a#r2"),
        _res("b#r1", success=False), _res("b#r2"),
    ])
    score = compute_knight_score(collapsed, collapsed)
    assert score["pass_at_k"] == 0.5     # 1 of 2 tasks passed all repeats
    assert score["knight_success_rate"] == 0.5


if __name__ == "__main__":
    for name, fn in sorted(globals().items()):
        if name.startswith("test_") and callable(fn):
            fn()
            print(f"ok {name}")
    print("all pass^k tests passed")
