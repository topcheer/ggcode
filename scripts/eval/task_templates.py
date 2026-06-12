"""Evaluation task templates for Knight automated evaluation.

Each task targets the eval-workbench project at ~/ggai/eval-workbench/.
Tasks are designed with intentional defects for the agent to discover and fix.

Task types:
  - code_edit: agent writes/modifies code
  - test_debug: agent fixes bugs found by tests
  - docs: agent writes/fixes documentation
"""

TASKS = [
    {
        "id": "task-01",
        "type": "code_edit",
        "description": (
            "Add a new format extractor for JSON Lines (.jsonl) files "
            "in internal/extract/. It should parse each line as a JSON object, "
            "extract 'title', 'body', and 'metadata' fields, and return them "
            "as extract.Item structs. Register it in the extractor registry as 'jsonl'. "
            "Include error handling for malformed lines (skip them with a warning). "
            "Add a test in extract_test.go that verifies jsonl extraction works."
        ),
        "expect_ask_user": False,
        "expect_knight": False,
        "timeout_sec": 600,
    },
    {
        "id": "task-02",
        "type": "code_edit",
        "description": (
            "Refactor error handling in internal/im/emitter.go: "
            "replace the scattered fmt.Printf error checks with a centralized errorSentinel pattern. "
            "The sentinel should track consecutive failures per sink and auto-disable "
            "a sink after 10 consecutive errors, logging a message when it does. "
            "Reset the counter on a successful send."
        ),
        "expect_ask_user": False,
        "expect_knight": False,
        "timeout_sec": 600,
    },
    {
        "id": "task-03",
        "type": "code_edit",
        "description": (
            "Add a new scheduled task type 'health_check' to internal/scheduler/ "
            "that pings localhost on the configured server port. Add a 'health_check' "
            "entry to RegisterDefaults() with a 30-second interval. "
            "Add a config field 'health_check_port' to SchedulerConfig in "
            "internal/config/config.go. Add a test that verifies the health_check "
            "task runs and reports success/failure."
        ),
        "expect_ask_user": False,
        "expect_knight": True,
        "timeout_sec": 600,
    },
    {
        "id": "task-04",
        "type": "code_edit",
        "description": (
            "Add a new config field 'im.max_message_length' (default 4096) "
            "to internal/config/config.go in the IMConfig struct. "
            "Validate it is between 100 and 100000 in the Validate function. "
            "Use it in internal/im/emitter.go to truncate outbound message content "
            "with a '... (truncated)' suffix when it exceeds the limit."
        ),
        "expect_ask_user": False,
        "expect_knight": False,
        "timeout_sec": 600,
    },
    {
        "id": "task-05",
        "type": "test_debug",
        "description": (
            "Run 'go test ./internal/extract/...' and fix any failing tests. "
            "The PDF extractor has a bug that causes a panic on empty input — "
            "fix it to return a proper error instead. After fixing, run the tests "
            "again to verify all pass. Do not change the test expectations, "
            "only fix the implementation."
        ),
        "expect_ask_user": False,
        "expect_knight": False,
        "timeout_sec": 600,
    },
    {
        "id": "task-06",
        "type": "test_debug",
        "description": (
            "Run 'go test -race ./internal/im/...' and fix any race conditions found. "
            "The dummy adapter's messages slice is accessed from multiple goroutines "
            "without synchronization. Add proper locking (mutex or sync-safe collection) "
            "to make the code race-free. Re-run with -race flag to verify."
        ),
        "expect_ask_user": False,
        "expect_knight": False,
        "timeout_sec": 600,
    },
    {
        "id": "task-07",
        "type": "test_debug",
        "description": (
            "Look at recent bug fixes from 'git log --oneline -20' and write "
            "regression tests for at least 3 of them. Each test should reproduce "
            "the original bug scenario and verify the fix works. "
            "Place the tests in a new file internal/regression_test.go."
        ),
        "expect_ask_user": False,
        "expect_knight": False,
        "timeout_sec": 600,
    },
    {
        "id": "task-08",
        "type": "docs",
        "description": (
            "Verify that all command references in README.md match the actual "
            "CLI commands. Build the binary with 'go build -o bin/eval-workbench .' "
            "and run 'bin/eval-workbench --help' to check actual commands. "
            "Fix any discrepancies found in README.md."
        ),
        "expect_ask_user": False,
        "expect_knight": False,
        "timeout_sec": 300,
    },
    {
        "id": "task-09",
        "type": "docs",
        "description": (
            "Write a comprehensive package-level doc comment for internal/extract. "
            "Include an overview of supported formats (txt, csv, pdf), "
            "the Extractor interface, the Register/Get/ExtractAll API, "
            "and a Go usage example showing how to extract text from a file."
        ),
        "expect_ask_user": False,
        "expect_knight": False,
        "timeout_sec": 300,
    },
    {
        "id": "task-10",
        "type": "docs",
        "description": (
            "Audit all skill files in .ggcode/skills/ directory. For each skill, "
            "check that the description accurately reflects what the skill actually does. "
            "Update any misleading or inaccurate descriptions to match reality."
        ),
        "expect_ask_user": False,
        "expect_knight": True,
        "timeout_sec": 300,
    },
    {
        "id": "task-11",
        "type": "code_edit",
        "description": (
            "Refactor the IM adapter registration in internal/im/adapters.go "
            "to use a registry pattern instead of a switch statement. Each adapter "
            "should self-register via an init() function. Update all existing "
            "adapters (Dummy, Webhook) to use the new pattern. "
            "What approach would you recommend for the registration mechanism?"
        ),
        "expect_ask_user": True,
        "expect_knight": False,
        "timeout_sec": 600,
    },
    {
        "id": "task-12",
        "type": "code_edit",
        "description": (
            "Add support for message threading in the IM system. The design "
            "is intentionally vague — you should ask clarifying questions about "
            "how threads should work before implementing. Consider how threading "
            "affects the Message struct, the Sink interface, and adapters."
        ),
        "expect_ask_user": True,
        "expect_knight": False,
        "timeout_sec": 600,
    },
    {
        "id": "task-13",
        "type": "code_edit",
        "description": (
            "Add a new IM adapter for Email (SMTP). It should implement the Sink "
            "interface, send messages via SMTP with configurable host/port/sender. "
            "Add basic config fields in config.go. After completing this task, "
            "remain idle for at least 60 seconds to observe if Knight performs "
            "any idle-time analysis."
        ),
        "expect_ask_user": True,
        "expect_knight": True,
        "timeout_sec": 900,
    },
]


def get_task(task_id: str) -> dict | None:
    """Return a task by ID, or None if not found."""
    for t in TASKS:
        if t["id"] == task_id:
            return t
    return None


def get_tasks_by_type(task_type: str) -> list[dict]:
    """Return all tasks of a given type."""
    return [t for t in TASKS if t["type"] == task_type]
