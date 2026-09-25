# Tool Tapes: Deterministic Record/Replay and Cut-Point Verification

Tool tapes implement the "VCR/cassette" pattern for ggcode's tool layer,
plus Chronicle-style cut-point verification (arXiv:2609.20625) that turns a
recorded agent incident into an offline regression test.

## Recording and replaying a session

Set `GGCODE_TOOL_TAPE` before starting an interactive or piped session:

```bash
# Record: every tool call executes for real and is persisted to the tape.
GGCODE_TOOL_TAPE=record:/tmp/incident.tape.json ggcode

# Replay: real tools are never invoked; results are served from the tape
# by (tool name, canonical input hash), FIFO for repeated inputs.
GGCODE_TOOL_TAPE=replay:/tmp/incident.tape.json ggcode
```

A tape captured up to a crash still contains every boundary recorded so
far (the file is rewritten atomically after each call), so it can be
attached to a bug report as-is.

## Verifying a tape against current code (`ggcode tape verify`)

```bash
ggcode tape verify /tmp/incident.tape.json
```

The verify command walks the recorded boundaries in order and — by default —
re-executes each one **live with the current tool code**, comparing results
against the record:

- `match` — live behavior reproduced the recorded result
- `diverged` — live behavior differs (content or error class); **fails the run (exit 1)**
- `cut` — below `--cut N`; served from the record, never executed
- `skipped-unsafe` — mutating tool not re-executed (safe default)
- `no-tool` — the recorded tool is not registered in this build; **fails the run**

Safety: only read-only tools (`read_file`, `grep`, `git_status`, …) are
re-executed live. Pass `--include-unsafe` to also re-execute mutating
tools — only in a workspace that tolerates the recorded side effects.

Options:

| Flag | Meaning |
|------|---------|
| `--cut N` | serve the first N boundaries from the record |
| `--include-unsafe` | also execute mutating tools live |
| `--workdir DIR` | working directory for tool execution (default: cwd) |
| `--timeout D` | overall timeout for live executions (default 10m) |

## Typical workflows

Turn a bug report into a regression test (runs in CI, zero model calls):

```bash
ggcode tape verify incident.tape.json   # exit 1 = your change alters recorded behavior
```

Test a fix only against the tail of a failed run:

```bash
ggcode tape verify --cut 20 incident.tape.json
```

Inspect what a tape contains before verifying:

```bash
ggcode tape info incident.tape.json
```

Comparison details: error parity is outcome-class based (recorded error +
live error = match, since error wording legitimately shifts across builds);
successful results are compared after CRLF/trailing-whitespace
normalization. Verification never consumes tape slots, so a verified tape
remains fully replayable.
