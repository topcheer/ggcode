#!/usr/bin/env python3
"""pr-replay: synthesize eval tasks from this repo's merged fix-PRs (r15).

Grounding: Change2Task (arXiv:2607.28591) / ORCA-bench (2607.28545) /
CoGate (2607.28529), 2026-07 - eval task supply should come from real
merged fixes, not hand-written templates, with per-task provenance so
rotation (r442) and contamination exclusion work naturally.

Two halves, deliberately split for offline agents:

1. PREFETCH (online, run by the main session once):
   `python3 scripts/eval/pr_task_replay.py prefetch --out pr_replay_tasks.json`
   shells `gh` to list merged PRs, extract linked issues (Fixes/Closes #N),
   fetch issue bodies + merge diffs, and write one offline JSON.

2. BUILD (offline, importable): build_tasks(records) turns the prefetch
   JSON into registry task dicts:
     id          = "pr-<number>"
     prompt      = linked issue body (fallback: PR body) + reproduce brief
     base_commit = PR base SHA (the code state the fix must be re-derived from)
     ground_truth = {"merge_diff": ..., "test_cmd": ...}
     added_at    = PR merge date (natural r442 rotation/contamination anchor)
     tags        = ["pr-replay"]
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path

REPRODUCE_BRIEF = (
    "\n\n---\nReproduce the task: check out base_commit, read the prompt "
    "above, and implement the fix. Compare against ground_truth after."
)

_LINK_RE = re.compile(r"(?:fix(?:e[sd])?|close[sd]?|resolve[sd]?)\s+#(\d+)", re.IGNORECASE)


def _gh(args: list[str]) -> str:
    proc = subprocess.run(["gh", *args], capture_output=True, text=True)
    if proc.returncode != 0:
        raise RuntimeError(f"gh {' '.join(args)} failed: {proc.stderr.strip()[:300]}")
    return proc.stdout


def fetch_merged_prs(repo: str, limit: int) -> list[dict]:
    """List merged PRs with the fields the synthesizer needs."""
    out = _gh([
        "pr", "list", "--repo", repo, "--state", "merged", "--limit", str(limit),
        "--json", "number,title,body,mergedAt,baseRefOid,headRefOid,mergeCommit",
    ])
    prs = json.loads(out)
    prs.sort(key=lambda p: p.get("mergedAt") or "", reverse=True)
    return prs


def linked_issue_number(pr_body: str) -> int | None:
    """First Fixes/Closes #N reference in the PR body."""
    m = _LINK_RE.search(pr_body or "")
    return int(m.group(1)) if m else None


def fetch_issue_body(repo: str, number: int) -> str:
    out = _gh(["issue", "view", str(number), "--repo", repo, "--json", "title,body"])
    data = json.loads(out)
    return f"{data.get('title', '')}\n\n{data.get('body') or ''}".strip()


def fetch_merge_diff(repo: str, pr: dict) -> str:
    merge_sha = (pr.get("mergeCommit") or {}).get("oid")
    if not merge_sha:
        return ""
    proc = subprocess.run(
        ["git", "-C", str(_repo_root()), "show", "--stat", merge_sha],
        capture_output=True, text=True,
    )
    if proc.returncode != 0:
        # merge commit not in local clone - fall back to gh
        try:
            return _gh(["pr", "diff", str(pr["number"]), "--repo", repo, "--patch"])
        except RuntimeError:
            return ""
    return proc.stdout


def _repo_root() -> Path:
    """Repo root relative to this script (scripts/eval/ -> repo top)."""
    return Path(__file__).resolve().parent.parent.parent


def prefetch(repo: str, limit: int, out_path: Path) -> int:
    """Online half: write the offline JSON. Returns record count."""
    prs = fetch_merged_prs(repo, limit)
    records = []
    for pr in prs:
        body = pr.get("body") or ""
        issue_no = linked_issue_number(body)
        prompt_src = fetch_issue_body(repo, issue_no) if issue_no else body
        if not prompt_src.strip():
            continue  # no task without a describable problem
        records.append({
            "pr_number": pr["number"],
            "pr_title": pr.get("title", ""),
            "merged_at": pr.get("mergedAt"),
            "base_ref_oid": pr.get("baseRefOid", ""),
            "issue_number": issue_no,
            "prompt_source": "issue" if issue_no else "pr-body",
            "prompt": prompt_src,
            "merge_diff": fetch_merge_diff(repo, pr),
            "source_repo": repo,
        })
    out_path.write_text(json.dumps({"schema": 1, "records": records}, indent=1))
    print(f"prefetched {len(records)} pr-replay records -> {out_path}", file=sys.stderr)
    return len(records)


def build_tasks(records: list[dict], schema: int = 1) -> list[dict]:
    """Offline half: prefetch JSON records -> registry task dicts.

    Fails loudly on schema mismatch or records missing required fields -
    a silently degraded task set poisons baselines (fail-closed, cf.
    research-r467 eval fail-closed).
    """
    if schema != 1:
        raise ValueError(f"unsupported prefetch schema {schema!r}")
    tasks = []
    for rec in records:
        missing = [k for k in ("pr_number", "prompt", "base_ref_oid", "merged_at") if not rec.get(k)]
        if missing:
            raise ValueError(f"record for PR {rec.get('pr_number', '?')} missing fields: {missing}")
        merged_day = datetime.fromisoformat(rec["merged_at"].replace("Z", "+00:00")) \
            .astimezone(timezone.utc).date().isoformat()
        tasks.append({
            "id": f"pr-{rec['pr_number']}",
            "prompt": rec["prompt"] + REPRODUCE_BRIEF,
            "base_commit": rec["base_ref_oid"],
            "ground_truth": {
                "merge_diff": rec.get("merge_diff", ""),
                "test_cmd": "go test -tags goolm ./...",
            },
            "added_at": merged_day,
            "version": "v1",
            "tags": ["pr-replay", f"src:{rec.get('prompt_source', 'unknown')}"],
        })
    tasks.sort(key=lambda t: t["id"])
    return tasks


def load_prefetch(path: Path) -> list[dict]:
    data = json.loads(path.read_text())
    return build_tasks(data.get("records", []), schema=data.get("schema", 0))


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    sub = ap.add_subparsers(dest="cmd", required=True)
    pf = sub.add_parser("prefetch", help="online: fetch merged PRs via gh")
    pf.add_argument("--repo", default="topcheer/ggcode")
    pf.add_argument("--limit", type=int, default=50)
    pf.add_argument("--out", default=str(Path(__file__).parent / "pr_replay_tasks.json"))
    args = ap.parse_args()
    if args.cmd == "prefetch":
        return 0 if prefetch(args.repo, args.limit, Path(args.out)) >= 0 else 1
    return 2


if __name__ == "__main__":
    sys.exit(main())
