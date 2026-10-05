"""Unit tests for the fail-closed verify layer in run_eval.py.

Grounding: arXiv:2610.02142 - text-only success judgment is fail-open;
a task declaring a verify block must have its assertion enforced, and an
empty/malformed verify block must fail rather than silently pass.

Run: python3 -m pytest scripts/eval/test_verify.py  (or python3 scripts/eval/test_verify.py)
"""

import sys
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

from run_eval import CSV_FIELDS, run_verify  # noqa: E402


def _test_artifact_pass():
    with tempfile.TemporaryDirectory() as d:
        f = Path(d) / "out.txt"
        f.write_text("hello extractor")
        status, _ = run_verify({"artifact": str(f)})
        assert status == "pass", "existing artifact must pass"


def _test_artifact_missing_fails():
    status, detail = run_verify({"artifact": "/nonexistent/__nope__.txt"})
    assert status == "fail", "missing artifact must fail (fail-closed)"
    assert "missing" in detail


def _test_content_contains():
    with tempfile.TemporaryDirectory() as d:
        f = Path(d) / "out.txt"
        f.write_text("title=ok body=ok")
        s1, _ = run_verify({"artifact": str(f), "content_contains": ["title=ok"]})
        assert s1 == "pass"
        s2, d2 = run_verify({"artifact": str(f), "content_contains": ["title=missing"]})
        assert s2 == "fail", "missing substring must fail"
        assert "title=missing" in d2


def _test_command_exit_code():
    s1, _ = run_verify({"command": ["true"]})
    assert s1 == "pass", "exit 0 must pass"
    s2, d2 = run_verify({"command": ["sh", "-c", "echo boom >&2; exit 3"]})
    assert s2 == "fail", "non-zero exit must fail"
    assert "boom" in d2, "stderr tail should be surfaced in detail"


def _test_empty_verify_fails_closed():
    status, detail = run_verify({})
    assert status == "fail", "verify block with no assertion must fail, not skip"
    assert "no artifact" in detail


def _test_combined_all_must_pass():
    with tempfile.TemporaryDirectory() as d:
        f = Path(d) / "a.txt"
        f.write_text("x")
        # artifact passes + command fails -> overall fail
        s, detail = run_verify({"artifact": str(f), "command": ["false"]})
        assert s == "fail" and "exit" in detail
        # both pass -> pass
        s2, _ = run_verify({"artifact": str(f), "command": ["true"]})
        assert s2 == "pass"


def _test_csv_fields_carry_verify():
    assert "verify_status" in CSV_FIELDS and "verify_detail" in CSV_FIELDS


def main():
    tests = [v for k, v in sorted(globals().items()) if k.startswith("_test_")]
    for t in tests:
        t()
        print(f"PASS {t.__name__}")
    print(f"\n{len(tests)} tests passed")


if __name__ == "__main__":
    main()
