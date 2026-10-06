#!/usr/bin/env python3
"""Versioned task-set registry with rotation sampling (r442).

LiveBench-style continuous evaluation needs: (1) task sets with per-task
provenance (version, added_at, tags) so contamination-prone or stale tasks
can be excluded by date, (2) a rotation sampler that draws a fresh subset
per run instead of always burning the full static list (run_eval.py used to
hard-import TASKS and run everything), (3) a machine-readable registry view
for trend analysis.

Legacy templates (task_templates.py TASKS, task_templates_teamclaw.py) carry
no provenance; the registry wraps them with version "v1" and epoch added_at
so existing behavior is unchanged until tasks gain metadata.
"""

import random
from datetime import date

EPOCH = "1970-01-01"
DEFAULT_VERSION = "v1"


def _wrap(task: dict) -> dict:
    """Attach default provenance to a legacy task dict (no mutation)."""
    out = dict(task)
    out.setdefault("version", DEFAULT_VERSION)
    out.setdefault("added_at", EPOCH)
    out.setdefault("tags", [])
    return out


class TaskRegistry:
    """Registry over named task sets with rotation sampling."""

    def __init__(self, task_sets: dict):
        # task_sets: {"eval-workbench": [...], "teamclaw": [...]}
        self._sets = {name: [_wrap(t) for t in tasks] for name, tasks in task_sets.items()}

    def names(self):
        return sorted(self._sets.keys())

    def get(self, name: str, version: str | None = None) -> list[dict]:
        tasks = self._sets.get(name)
        if tasks is None:
            raise KeyError(f"unknown task set {name!r}; known: {self.names()}")
        if version is None:
            return list(tasks)
        matched = [t for t in tasks if t["version"] == version]
        if not matched:
            raise KeyError(f"task set {name!r} has no tasks at version {version}")
        return matched

    def exclude_before(self, name: str, cutoff: str) -> list[dict]:
        """Tasks in set added on/after cutoff (ISO date). Rotation hook."""
        cut = date.fromisoformat(cutoff)
        return [
            t for t in self._sets[name]
            if date.fromisoformat(t["added_at"]) >= cut
        ]

    def sample(self, name: str, n: int, exclude_before: str | None = None, seed: int | None = None) -> list[dict]:
        """Draw n tasks (rotation). Falls back to full set when n >= len."""
        pool = self.exclude_before(name, exclude_before) if exclude_before else self.get(name)
        if not pool:
            raise ValueError(f"empty pool for {name!r} after exclude_before={exclude_before}")
        if n >= len(pool):
            return list(pool)
        rng = random.Random(seed)
        return rng.sample(pool, n)

    def info(self) -> dict:
        """Machine-readable registry view for trend records."""
        return {
            name: {
                "count": len(tasks),
                "versions": sorted({t["version"] for t in tasks}),
                "newest": max(t["added_at"] for t in tasks),
            }
            for name, tasks in self._sets.items()
        }
