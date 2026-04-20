#!/usr/bin/env python3
"""Knight evaluation orchestrator.

Drives automated multi-turn conversations with ggcode agent via the dummy IM
adapter's HTTP+SSE interface. Supports A/B benchmarking (baseline vs
Knight-assisted) and produces CSV metrics + scorecard.

Can be used standalone (pointing to a running daemon) or in one-shot mode
where it starts and stops the daemon automatically.

Usage:
    # One-shot mode (recommended — starts daemon, runs eval, shuts down):
    python scripts/eval/run_eval.py --auto --output .tmp/knight-eval/results.csv

    # Connect to already-running daemon:
    python scripts/eval/run_eval.py --port-file .tmp/dummy-adapter-port --output results.csv

    # A/B mode (runs baseline then knight, compares):
    python scripts/eval/run_eval.py --auto --ab --output .tmp/knight-eval
"""

import argparse
import csv
import json
import os
import signal
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
# Daemon lifecycle
# ---------------------------------------------------------------------------

def write_dummy_binding(working_dir: str):
    """Pre-write the IM binding JSON so daemon finds the dummy adapter binding.

    The daemon checks bindings before starting adapters. Dummy adapter's autoBind()
    runs after StartCurrentBindingAdapter has already checked, so we must pre-seed.
    """
    import hashlib
    bindings_path = Path.home() / ".ggcode" / "im-bindings.json"

    # Normalize workspace path (EvalSymlinks + lowercase on macOS)
    norm_dir = working_dir
    try:
        norm_dir = str(Path(working_dir).resolve())
    except Exception:
        pass

    key = f"{norm_dir}\x00dummy"
    binding = {
        "Workspace": norm_dir,
        "Platform": "dummy",
        "Adapter": "dummy",
        "TargetID": "eval-user",
        "ChannelID": "eval-channel",
        "BoundAt": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    }

    # Load existing bindings, add ours
    existing = {}
    if bindings_path.exists():
        try:
            existing = json.loads(bindings_path.read_text())
        except Exception:
            pass

    existing[key] = binding
    bindings_path.parent.mkdir(parents=True, exist_ok=True)
    bindings_path.write_text(json.dumps(existing, indent=2))
    print(f"[eval] Pre-seeded binding for {norm_dir}")


def start_daemon(
    config_path: str,
    port_file: str,
    working_dir: str,
    timeout: float = 180.0,
) -> subprocess.Popen:
    """Start ggcode daemon in background, wait for port file.

    Returns the Popen process handle.
    """
    # Clean up stale port file
    Path(port_file).unlink(missing_ok=True)

    # Pre-seed IM binding so daemon finds dummy adapter
    write_dummy_binding(working_dir)

    cmd = [
        "ggcode", "daemon",
        "--config", config_path,
        "--bypass",
    ]
    print(f"[eval] Starting daemon: {' '.join(cmd)}")
    proc = subprocess.Popen(
        cmd,
        cwd=working_dir,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )

    # Wait for port file
    deadline = time.time() + timeout
    while time.time() < deadline:
        if proc.poll() is not None:
            stderr = proc.stderr.read().decode() if proc.stderr else ""
            raise RuntimeError(f"Daemon exited early (code {proc.returncode}): {stderr[:500]}")
        if Path(port_file).exists():
            time.sleep(0.5)  # small grace for server to be ready
            return proc
        time.sleep(0.5)

    proc.kill()
    raise TimeoutError(f"Daemon did not produce port file {port_file} within {timeout}s")


def stop_daemon(proc: subprocess.Popen, base_url: str, shutdown_token: str = ""):
    """Gracefully stop the daemon via /shutdown or SIGTERM."""
    if shutdown_token:
        try:
            httpx.post(
                f"{base_url}/shutdown",
                headers={"Authorization": f"Bearer {shutdown_token}"},
                timeout=5.0,
            )
        except Exception:
            pass

    # Give it a moment, then SIGTERM
    try:
        proc.terminate()
        proc.wait(timeout=10)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait(timeout=5)


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


# Idle timeout: how long to wait after last event before declaring task done.
# After a round_done event, if no new events arrive within this window, the
# agent has finished processing.
TASK_IDLE_TIMEOUT = 30.0  # seconds of silence = task complete


