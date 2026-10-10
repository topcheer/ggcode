"""Tests for pr-replay task synthesis (r15, Change2Task-style supply).

Run: python3 -m pytest scripts/eval/test_pr_task_replay.py
"""

import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

from pr_task_replay import REPRODUCE_BRIEF, build_tasks, linked_issue_number, load_prefetch  # noqa: E402

FIXTURE_RECORDS = [
    {
        "pr_number": 101,
        "pr_title": "fix: deadlock in config set",
        "merged_at": "2026-10-08T12:00:00Z",
        "base_ref_oid": "abc1234",
        "issue_number": 99,
        "prompt_source": "issue",
        "prompt": "## deadlock\n`/config set fallbacks` hangs forever",
        "merge_diff": "diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go",
        "source_repo": "topcheer/ggcode",
    },
    {
        "pr_number": 87,
        "pr_title": "fix: strip code fence",
        "merged_at": "2026-09-30T08:15:30Z",
        "base_ref_oid": "def5678",
        "issue_number": None,
        "prompt_source": "pr-body",
        "prompt": "odd trailing fences leak into output",
        "merge_diff": "",
        "source_repo": "topcheer/ggcode",
    },
]


def test_build_tasks_fields_and_provenance(tmp_path):
    tasks = build_tasks(FIXTURE_RECORDS, schema=1)
    assert [t["id"] for t in tasks] == ["pr-101", "pr-87"], "ids are pr-<number>, sorted"
    t101 = tasks[0]
    assert t101["prompt"].startswith("## deadlock")
    assert t101["prompt"].endswith(REPRODUCE_BRIEF.strip()), "reproduce brief appended"
    assert t101["base_commit"] == "abc1234"
    assert t101["ground_truth"]["merge_diff"].startswith("diff --git")
    assert t101["ground_truth"]["test_cmd"].startswith("go test")
    assert t101["added_at"] == "2026-10-08", "added_at is merge date (UTC)"
    assert "pr-replay" in t101["tags"] and "src:issue" in t101["tags"]
    assert t101["version"] == "v1"


def test_build_tasks_fail_closed_on_missing_fields():
    bad = [dict(FIXTURE_RECORDS[0], base_ref_oid="")]
    try:
        build_tasks(bad, schema=1)
    except ValueError as e:
        assert "base_ref_oid" in str(e)
    else:
        raise AssertionError("missing required field must fail loudly, not silently degrade")


def test_build_tasks_rejects_unknown_schema():
    try:
        build_tasks(FIXTURE_RECORDS, schema=2)
    except ValueError:
        pass
    else:
        raise AssertionError("unknown schema must be rejected")


def test_load_prefetch_roundtrip(tmp_path):
    p = tmp_path / "prefetch.json"
    p.write_text(json.dumps({"schema": 1, "records": FIXTURE_RECORDS}))
    tasks = load_prefetch(p)
    assert len(tasks) == 2
    assert tasks[0]["id"] == "pr-101"


def test_linked_issue_number_patterns():
    assert linked_issue_number("Fixes #3736\n\nsomething") == 3736
    assert linked_issue_number("closes #42") == 42
    assert linked_issue_number("resolves #7 and #8") == 7, "first reference wins"
    assert linked_issue_number("see issue 12") is None, "bare mention is not a link"
    assert linked_issue_number("") is None


def test_registry_rotation_compat():
    """pr-replay tasks must slot into the r442 TaskRegistry unchanged."""
    from task_registry import TaskRegistry

    reg = TaskRegistry({"pr-replay": build_tasks(FIXTURE_RECORDS, schema=1)})
    assert reg.names() == ["pr-replay"]
    fresh = reg.exclude_before("pr-replay", "2026-10-01")
    assert [t["id"] for t in fresh] == ["pr-101"], "added_at drives contamination exclusion"
    sampled = reg.sample("pr-replay", 1, seed=7)
    assert len(sampled) == 1
