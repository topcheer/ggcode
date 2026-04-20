#!/usr/bin/env python3
"""Knight evaluation orchestrator.

Drives automated multi-turn conversations with ggcode agent via the dummy IM
adapter's HTTP+SSE interface. Supports A/B benchmarking (baseline vs
Knight-assisted) and produces CSV metrics + scorecard.

Usage:
    # Start ggcode daemon with dummy adapter first:
    ggcode daemon -c docs/examples/eval-dummy.yaml &

    # Run evaluation:
    python scripts/eval/run_eval.py --port-file .tmp/dummy-adapter-port --output .tmp/knight-eval/results.csv

    # Or with explicit URL:
    python scripts/eval/run_eval.py --base-url http://127.0.0.1:12345 --output .tmp/knight-eval/results.csv
"""

import argparse
import csv
import json
import os
import subprocess
import sys
import time
from pathlib import Path

import httpx

# Add parent to path for local imports
sys.path.insert(0, str(Path(__file__).parent))

from llm_client import FALLBACK_ANSWER, LLMClient
from task_templates import TASKS


# ---------------------------------------------------------------------------
# SSE client
# ---------------------------------------------------------------------------

def read_port_file(path: str, timeout: float = 30.0) -> tuple[str, str]:
    """Read addr and shutdown token from the dummy adapter port file.

    Returns (base_url, shutdown_token).
    """
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            content = Path(path).read_text().strip().split("\n")
            addr = content[0].strip()
            token = content[1].strip() if len(content) > 1 else ""
            return f"http://{addr}", token
        except (FileNotFoundError, IndexError):
            time.sleep(0.5)
    raise FileNotFoundError(f"Port file {path} not found after {timeout}s")


