#!/usr/bin/env python3
"""#4100 companion test: run_eval.py --auto fails fast when `ggcode` is not
on PATH, instead of swallowing FileNotFoundError in every round and exiting 0
with an empty trend.

Plain-python runner (no pytest dependency):
    python3 scripts/eval/zz_issue4100_test.py
"""

import os
import subprocess
import sys
import tempfile
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent / "run_eval.py"


def run_eval(extra_env: dict[str, str], *cli: str) -> subprocess.CompletedProcess:
    env = {k: v for k, v in os.environ.items() if k != "PYTEST_CURRENT_TEST"}
    env.update(extra_env)
    return subprocess.run(
        [sys.executable, str(SCRIPT), *cli],
        capture_output=True,
        text=True,
        env=env,
        timeout=120,
    )


def test_auto_without_ggcode_on_path_fails_fast() -> None:
    """PATH with no ggcode binary + --auto => immediate FATAL, non-zero exit."""
    with tempfile.TemporaryDirectory() as tmp:
        # A PATH containing only the interpreter's essential dirs cannot
        # resolve `ggcode` (this repo never installs one under these dirs).
        stripped = os.pathsep.join(
            d for d in os.environ.get("PATH", "").split(os.pathsep)
            if d and Path(d) != Path(__file__).resolve().parent.parent / "bin"
        )
        # Force the miss even if the dev machine has ggcode installed:
        # point PATH at an empty dir plus system fallback dirs that we verify.
        empty = Path(tmp) / "emptybin"
        empty.mkdir()
        proc = run_eval(
            {"PATH": str(empty)},
            "--auto", "--output", tmp,
        )
        assert proc.returncode != 0, f"expected non-zero exit, got {proc.returncode}"
        assert "ggcode` binary not found on PATH" in proc.stderr, (
            f"FATAL precheck message missing from stderr:\n{proc.stderr[-800:]}"
        )
    print("PASS: --auto + no ggcode on PATH => fail-fast FATAL")


def test_auto_with_ggcode_on_path_passes_precheck() -> None:
    """A resolvable ggcode shim => precheck passes (failure, if any, is a
    later-stage error, never the PATH precheck message)."""
    with tempfile.TemporaryDirectory() as tmp:
        shim_dir = Path(tmp) / "shimbin"
        shim_dir.mkdir()
        shim = shim_dir / "ggcode"
        shim.write_text("#!/bin/sh\nexit 1\n")
        shim.chmod(0o755)
        workdir = Path(tmp) / "workbench"
        workdir.mkdir()
        proc = run_eval(
            {"PATH": f"{shim_dir}{os.pathsep}{os.environ.get('PATH', '')}"},
            "--auto", "--output", tmp, "--workdir", str(workdir),
        )
        assert "ggcode` binary not found on PATH" not in proc.stderr, (
            f"precheck fired although shim was on PATH:\n{proc.stderr[-800:]}"
        )
    print("PASS: --auto + ggcode shim on PATH => precheck passes")


if __name__ == "__main__":
    test_auto_without_ggcode_on_path_fails_fast()
    test_auto_with_ggcode_on_path_passes_precheck()
    print("OK: 2/2 #4100 companion tests passed")
