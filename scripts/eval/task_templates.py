"""Evaluation task templates for Knight automated evaluation.

Each task is a dict with:
  - id: unique task identifier
  - type: one of code_edit, test_debug, docs
  - description: template description (LLM will concretize this)
  - expect_ask_user: whether this task typically triggers ask_user
  - expect_knight: whether this task typically triggers Knight analysis
  - timeout_sec: per-task deadline in seconds
"""

TASKS = [
    {
        "id": "task-01",
        "type": "code_edit",
        "description": (
            "Add a new format extractor for JSON Lines (.jsonl) files "
            "in internal/extract. It should parse each line as a JSON object, "
            "extract 'title', 'body', and 'metadata' fields, and return them "
            "as ExtractedItem structs. Include error handling for malformed lines."
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
            "replace ad-hoc error checks with a centralized errorSentinel pattern. "
            "The sentinel should track consecutive failures and auto-disable the "
            "emitter after 10 consecutive errors, with a log message."
        ),
        "expect_ask_user": False,
        "expect_knight": False,
        "timeout_sec": 600,
    },
    {
        "id": "task-03",
        "type": "code_edit",
        "description": (
            "Add a new scheduled task type 'git_sync' to the Knight scheduler "
            "that runs 'git fetch --prune' and 'git branch -vv' to detect "
            "stale local branches. Include it in the default capability list "
            "and add a config field 'git_sync_interval_sec'."
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
            "to internal/config. Validate it is between 100 and 100000. "
            "Use it in emitter.go to truncate outbound text messages with a "
            "'... (truncated)' suffix when they exceed the limit."
        ),
        "expect_ask_user": False,
        "expect_knight": False,
        "timeout_sec": 600,
    },
    {
        "id": "task-05",
        "type": "test_debug",
        "description": (
            "Run 'go test ./internal/knight/...' and fix any failing tests. "
            "If all tests pass, add a new test for the Knight scheduler that "
            "verifies tasks are not executed more than once per interval."
        ),
        "expect_ask_user": False,
        "expect_knight": False,
        "timeout_sec": 600,
    },
    {
        "id": "task-06",
        "type": "test_debug",
        "description": (
            "Review internal/im/runtime_test.go for potential race conditions. "
            "If any are found, fix them by adding proper synchronization. "
            "Run the tests with -race flag to verify."
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
            "the original bug scenario and verify the fix."
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
            "CLI commands. Run 'ggcode --help' and 'ggcode daemon --help' to "
            "check. Fix any discrepancies found."
        ),
        "expect_ask_user": False,
        "expect_knight": False,
        "timeout_sec": 300,
    },
    {
        "id": "task-09",
        "type": "docs",
        "description": (
            "Write package-level doc comments for internal/extract package. "
            "Include overview of supported formats, the Extract function signature, "
            "and a usage example."
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
            "check that the description accurately reflects what the skill does. "
            "Update any stale or misleading descriptions."
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
            "adapters (QQ, Telegram, Discord, Feishu, DingTalk, Slack, Dummy) "
            "to use the new pattern."
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
            "how threads should work before implementing."
        ),
        "expect_ask_user": True,
        "expect_knight": False,
        "timeout_sec": 600,
    },
    {
        "id": "task-13",
        "type": "code_edit",
        "description": (
            "Add a new IM adapter for Microsoft Teams. Include the basic "
            "adapter structure with webhook-based message receiving. After "
            "completing this task, wait idle for at least 60 seconds to "
            "observe if Knight performs any idle analysis."
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