def wait_for_healthz(base_url: str, timeout: float = 30.0) -> bool:
    """Poll /healthz until it returns ok."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            resp = httpx.get(f"{base_url}/healthz", timeout=5.0)
            if resp.status_code == 200:
                return True
        except httpx.ConnectError:
            pass
        time.sleep(0.5)
    return False


def consume_sse_events(
    base_url: str,
    llm: LLMClient,
    send_fn,  # callable(text: str) -> None
    task_timeout: float,
    log_file: str | None = None,
) -> dict:
    """Connect to /events SSE stream and process events until task completes.

    Handles:
    - approval_request: calls LLM to generate answer, then sends via send_fn
    - round_done: tracks rounds
    - knight_report: records count
    - text: logs
    - status: logs

    Returns event summary dict.
    """
    summary = {
        "rounds": 0,
        "knight_reports": 0,
        "tool_calls": 0,
        "tool_errors": 0,
        "ask_user_count": 0,
        "text_chunks": 0,
        "timed_out": False,
        "events": [],
    }

    deadline = time.time() + task_timeout
    log_fh = open(log_file, "a") if log_file else None

    try:
        with httpx.stream("GET", f"{base_url}/events", timeout=task_timeout + 60) as resp:
            buf = ""
            for raw_chunk in resp.iter_text():
                if time.time() > deadline:
                    summary["timed_out"] = True
                    break

                buf += raw_chunk
                # Parse SSE frames
                while "\n\n" in buf:
                    frame, buf = buf.split("\n\n", 1)
                    event_type, data = _parse_sse_frame(frame)
                    if event_type is None:
                        continue

                    event = {"type": event_type, "data": data}
                    summary["events"].append(event)

                    if log_fh:
                        log_fh.write(json.dumps(event) + "\n")
                        log_fh.flush()

                    if event_type == "approval_request":
                        summary["ask_user_count"] += 1
                        _handle_approval(llm, send_fn, data)

                    elif event_type == "round_done":
                        summary["rounds"] += 1

                    elif event_type == "knight_report":
                        summary["knight_reports"] += 1

                    elif event_type == "tool_result":
                        summary["tool_calls"] += 1
                        if data.get("is_error"):
                            summary["tool_errors"] += 1

                    elif event_type == "text":
                        summary["text_chunks"] += 1

    except httpx.ReadTimeout:
        summary["timed_out"] = True
    finally:
        if log_fh:
            log_fh.close()

    return summary


def _parse_sse_frame(frame: str) -> tuple[str | None, dict]:
    """Parse a single SSE frame into (event_type, data_dict)."""
    event_type = None
    data_str = ""

    for line in frame.split("\n"):
        if line.startswith("event: "):
            event_type = line[7:].strip()
        elif line.startswith("data: "):
            data_str += line[6:]

    if not event_type or not data_str:
        return None, {}

    try:
        return event_type, json.loads(data_str)
    except json.JSONDecodeError:
        return event_type, {"raw": data_str}


def _handle_approval(llm: LLMClient, send_fn, data: dict):
    """Handle an approval_request by generating an answer and sending it."""
    question = data.get("title", data.get("content", ""))
    if not question:
        # No question text — give a default positive response
        send_fn("Yes, proceed.")
        return

    answer = llm.answer_ask_user(question)
    send_fn(answer)


# ---------------------------------------------------------------------------
# Task runner
# ---------------------------------------------------------------------------

def run_task(
    base_url: str,
    task: dict,
    llm: LLMClient,
    log_file: str | None = None,
) -> dict:
    """Run a single evaluation task.

    Returns a result dict with task_id, metrics, and event summary.
    """
    task_id = task["id"]
    timeout = task.get("timeout_sec", 600)
    print(f"\n{'='*60}")
    print(f"[eval] Starting {task_id}: {task['description'][:60]}...")
    print(f"{'='*60}")

    # Step 1: Generate prompt (optionally via LLM)
    prompt = llm.generate_prompt(task["description"])
    print(f"[eval] Generated prompt ({len(prompt)} chars)")

    # Step 2: Send prompt with reset_metrics
    start_time = time.time()

    def send_fn(text: str):
        """Send a message to the dummy adapter."""
        try:
            resp = httpx.post(
                f"{base_url}/send",
                json={"text": text},
                params={"reset_metrics": "false"},
                timeout=30.0,
            )
            return resp.json()
        except Exception as e:
            print(f"[eval] send error: {e}")
            return {}

    # Initial send with metrics reset
    try:
        resp = httpx.post(
            f"{base_url}/send",
            json={"text": prompt},
            params={"reset_metrics": "true"},
            timeout=30.0,
        )
        result = resp.json()
        print(f"[eval] Prompt sent: message_id={result.get('message_id', 'unknown')}")
    except Exception as e:
        print(f"[eval] Failed to send prompt: {e}")
        return {
            "task_id": task_id,
            "success": False,
            "error": str(e),
            "elapsed_sec": 0,
        }

    # Step 3: Consume SSE events
    summary = consume_sse_events(base_url, llm, send_fn, timeout, log_file)

    elapsed = time.time() - start_time

    # Step 4: Collect final metrics from /status
    try:
        resp = httpx.get(f"{base_url}/status", timeout=10.0)
        status_data = resp.json()
        metrics = status_data.get("metrics", {})
    except Exception as e:
        print(f"[eval] Failed to get status: {e}")
        metrics = {}

    result = {
        "task_id": task_id,
        "task_type": task.get("type", ""),
        "success": not summary["timed_out"],
        "timed_out": summary["timed_out"],
        "elapsed_sec": round(elapsed, 1),
        "rounds": summary["rounds"],
        "tool_calls": metrics.get("total_tool_calls", summary["tool_calls"]),
        "tool_errors": metrics.get("tool_errors", summary["tool_errors"]),
        "ask_user_count": metrics.get("ask_user_count", summary["ask_user_count"]),
        "knight_reports": summary["knight_reports"],
        "user_messages": metrics.get("user_messages", 1),
        "rework_count": metrics.get("rework_count", 0),
    }

    print(f"[eval] Task {task_id} complete: success={result['success']} "
          f"elapsed={result['elapsed_sec']}s tools={result['tool_calls']} "
          f"errors={result['tool_errors']} rounds={result['rounds']}")

    return result


# ---------------------------------------------------------------------------
# Scorecard
# ---------------------------------------------------------------------------

def compute_knight_score(baseline: list[dict], knight: list[dict]) -> dict:
    """Compute Knight Score comparing baseline vs knight-assisted results.

    Knight Score =
        35% * task success rate delta
      + 20% * elapsed time improvement
      + 15% * tool call reduction
      + 10% * turn reduction
      + 10% * useful skill rate
      + 10% * operator trust score
    """
    if not baseline or not knight:
        return {"knight_score": 0, "note": "insufficient data"}

    # Match tasks by ID
    baseline_map = {r["task_id"]: r for r in baseline}
    knight_map = {r["task_id"]: r for r in knight}

    common_ids = set(baseline_map) & set(knight_map)
    if not common_ids:
        return {"knight_score": 0, "note": "no common tasks"}

    n = len(common_ids)

    # Success rate delta (0 to 1)
    b_success = sum(1 for tid in common_ids if baseline_map[tid].get("success"))
    k_success = sum(1 for tid in common_ids if knight_map[tid].get("success"))
    success_delta = (k_success - b_success) / n

    # Elapsed time improvement (normalized)
    time_improvements = []
    for tid in common_ids:
        b_t = baseline_map[tid].get("elapsed_sec", 1)
        k_t = knight_map[tid].get("elapsed_sec", 1)
        if b_t > 0:
            time_improvements.append((b_t - k_t) / b_t)
    time_avg = sum(time_improvements) / len(time_improvements) if time_improvements else 0

    # Tool call reduction (normalized)
    tool_improvements = []
    for tid in common_ids:
        b_tc = max(baseline_map[tid].get("tool_calls", 0), 1)
        k_tc = knight_map[tid].get("tool_calls", 0)
        tool_improvements.append((b_tc - k_tc) / b_tc)
    tool_avg = sum(tool_improvements) / len(tool_improvements) if tool_improvements else 0

    # Turn reduction
    turn_improvements = []
    for tid in common_ids:
        b_r = max(baseline_map[tid].get("rounds", 0), 1)
        k_r = knight_map[tid].get("rounds", 0)
        turn_improvements.append((b_r - k_r) / b_r)
    turn_avg = sum(turn_improvements) / len(turn_improvements) if turn_improvements else 0

    # Useful skill rate (knight reports that actually helped)
    k_reports = sum(knight_map[tid].get("knight_reports", 0) for tid in common_ids)
    skill_rate = min(k_reports / max(n, 1), 1.0)

    # Trust score (fewer errors = more trust)
    k_errors = sum(knight_map[tid].get("tool_errors", 0) for tid in common_ids)
    b_errors = sum(baseline_map[tid].get("tool_errors", 0) for tid in common_ids)
    trust = max(0, (b_errors - k_errors)) / max(b_errors, 1)

    knight_score = (
        0.35 * max(min(success_delta, 1), -1)
        + 0.20 * max(min(time_avg, 1), -1)
        + 0.15 * max(min(tool_avg, 1), -1)
        + 0.10 * max(min(turn_avg, 1), -1)
        + 0.10 * skill_rate
        + 0.10 * max(min(trust, 1), 0)
    )

    return {
        "knight_score": round(knight_score, 4),
        "success_delta": round(success_delta, 4),
        "time_improvement": round(time_avg, 4),
        "tool_reduction": round(tool_avg, 4),
        "turn_reduction": round(turn_avg, 4),
        "skill_rate": round(skill_rate, 4),
        "trust_score": round(trust, 4),
        "baseline_success_rate": round(b_success / n, 4),
        "knight_success_rate": round(k_success / n, 4),
        "n_tasks": n,
    }


def write_scorecard(path: str, results: list[dict], score: dict, mode: str):
    """Write a markdown scorecard file."""
    with open(path, "w") as f:
        f.write(f"# Knight Evaluation Scorecard ({mode})\n\n")
        f.write(f"**Date:** {time.strftime('%Y-%m-%d %H:%M')}\n")
        f.write(f"**Tasks:** {len(results)}\n")
        f.write(f"**Mode:** {mode}\n\n")

        f.write("## Knight Score\n\n")
        for k, v in score.items():
            f.write(f"- **{k}:** {v}\n")

        f.write("\n## Per-Task Results\n\n")
        f.write("| Task | Type | Success | Elapsed | Tools | Errors | Rounds | Knight |\n")
        f.write("|------|------|---------|---------|-------|--------|--------|--------|\n")
        for r in results:
            f.write(
                f"| {r['task_id']} | {r.get('task_type', '')} | "
                f"{'✓' if r.get('success') else '✗'} | "
                f"{r.get('elapsed_sec', 0):.1f}s | "
                f"{r.get('tool_calls', 0)} | "
                f"{r.get('tool_errors', 0)} | "
                f"{r.get('rounds', 0)} | "
                f"{r.get('knight_reports', 0)} |\n"
            )

        f.write("\n## Key Observations\n\n")

        best = max(results, key=lambda r: r.get("tool_calls", 0))
        worst = min(results, key=lambda r: 1 if r.get("success") else 0)
        f.write(f"- **Most improved:** {best['task_id']}\n")
        f.write(f"- **Worst regression:** {worst['task_id']}\n")
        f.write(f"- **Timed out:** {sum(1 for r in results if r.get('timed_out'))}\n")


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(description="Knight evaluation orchestrator")
    parser.add_argument("--base-url", help="Dummy adapter base URL (e.g. http://127.0.0.1:12345)")
    parser.add_argument("--port-file", help="Path to dummy adapter port file")
    parser.add_argument("--output", required=True, help="Output CSV file path")
    parser.add_argument("--log-dir", help="Directory for SSE event logs")
    parser.add_argument("--mode", default="knight", help="Eval mode: baseline or knight")
    parser.add_argument("--run-id", default=time.strftime("%Y%m%d-%H%M%S"), help="Run identifier")
    parser.add_argument("--tasks", help="Comma-separated task IDs to run (default: all)")
    parser.add_argument("--llm-model", default="glm-5-turbo", help="LLM model for prompt gen")
    parser.add_argument("--llm-base-url", help="LLM API base URL")
    parser.add_argument("--no-llm", action="store_true", help="Skip LLM, use raw descriptions")
    parser.add_argument("--timeout", type=float, default=600, help="Per-task timeout in seconds")
    args = parser.parse_args()

    # Resolve base URL
    if args.base_url:
        base_url = args.base_url.rstrip("/")
    elif args.port_file:
        base_url, _ = read_port_file(args.port_file)
    else:
        parser.error("Either --base-url or --port-file is required")

    print(f"[eval] Connecting to {base_url}")

    # Wait for healthz
    if not wait_for_healthz(base_url):
        print(f"[eval] ERROR: {base_url}/healthz not responding", file=sys.stderr)
        sys.exit(1)
    print("[eval] Dummy adapter is ready")

    # Setup LLM client
    if args.no_llm:
        llm = None
    else:
        llm = LLMClient(
            base_url=args.llm_base_url,
            model=args.llm_model,
        )

    # Select tasks
    if args.tasks:
        task_ids = [t.strip() for t in args.tasks.split(",")]
        tasks = [t for t in TASKS if t["id"] in task_ids]
    else:
        tasks = TASKS

    print(f"[eval] Running {len(tasks)} tasks in {args.mode} mode")

    # Setup output
    output_path = Path(args.output)
    output_path.parent.mkdir(parents=True, exist_ok=True)

    log_dir = args.log_dir or str(output_path.parent)
    Path(log_dir).mkdir(parents=True, exist_ok=True)

    # Write CSV header
    csv_fields = [
        "run_id", "phase", "mode", "task_id", "task_type",
        "success", "timed_out", "elapsed_sec", "tool_calls",
        "tool_errors", "ask_user_count", "knight_reports",
        "user_messages", "rounds", "rework_count",
    ]
    with open(output_path, "w", newline="") as f:
        w = csv.DictWriter(f, fieldnames=csv_fields)
        w.writeheader()

    # Run tasks
    results = []
    for task in tasks:
        log_file = f"{log_dir}/{args.run_id}_{task['id']}.ndjson"
        result = run_task(base_url, task, llm, log_file)
        result["run_id"] = args.run_id
        result["phase"] = args.mode
        result["mode"] = args.mode
        results.append(result)

        # Append to CSV
        with open(output_path, "a", newline="") as f:
            w = csv.DictWriter(f, fieldnames=csv_fields)
            w.writerow({k: result.get(k, "") for k in csv_fields})

    # Write scorecard
    score_path = output_path.with_suffix(".scorecard.md")
    score = compute_knight_score(results, results)  # self-compare for single run
    write_scorecard(str(score_path), results, score, args.mode)

    print(f"\n[eval] Complete: {len(results)} tasks")
    print(f"[eval] Results: {output_path}")
    print(f"[eval] Scorecard: {score_path}")

    # Summary
    successes = sum(1 for r in results if r.get("success"))
    total_tools = sum(r.get("tool_calls", 0) for r in results)
    total_time = sum(r.get("elapsed_sec", 0) for r in results)
    print(f"[eval] Success rate: {successes}/{len(results)} ({100*successes//len(results)}%)")
    print(f"[eval] Total tool calls: {total_tools}")
    print(f"[eval] Total elapsed: {total_time:.1f}s")


if __name__ == "__main__":
    main()
