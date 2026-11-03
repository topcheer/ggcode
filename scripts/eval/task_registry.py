#!/usr/bin/env python3

import random
from datetime import date

EPOCH = "1970-01-01"
DEFAULT_VERSION = "v1"

def _wrap(task: dict) -> dict:
    out = dict(task)
    out.setdefault("version", DEFAULT_VERSION)
    out.setdefault("added_at", EPOCH)
    out.setdefault("tags", [])
    return out

class TaskRegistry:

    def __init__(self, task_sets: dict):

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
        cut = date.fromisoformat(cutoff)
        return [
            t for t in self._sets[name]
            if date.fromisoformat(t["added_at"]) >= cut
        ]

    def sample(self, name: str, n: int, exclude_before: str | None = None, seed: int | None = None) -> list[dict]:
        pool = self.exclude_before(name, exclude_before) if exclude_before else self.get(name)
        if not pool:
            raise ValueError(f"empty pool for {name!r} after exclude_before={exclude_before}")
        if n >= len(pool):
            return list(pool)
        rng = random.Random(seed)
        return rng.sample(pool, n)

    def info(self) -> dict:
        return {
            name: {
                "count": len(tasks),
                "versions": sorted({t["version"] for t in tasks}),
                "newest": max(t["added_at"] for t in tasks),
            }
            for name, tasks in self._sets.items()
        }
