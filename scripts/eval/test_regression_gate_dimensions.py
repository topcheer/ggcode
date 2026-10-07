"""Per-dimension assertion tests for regression_gate.py (r101).

Grounding: 2026 agent-eval guidance (futureagi, The Definitive Guide to AI
Agent Evaluation) - gate on per-dimension assertions instead of the aggregate
alone; a success_delta collapse must not hide behind time/tool gains inside
the weighted knight_score mean.

Run: python3 -m pytest scripts/eval/test_regression_gate_dimensions.py
"""

import json
import subprocess
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

from regression_gate import dimension_assertions, dim_mean  # noqa: E402

GATE = str(Path(__file__).parent / "regression_gate.py")


def _trend_entry(score: dict, run_id: str = "r1") -> dict:
    return {"task_set": "ts", "task_version": "v1", "run_id": run_id, "score": score}


def _run_gate(trend_path: Path, baseline_path: Path, *extra: str) -> subprocess.CompletedProcess:
    return subprocess.run(
        [sys.executable, GATE, "--trend", str(trend_path),
         "--task-set", "ts", "--baseline", str(baseline_path), *extra],
        capture_output=True, text=True,
    )


def _write_trend(path: Path, scores: list[dict]) -> None:
    path.write_text("\n".join(
        json.dumps(_trend_entry(s, f"r{i}")) for i, s in enumerate(scores)) + "\n")


def test_dimension_collapse_fails_despite_flat_aggregate():
    """Aggregate flat but success_delta drops beyond its strict threshold."""
    with tempfile.TemporaryDirectory() as d:
        trend = Path(d) / "trend.jsonl"
        base = Path(d) / "base.json"
        # Window: knight_score matches baseline exactly; success_delta fell
        # 0.10 -> -0.05 (drop 0.15 > the 0.05 success threshold).
        _write_trend(trend, [{
            "knight_score": 0.42, "success_delta": -0.05,
            "time_improvement": 0.30, "tool_reduction": 0.25,
        }])
        base.write_text(json.dumps({
            "knight_score": 0.42,
            "dimensions": {"success_delta": 0.10, "time_improvement": 0.30},
        }))
        r = _run_gate(trend, base)
        assert r.returncode == 1, f"dim collapse must fail the gate:\n{r.stdout}"
        assert "FAIL success_delta" in r.stdout
        assert "Failed dimensions: success_delta" in r.stdout


def test_aggregate_drop_still_fails_with_clean_dimensions():
    """Original behavior preserved: aggregate drop beyond threshold fails."""
    with tempfile.TemporaryDirectory() as d:
        trend = Path(d) / "trend.jsonl"
        base = Path(d) / "base.json"
        _write_trend(trend, [{"knight_score": 0.42, "trust_score": 0.05}])
        base.write_text(json.dumps({
            "knight_score": 0.50, "dimensions": {"trust_score": 0.05},
        }))
        r = _run_gate(trend, base)
        assert r.returncode == 1
        assert "FAIL" in r.stdout


def test_baseline_without_dimensions_aggregate_only():
    """Backward compat: no dimensions block -> aggregate gate only, PASS."""
    with tempfile.TemporaryDirectory() as d:
        trend = Path(d) / "trend.jsonl"
        base = Path(d) / "base.json"
        _write_trend(trend, [{"knight_score": 0.41, "success_delta": -0.90}])
        base.write_text(json.dumps({"knight_score": 0.42}))
        r = _run_gate(trend, base)
        assert r.returncode == 0, r.stdout
        assert "PASS" in r.stdout


def test_update_baseline_writes_dimensions_block():
    with tempfile.TemporaryDirectory() as d:
        trend = Path(d) / "trend.jsonl"
        base = Path(d) / "base.json"
        _write_trend(trend, [
            {"knight_score": 0.40, "success_delta": 0.10, "trust_score": 0.04},
            {"knight_score": 0.44, "success_delta": 0.20, "trust_score": 0.06},
        ])
        r = _run_gate(trend, base, "--update-baseline", "--window", "2")
        assert r.returncode == 0, r.stdout
        out = json.loads(base.read_text())
        assert out["knight_score"] == 0.42
        assert out["dimensions"]["success_delta"] == 0.15
        assert out["dimensions"]["trust_score"] == 0.05


def test_missing_subscores_skipped_not_crashed():
    """Window entries without the pinned dimension are skipped, not drops."""
    window = [{"score": {"knight_score": 0.4}}, {"score": {}}]
    assert dim_mean(window, "success_delta") is None
    assert dimension_assertions(
        window, {"dimensions": {"success_delta": 0.10}}, 0.10) == []


def test_success_delta_uses_half_threshold():
    """drop 0.06: fails the 0.05 success threshold, passes 0.10 for others."""
    window = [{"score": {"success_delta": 0.04, "trust_score": 0.04}}]
    results = dict(
        (dim, ok) for dim, _, _, ok in dimension_assertions(
            window, {"dimensions": {"success_delta": 0.10, "trust_score": 0.10}},
            0.10))
    assert results["success_delta"] is False
    assert results["trust_score"] is True


if __name__ == "__main__":
    for fn in [v for k, v in sorted(globals().items()) if k.startswith("test_")]:
        fn()
        print(f"ok {fn.__name__}")