def consume_sse_events(
    base_url: str,
    llm: LLMClient,
    send_fn,  # callable(text: str) -> None
    task_timeout: float,
    log_file: str | None = None,
) -> dict:
    """Connect to /events SSE stream and process events until task completes.

    Task completion is detected by:
    1. Idle timeout: no events for TASK_IDLE_TIMEOUT seconds after content received
    2. Hard timeout: task_timeout seconds total elapsed

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
        "completed": False,
        "events": [],
    }

    hard_deadline = time.time() + task_timeout
    last_event_time = time.time()
    seen_round_done = False
    log_fh = open(log_file, "a") if log_file else None

    try:
        with httpx.stream("GET", f"{base_url}/events", timeout=task_timeout + 120) as resp:
            buf = ""
            for raw_chunk in resp.iter_text():
                now = time.time()

                # Hard timeout
                if now > hard_deadline:
                    summary["timed_out"] = True
                    break

                # Idle timeout: after receiving any meaningful content (text/tool),
                # 30s of silence means the agent finished.
                if summary["text_chunks"] > 0 and (now - last_event_time) > TASK_IDLE_TIMEOUT:
                    summary["completed"] = True
                    break

                buf += raw_chunk
                # Parse SSE frames
                while "\n\n" in buf:
                    frame, buf = buf.split("\n\n", 1)
                    event_type, data = _parse_sse_frame(frame)
                    if event_type is None:
                        continue

                    last_event_time = time.time()
                    event = {"type": event_type, "data": data, "ts": now}
                    summary["events"].append(event)

                    if log_fh:
                        log_fh.write(json.dumps(event) + "\n")
                        log_fh.flush()

                    if event_type == "approval_request":
                        summary["ask_user_count"] += 1
                        _handle_approval(llm, send_fn, data)

                    elif event_type == "round_done":
                        summary["rounds"] += 1
                        seen_round_done = True

                    elif event_type == "knight_report":
                        summary["knight_reports"] += 1

                    elif event_type == "tool_result":
                        summary["tool_calls"] += 1
                        if data.get("is_error"):
                            summary["tool_errors"] += 1

                    elif event_type == "text":
                        summary["text_chunks"] += 1

                    elif event_type == "status":
                        # Status updates don't count as activity for idle detection
                        pass

            # Stream ended naturally
            if not summary["timed_out"] and summary["text_chunks"] > 0:
                summary["completed"] = True

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
        send_fn("Yes, proceed.")
        return

    if llm:
        answer = llm.answer_ask_user(question)
    else:
        answer = FALLBACK_ANSWER
    send_fn(answer)


# ---------------------------------------------------------------------------
# Task runner
# ---------------------------------------------------------------------------

def run_task(
    base_url: str,
    task: dict,
    llm: LLMClient | None,
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
    if llm:
        prompt = llm.generate_prompt(task["description"])
    else:
        prompt = task["description"]
    print(f"[eval] Prompt ({len(prompt)} chars): {prompt[:80]}...")

    start_time = time.time()

    def send_fn(text: str):
        """Send a message to the dummy adapter."""
        try:
            resp = httpx.post(
                f"{base_url}/send",
                json={"text": text},
                timeout=30.0,
            )
            return resp.json()
        except Exception as e:
            print(f"[eval] send error: {e}")
            return {}

    # Step 2: Start SSE consumer in a thread FIRST, so we don't miss events.
    # Then send the prompt. SSE connection must be established before /send
    # to avoid missing fast agent responses.
    import threading

    sse_result = [None]  # mutable container for thread result

    def sse_worker():
        try:
            sse_result[0] = consume_sse_events(base_url, llm, send_fn, timeout, log_file)
        except Exception as e:
            import traceback
            print(f"[eval] SSE worker error: {e}")
            traceback.print_exc()
            sse_result[0] = {
                "rounds": 0, "knight_reports": 0, "tool_calls": 0,
                "tool_errors": 0, "ask_user_count": 0, "text_chunks": 0,
                "timed_out": False, "completed": False, "events": [],
            }

    sse_thread = threading.Thread(target=sse_worker, daemon=True)
    sse_thread.start()

    # Small delay to let SSE connect (larger projects need more time)
    time.sleep(2.0)

    # Step 3: Send prompt with metrics reset (with retry)
    send_ok = False
    for attempt in range(3):
        try:
            resp = httpx.post(
                f"{base_url}/send",
                json={"text": prompt},
                params={"reset_metrics": "true"},
                timeout=60.0,
            )
            result = resp.json()
            print(f"[eval] Prompt sent: message_id={result.get('message_id', 'unknown')}")
            send_ok = True
            break
        except Exception as e:
            print(f"[eval] Send attempt {attempt+1}/3 failed: {e}")
            if attempt < 2:
                time.sleep(5)

    if not send_ok:
        return {
            "task_id": task_id,
            "task_type": task.get("type", ""),
            "success": False,
            "timed_out": False,
            "elapsed_sec": 0,
            "error": "Failed to send prompt after 3 attempts",
        }

    # Step 4: Wait for SSE consumer to finish
    sse_thread.join(timeout=timeout + 60)
    summary = sse_result[0] or {
        "rounds": 0, "knight_reports": 0, "tool_calls": 0,
        "tool_errors": 0, "ask_user_count": 0, "text_chunks": 0,
        "timed_out": True, "completed": False, "events": [],
    }

    elapsed = time.time() - start_time

    # Step 4: Collect final metrics from /status
    try:
        resp = httpx.get(f"{base_url}/status", timeout=10.0)
        status_data = resp.json()
        metrics = status_data.get("metrics", {})
    except Exception as e:
        print(f"[eval] Failed to get status: {e}")
        metrics = {}

    success = summary["completed"] and not summary["timed_out"]
    # If we got at least one round_done and the agent produced text, consider it a success
    # even if we didn't have a clean idle timeout
    if summary["rounds"] > 0 and summary["text_chunks"] > 0 and not summary["timed_out"]:
        success = True

    result = {
        "task_id": task_id,
        "task_type": task.get("type", ""),
        "success": success,
        "timed_out": summary["timed_out"],
        "completed": summary["completed"],
        "elapsed_sec": round(elapsed, 1),
        "rounds": summary["rounds"],
        "tool_calls": metrics.get("total_tool_calls", summary["tool_calls"]),
        "tool_errors": metrics.get("tool_errors", summary["tool_errors"]),
        "ask_user_count": metrics.get("ask_user_count", summary["ask_user_count"]),
        "knight_reports": summary["knight_reports"],
        "user_messages": metrics.get("user_messages", 1),
        "rework_count": metrics.get("rework_count", 0),
    }

    status = "DONE" if success else ("TIMEOUT" if summary["timed_out"] else "PARTIAL")
    print(f"[eval] Task {task_id} {status}: elapsed={result['elapsed_sec']}s "
          f"tools={result['tool_calls']} errors={result['tool_errors']} "
          f"rounds={result['rounds']} knight={result['knight_reports']}")

    return result


# ---------------------------------------------------------------------------
# Scorecard
# ---------------------------------------------------------------------------

def compute_knight_score(baseline: list[dict], knight: list[dict]) -> dict:
    """Compute Knight Score comparing baseline vs knight-assisted results."""
    if not baseline or not knight:
        return {"knight_score": 0, "note": "insufficient data"}

    baseline_map = {r["task_id"]: r for r in baseline}
    knight_map = {r["task_id"]: r for r in knight}
    common_ids = set(baseline_map) & set(knight_map)
    if not common_ids:
        return {"knight_score": 0, "note": "no common tasks"}

    n = len(common_ids)

    b_success = sum(1 for tid in common_ids if baseline_map[tid].get("success"))
    k_success = sum(1 for tid in common_ids if knight_map[tid].get("success"))
    success_delta = (k_success - b_success) / n

    def avg_improvement(key, higher_is_better=False):
        vals = []
        for tid in common_ids:
            b_v = baseline_map[tid].get(key, 0)
            k_v = knight_map[tid].get(key, 0)
            base = max(b_v, 1)
            if higher_is_better:
                vals.append((k_v - b_v) / base)
            else:
                vals.append((b_v - k_v) / base)
        return sum(vals) / len(vals) if vals else 0

    time_avg = avg_improvement("elapsed_sec")
    tool_avg = avg_improvement("tool_calls")
    turn_avg = avg_improvement("rounds")

    k_reports = sum(knight_map[tid].get("knight_reports", 0) for tid in common_ids)
    skill_rate = min(k_reports / max(n, 1), 1.0)

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
                f"{'PASS' if r.get('success') else 'FAIL'} | "
                f"{r.get('elapsed_sec', 0):.1f}s | "
                f"{r.get('tool_calls', 0)} | "
                f"{r.get('tool_errors', 0)} | "
                f"{r.get('rounds', 0)} | "
                f"{r.get('knight_reports', 0)} |\n"
            )

        successes = [r for r in results if r.get("success")]
        failures = [r for r in results if not r.get("success")]
        timed_out = [r for r in results if r.get("timed_out")]

        f.write("\n## Summary\n\n")
        f.write(f"- **Passed:** {len(successes)}/{len(results)}\n")
        f.write(f"- **Failed:** {len(failures)}\n")
        f.write(f"- **Timed out:** {len(timed_out)}\n")
        f.write(f"- **Total tool calls:** {sum(r.get('tool_calls', 0) for r in results)}\n")
        f.write(f"- **Total errors:** {sum(r.get('tool_errors', 0) for r in results)}\n")
        f.write(f"- **Total elapsed:** {sum(r.get('elapsed_sec', 0) for r in results):.1f}s\n")

        if successes:
            f.write(f"- **Best task:** {successes[0]['task_id']} "
                    f"({successes[0].get('elapsed_sec', 0):.1f}s, "
                    f"{successes[0].get('tool_calls', 0)} tools)\n")


# ---------------------------------------------------------------------------
# Config generation
# ---------------------------------------------------------------------------

def generate_config(
    output_path: str,
    mode: str,  # "baseline" or "knight"
    port_file: str,
    metrics_path: str,
):
    """Generate a ggcode config YAML for the given mode.

    Uses the embedded default config as base (so vendor/system_prompt/vendors
    are all present), then patches in dummy adapter and optional knight settings.
    The minimal config approach fails because ggcode's daemon needs the full
    vendor registry and system_prompt to initialize properly.
    """
    # Try to load a reference config from a previous successful run.
    # Fallback to the embedded minimal default.
    ref_paths = [
        Path("/tmp/knight-teamclaw-smoke3/baseline-config.yaml"),
    ]
    base_yaml = None
    for rp in ref_paths:
        if rp.exists():
            base_yaml = rp.read_text()
            break

    if base_yaml:
        # Patch the existing config: update port_file, metrics_path, and knight settings
        import re

        # Sanitize hardcoded API keys — replace literal keys with ${VENDOR_API_KEY} env vars
        # This prevents leaking keys in generated configs
        env_var_map = {
            "AIzaSyDhtwWBmkqTrWPkwHiYtZ4ixDm9R2giIMQ": "${GOOGLE_API_KEY}",
            "sk-6h3rg0rAnwBoG60Ei2j8SgXkZILd7RPj9AnwCB6cYf2IP1xV": "${MOONSHOT_API_KEY}",
            "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": "${OPENROUTER_API_KEY}",
            "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": "${NVIDIA_API_KEY}",
            "38dfc49f4bde4b17a5d5b8a612687d87.3axFVc9M6KDD59Rn": "${ZAI_API_KEY}",
            "31c52998b0ed4b5b873fae684573cbfa.1kPfC0iWOMMxs2tE": "${ZAI_API_KEY}",
        }
        for literal_key, env_ref in env_var_map.items():
            base_yaml = base_yaml.replace(literal_key, env_ref)

        # Update port_file and metrics_path (absolute paths, no quotes needed)
        base_yaml = re.sub(
            r'port_file:.*',
            f'port_file: {port_file}',
            base_yaml,
        )
        base_yaml = re.sub(
            r'metrics_path:.*',
            f'metrics_path: {metrics_path}',
            base_yaml,
        )

        # Remove any existing knight section and add if needed
        base_yaml = re.sub(r'\nknight:.*?(?=\n[a-z]|\n\n|\Z)', '', base_yaml, flags=re.DOTALL)

        if mode == "knight":
            knight_block = (
                "\nknight:\n"
                "  enabled: true\n"
                "  trust_level: staged\n"
                "  model: glm-5-air\n"
                "  idle_delay_sec: 60\n"
                "  capabilities:\n"
                "    - skill_creation\n"
                "    - skill_validation\n"
                "    - regression_testing\n"
                "    - doc_sync\n"
            )
            # Insert knight block after the model line
            base_yaml = re.sub(
                r'(model:.*\n)',
                r'\1' + knight_block + "\n",
                base_yaml,
                count=1,
            )

        with open(output_path, "w") as f:
            f.write(base_yaml)
        return

    # Fallback: minimal config (works for projects with existing .ggcode config)
    config = {
        "vendor": "zai",
        "endpoint": "cn-coding-openai",
        "model": "glm-5-turbo",
    }

    if mode == "knight":
        config["knight"] = {
            "enabled": True,
            "trust_level": "staged",
            "model": "glm-5-air",
            "idle_delay_sec": 60,
            "capabilities": [
                "skill_creation",
                "skill_validation",
                "regression_testing",
                "doc_sync",
            ],
        }

    config["im"] = {
        "enabled": True,
        "adapters": {
            "dummy": {
                "enabled": True,
                "platform": "dummy",
                "extra": {
                    "listen_addr": "127.0.0.1:0",
                    "metrics_path": metrics_path,
                    "sse_buffer_size": "1024",
                    "port_file": port_file,
                },
            },
        },
    }

    with open(output_path, "w") as f:
        # Simple YAML generation without pyyaml dependency
        f.write(f"vendor: {config['vendor']}\n")
        f.write(f"endpoint: {config['endpoint']}\n")
        f.write(f"model: {config['model']}\n\n")

        if "knight" in config:
            k = config["knight"]
            f.write("knight:\n")
            f.write(f"  enabled: {str(k['enabled']).lower()}\n")
            f.write(f"  trust_level: {k['trust_level']}\n")
            f.write(f"  model: {k['model']}\n")
            f.write(f"  idle_delay_sec: {k['idle_delay_sec']}\n")
            f.write("  capabilities:\n")
            for cap in k["capabilities"]:
                f.write(f"    - {cap}\n")
            f.write("\n")

        im = config["im"]
        f.write("im:\n")
        f.write(f"  enabled: {str(im['enabled']).lower()}\n")
        f.write("  adapters:\n")
        f.write("    dummy:\n")
        f.write(f"      enabled: true\n")
        f.write(f"      platform: dummy\n")
        f.write("      extra:\n")
        for k, v in im["adapters"]["dummy"]["extra"].items():
            if isinstance(v, str):
                f.write(f'        {k}: "{v}"\n')
            else:
                f.write(f"        {k}: {v}\n")


# ---------------------------------------------------------------------------
# CSV helpers
# ---------------------------------------------------------------------------

CSV_FIELDS = [
    "run_id", "phase", "mode", "task_id", "task_type",
    "success", "timed_out", "elapsed_sec", "tool_calls",
    "tool_errors", "ask_user_count", "knight_reports",
    "user_messages", "rounds", "rework_count",
]


def write_csv_header(path: str):
    with open(path, "w", newline="") as f:
        w = csv.DictWriter(f, fieldnames=CSV_FIELDS)
        w.writeheader()


def append_csv_row(path: str, result: dict):
    with open(path, "a", newline="") as f:
        w = csv.DictWriter(f, fieldnames=CSV_FIELDS)
        w.writerow({k: result.get(k, "") for k in CSV_FIELDS})


# ---------------------------------------------------------------------------
# Run modes
# ---------------------------------------------------------------------------

def run_single_mode(
    mode: str,
    tasks: list[dict],
    llm: LLMClient | None,
    output_dir: str,
    run_id: str,
    auto: bool = False,
    working_dir: str | None = None,
) -> list[dict]:
    """Run evaluation in a single mode (baseline or knight).

    If auto=True, starts/stops daemon automatically.
    Returns list of result dicts.
    """
    out_dir = Path(output_dir)
    out_dir.mkdir(parents=True, exist_ok=True)

    csv_path = str(out_dir / f"{mode}.csv")
    log_dir = str(out_dir / "logs" / mode)
    Path(log_dir).mkdir(parents=True, exist_ok=True)

    proc = None
    base_url = ""
    shutdown_token = ""

    if auto:
        # Generate temp config — use absolute paths so daemon can find
        # port_file and metrics_path regardless of its working directory
        port_file = str((out_dir / f".{mode}-port").resolve())
        metrics_path = str((out_dir / f"{mode}-metrics.json").resolve())
        config_path = str((out_dir / f"{mode}-config.yaml").resolve())
        generate_config(config_path, mode, port_file, metrics_path)

        # Start daemon
        proc = start_daemon(config_path, port_file, working_dir or os.getcwd())
        base_url, shutdown_token = read_port_file(port_file)
        print(f"[eval] Daemon started: {base_url}")

        # Wait for healthz
        if not wait_for_healthz(base_url):
            print(f"[eval] ERROR: healthz failed", file=sys.stderr)
            stop_daemon(proc, base_url, shutdown_token)
            sys.exit(1)
    else:
        # Connect to existing daemon
        # base_url should be set by caller
        pass

    print(f"[eval] Running {len(tasks)} tasks in {mode} mode")

    write_csv_header(csv_path)
    results = []

    for task in tasks:
        log_file = f"{log_dir}/{run_id}_{task['id']}.ndjson"
        result = run_task(base_url, task, llm, log_file)
        result["run_id"] = run_id
        result["phase"] = mode
        result["mode"] = mode
        results.append(result)
        append_csv_row(csv_path, result)

    # Write scorecard
    score_path = str(out_dir / f"{mode}.scorecard.md")
    score = compute_knight_score(results, results)
    write_scorecard(score_path, results, score, mode)

    # Shutdown daemon if we started it
    if auto and proc:
        print(f"[eval] Shutting down daemon...")
        stop_daemon(proc, base_url, shutdown_token)
        print(f"[eval] Daemon stopped")

    return results


# ---------------------------------------------------------------------------
# Multi-round helpers
# ---------------------------------------------------------------------------

def git_reset_workdir(workdir: str):
    """Git-reset the workdir to clean state between rounds."""
    print(f"[eval] Git reset: {workdir}")
    try:
        subprocess.run(["git", "checkout", "."], cwd=workdir, capture_output=True, timeout=30)
        subprocess.run(["git", "clean", "-fd"], cwd=workdir, capture_output=True, timeout=30)
    except Exception as e:
        print(f"[eval] WARNING: git reset failed: {e}")


def write_ab_scorecard(score_path: str, tasks: list, baseline_results: list,
                       knight_results: list, score: dict):
    """Write an A/B comparison scorecard."""
    with open(score_path, "w") as f:
        f.write("# Knight A/B Comparison\n\n")
        f.write(f"**Date:** {time.strftime('%Y-%m-%d %H:%M')}\n")
        f.write(f"**Baseline tasks:** {len(baseline_results)}\n")
        f.write(f"**Knight tasks:** {len(knight_results)}\n\n")

        f.write("## Knight Score\n\n")
        for k, v in score.items():
            f.write(f"- **{k}:** {v}\n")

        f.write("\n## Head-to-Head\n\n")
        f.write("| Task | B.Success | B.Tools | B.Time | K.Success | K.Tools | K.Time | Delta |\n")
        f.write("|------|-----------|---------|--------|-----------|---------|--------|-------|\n")
        b_map = {r["task_id"]: r for r in baseline_results}
        k_map = {r["task_id"]: r for r in knight_results}
        for tid in [t["id"] for t in tasks]:
            b = b_map.get(tid, {})
            k = k_map.get(tid, {})
            b_s = "PASS" if b.get("success") else "FAIL"
            k_s = "PASS" if k.get("success") else "FAIL"
            delta = ""
            if b.get("tool_calls") and k.get("tool_calls"):
                d = b["tool_calls"] - k["tool_calls"]
                delta = f"{d:+d} tools"
            f.write(
                f"| {tid} | {b_s} | {b.get('tool_calls', 0)} | "
                f"{b.get('elapsed_sec', 0):.1f}s | {k_s} | "
                f"{k.get('tool_calls', 0)} | {k.get('elapsed_sec', 0):.1f}s | {delta} |\n"
            )


def write_final_report(path: str, all_results: list, duration_hours: float):
    """Write a comprehensive multi-round final report."""
    total_execs = sum(len(res) for _, _, res in all_results)
    total_pass = sum(1 for _, _, res in all_results for r in res if r.get("success"))

    with open(path, "w") as f:
        f.write("# Knight Multi-Round Evaluation Report\n\n")
        f.write(f"**Duration:** {duration_hours:.1f} hours\n")
        f.write(f"**Total task executions:** {total_execs}\n")
        f.write(f"**Overall success rate:** {total_pass}/{total_execs} "
                f"({100*total_pass/total_execs if total_execs else 0:.1f}%)\n")

        # Count rounds
        rounds_seen = set(rn for rn, _, _ in all_results)
        f.write(f"**Rounds completed:** {len(rounds_seen)}\n\n")

        # Per-task statistics across rounds
        for mode in ["baseline", "knight"]:
            mode_results = [r for _, m, res in all_results if m == mode for r in res]
            if not mode_results:
                continue

            # Group by task_id
            by_task = {}
            for r in mode_results:
                by_task.setdefault(r["task_id"], []).append(r)

            f.write(f"## Per-Task Statistics — {mode.title()}\n\n")
            f.write("| Task | Success Rate | Avg Time | Std Time | Avg Tools | Std Tools |\n")
            f.write("|------|-------------|----------|----------|-----------|----------|\n")

            for tid in sorted(by_task.keys()):
                runs = by_task[tid]
                sr = sum(1 for r in runs if r.get("success")) / len(runs)
                times = [r.get("elapsed_sec", 0) for r in runs]
                tools = [r.get("tool_calls", 0) for r in runs]
                avg_t = sum(times) / len(times) if times else 0
                std_t = (sum((t - avg_t)**2 for t in times) / len(times))**0.5 if len(times) > 1 else 0
                avg_tools = sum(tools) / len(tools) if tools else 0
                std_tools = (sum((t - avg_tools)**2 for t in tools) / len(tools))**0.5 if len(tools) > 1 else 0
                f.write(f"| {tid} | {sr:.2f} | {avg_t:.1f}s | {std_t:.1f}s | {avg_tools:.1f} | {std_tools:.1f} |\n")

            f.write("\n")

    print(f"[eval] Final report saved to {path}")


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(description="Knight evaluation orchestrator")
    parser.add_argument("--base-url", help="Dummy adapter base URL")
    parser.add_argument("--port-file", help="Path to dummy adapter port file")
    parser.add_argument("--output", required=True, help="Output directory for CSVs and scorecards")
    parser.add_argument("--mode", default="knight", choices=["baseline", "knight"],
                        help="Eval mode")
    parser.add_argument("--ab", action="store_true",
                        help="Run A/B comparison (baseline then knight)")
    parser.add_argument("--auto", action="store_true",
                        help="Auto-start/stop ggcode daemon")
    parser.add_argument("--run-id", default=time.strftime("%Y%m%d-%H%M%S"),
                        help="Run identifier")
    parser.add_argument("--tasks", help="Comma-separated task IDs (default: all)")
    parser.add_argument("--llm-model", default="glm-5-turbo", help="LLM model for prompt gen")
    parser.add_argument("--llm-base-url", help="LLM API base URL")
    parser.add_argument("--no-llm", action="store_true", help="Skip LLM, use raw descriptions")
    parser.add_argument("--timeout", type=float, default=600, help="Per-task timeout in seconds")
    parser.add_argument("--workdir", default=os.path.expanduser("~/ggai/eval-workbench"),
                        help="Working directory for the daemon")
    parser.add_argument("--templates", default="teamclaw",
                        help="Task template set: eval-workbench or teamclaw (default: teamclaw)")
    parser.add_argument("--rounds", type=int, default=1, help="Number of rounds (default: 1)")
    parser.add_argument("--duration", type=float, default=0,
                        help="Max duration in hours (0 = use --rounds). Mutually exclusive with rounds.")
    parser.add_argument("--no-reset", action="store_true",
                        help="Don't git-reset workdir between rounds")
    args = parser.parse_args()

    if not args.auto and not args.base_url and not args.port_file:
        parser.error("Either --auto, --base-url, or --port-file is required")

    # Select task template set
    template_sets = {
        "eval-workbench": TASKS,
    }
    try:
        from task_templates_teamclaw import TASKS as TEAMCLAW_TASKS
        template_sets["teamclaw"] = TEAMCLAW_TASKS
    except ImportError:
        pass

    all_templates = template_sets.get(args.templates, TASKS)

    # Select tasks
    if args.tasks:
        task_ids = [t.strip() for t in args.tasks.split(",")]
        tasks = [t for t in all_templates if t["id"] in task_ids]
    else:
        tasks = all_templates

    # Setup LLM client
    llm = None if args.no_llm else LLMClient(
        base_url=args.llm_base_url,
        model=args.llm_model,
    )

    working_dir = os.path.abspath(args.workdir)

    # Determine termination: duration-based or round-count-based
    use_duration = args.duration > 0
    if use_duration:
        max_rounds = 9999
        deadline = time.time() + args.duration * 3600
    else:
        max_rounds = args.rounds
        deadline = None

    # Print header
    mode_label = "A/B" if args.ab else args.mode
    duration_label = f"{args.duration}h" if use_duration else f"{max_rounds} rounds"
    print(f"\n{'#'*60}")
    print(f"  Knight Evaluation — {args.templates}")
    print(f"  Tasks: {len(tasks)} | Templates: {args.templates}")
    print(f"  Workdir: {working_dir}")
    print(f"  Output: {args.output}")
    print(f"  Mode: {mode_label}")
    print(f"  Duration: {duration_label}")
    print(f"{'#'*60}")

    # Multi-round loop
    all_results = []  # list of (round_num, mode, results_list)
    round_num = 0
    start_time = time.time()

    while round_num < max_rounds:
        round_num += 1
        elapsed_h = (time.time() - start_time) / 3600

        if deadline and time.time() >= deadline:
            print(f"\n[eval] Duration limit reached ({args.duration}h). Stopping.")
            break

        remaining = f"{args.duration - elapsed_h:.1f}h remaining" if deadline else f"round {round_num}/{max_rounds}"
        print(f"\n{'='*60}")
        print(f"  ROUND {round_num}/{max_rounds if not deadline else '∞'}")
        print(f"  Elapsed: {elapsed_h:.1f}h | {remaining}")
        print(f"{'='*60}")

        # Git reset between rounds (not on first round, unless --no-reset)
        if round_num > 1 and not args.no_reset:
            git_reset_workdir(working_dir)

        round_run_id = f"{args.run_id}-r{round_num:03d}"

        try:
            if args.ab:
                # A/B: run baseline then knight in each round
                baseline_results = run_single_mode(
                    "baseline", tasks, llm, args.output, round_run_id,
                    auto=args.auto, working_dir=working_dir,
                )
                all_results.append((round_num, "baseline", baseline_results))

                print(f"\n[eval] Baseline complete. Starting Knight mode...")
                time.sleep(2)

                knight_results = run_single_mode(
                    "knight", tasks, llm, args.output, round_run_id,
                    auto=args.auto, working_dir=working_dir,
                )
                all_results.append((round_num, "knight", knight_results))

                # Per-round comparison scorecard
                score = compute_knight_score(baseline_results, knight_results)
                score_path = str(Path(args.output) / f"round-{round_num:03d}-ab.scorecard.md")
                write_ab_scorecard(score_path, tasks, baseline_results, knight_results, score)

            elif args.auto:
                results = run_single_mode(
                    args.mode, tasks, llm, args.output, round_run_id,
                    auto=True, working_dir=working_dir,
                )
                all_results.append((round_num, args.mode, results))

            else:
                # Manual mode (single round only)
                if args.base_url:
                    base_url = args.base_url.rstrip("/")
                elif args.port_file:
                    base_url, _ = read_port_file(args.port_file)
                else:
                    parser.error("Either --base-url or --port-file is required")

                print(f"[eval] Connecting to {base_url}")
                if not wait_for_healthz(base_url):
                    print(f"[eval] ERROR: healthz failed", file=sys.stderr)
                    sys.exit(1)

                out_dir = Path(args.output)
                out_dir.mkdir(parents=True, exist_ok=True)
                csv_path = str(out_dir / f"{args.mode}.csv")
                log_dir = str(out_dir / "logs" / args.mode)
                Path(log_dir).mkdir(parents=True, exist_ok=True)

                write_csv_header(csv_path)
                results = []

                for task in tasks:
                    log_file = f"{log_dir}/{args.run_id}_{task['id']}.ndjson"
                    result = run_task(base_url, task, llm, log_file)
                    result["run_id"] = args.run_id
                    result["phase"] = args.mode
                    result["mode"] = args.mode
                    results.append(result)
                    append_csv_row(csv_path, result)

                score_path = str(out_dir / f"{args.mode}.scorecard.md")
                score = compute_knight_score(results, results)
                write_scorecard(score_path, results, score, args.mode)
                all_results.append((round_num, args.mode, results))
                break  # Manual mode is single-round

        except Exception as e:
            print(f"\n[eval] Round {round_num} FAILED: {e}")
            import traceback
            traceback.print_exc()
            continue

        round_mins = (time.time() - start_time) / 60
        passed = sum(1 for _, _, res in all_results for r in res if r.get("success"))
        total = sum(1 for _, _, res in all_results for r in res)
        print(f"\n[eval] Round {round_num} done: {passed}/{total} passed, {round_mins:.1f} min total")

    # Final aggregate report
    if all_results:
        duration_h = (time.time() - start_time) / 3600
        report_path = str(Path(args.output) / "final-report.md")
        write_final_report(report_path, all_results, duration_h)

        # If A/B, also write aggregate comparison
        if args.ab:
            agg_b = [r for _, m, res in all_results if m == "baseline" for r in res]
            agg_k = [r for _, m, res in all_results if m == "knight" for r in res]
            if agg_b and agg_k:
                agg_score = compute_knight_score(agg_b, agg_k)
                agg_path = str(Path(args.output) / "ab-comparison.scorecard.md")
                write_ab_scorecard(agg_path, tasks, agg_b, agg_k, agg_score)

    print(f"\n{'#'*60}")
    print(f"  EVALUATION COMPLETE")
    print(f"  Rounds: {round_num}")
    print(f"  Duration: {(time.time() - start_time) / 3600:.1f} hours")
    print(f"  Results: {args.output}")
    report_file = Path(args.output) / "final-report.md"
    if report_file.exists():
        print(f"  Report: {report_file}")
    print(f"{'#'*60}")


if __name__ == "__main__":
    main()
