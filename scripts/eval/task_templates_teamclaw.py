"""Evaluation task templates for Knight automated evaluation — TeamClaw project.

Targets the real TeamClaw codebase at ~/ggai/teamclaw/.
Tasks are designed to test agent capability on a production TypeScript/Node.js
project with complex architecture, real business logic, and meaningful scale.

Task types:
  - code_edit: agent writes/modifies code
  - test_debug: agent fixes bugs found by tests
  - docs: agent writes/fixes documentation
"""

TASKS = [
    # ── code_edit: real feature implementation ──────────────────────────
    {
        "id": "task-01",
        "type": "code_edit",
        "description": (
            "Add a new role 'data-engineer' to src/src/roles.ts. "
            "The role should have id 'data-engineer', label 'Data Engineer', "
            "icon '📊', description 'Data pipeline design, ETL, data modeling', "
            "capabilities ['data-modeling', 'etl-pipeline', 'data-quality', 'sql-optimization'], "
            "recommendedSkills ['find-skills'], a systemPrompt describing the role, "
            "and suggestedNextRoles ['developer', 'infra-engineer']. "
            "Also add 'data-engineer' to the VALID_ROLES array in src/src/types.ts. "
            "Run 'node tests/test-role-registry.mjs' to verify."
        ),
        "expect_ask_user": False,
        "expect_knight": False,
        "timeout_sec": 600,
    },
    {
        "id": "task-02",
        "type": "code_edit",
        "description": (
            "In src/src/worker/tools.ts, add a new worker tool 'teamclaw_request_review' "
            "that lets a worker request a code review from another team member. "
            "Parameters: targetRole (string), filePath (string), description (string), "
            "priority (optional, enum low/medium/high). "
            "The tool should POST to /api/v1/messages as a review-request. "
            "Follow the existing tool pattern (teamclaw_ask_peer) for structure. "
            "Use TypeBox for the schema."
        ),
        "expect_ask_user": False,
        "expect_knight": False,
        "timeout_sec": 600,
    },
    {
        "id": "task-03",
        "type": "code_edit",
        "description": (
            "In src/src/controller/http-server.ts, add a new REST endpoint "
            "GET /api/v1/stats that returns team statistics: total workers, "
            "workers by status (idle/busy/offline), total tasks, tasks by status, "
            "average task duration, and uptime. Create a helper function "
            "computeTeamStats(state: TeamState) to calculate these. "
            "Add the endpoint near the existing /api/v1/workers and /api/v1/tasks endpoints."
        ),
        "expect_ask_user": False,
        "expect_knight": True,
        "timeout_sec": 600,
    },
    {
        "id": "task-04",
        "type": "code_edit",
        "description": (
            "In src/src/types.ts, add a new config field 'maxConcurrentTasks' "
            "(default 5, min 1, max 50) to PluginConfig. "
            "Update parsePluginConfig() to parse and validate it. "
            "Also update src/src/config.ts buildConfigSchema() to include the new field "
            "with proper JSON Schema and UI hint. "
            "Use it in src/src/task-executor.ts to limit how many tasks "
            "can be in 'in_progress' status simultaneously — if at capacity, "
            "new tasks stay in 'pending' until a slot opens."
        ),
        "expect_ask_user": False,
        "expect_knight": False,
        "timeout_sec": 600,
    },
    {
        "id": "task-05",
        "type": "code_edit",
        "description": (
            "In src/src/interaction-contracts.ts, add support for a new "
            "result deliverable kind 'test-suite'. It should have fields: "
            "kind ('test-suite'), path (string), framework (string, e.g. 'jest', 'vitest', 'pytest'), "
            "coverage (optional number 0-100), passed (number), failed (number). "
            "Add it to the RESULT_DELIVERABLE_KINDS set and create a normalizer function "
            "normalizeTestSuiteDeliverable(). Update the WorkerTaskResultDeliverable type "
            "in src/src/types.ts accordingly."
        ),
        "expect_ask_user": False,
        "expect_knight": False,
        "timeout_sec": 600,
    },
    {
        "id": "task-06",
        "type": "code_edit",
        "description": (
            "Refactor src/src/state.ts to add a state version field. "
            "When loading state from disk, check the version. If it's missing or "
            "older than the current version, run a migration function that adds "
            "any missing fields with defaults. Create a STATE_VERSION constant (set to 2), "
            "and a migrateState(state: unknown) function. This should handle states "
            "saved by version 1 (no version field) by adding default values for "
            "any fields introduced in version 2 (like 'provisioning.readiness'). "
            "Write backward-compatible code that doesn't break existing installations."
        ),
        "expect_ask_user": False,
        "expect_knight": True,
        "timeout_sec": 600,
    },
    {
        "id": "task-07",
        "type": "code_edit",
        "description": (
            "In src/src/controller/controller-tools.ts, the EXECUTION_READY_BLOCKERS "
            "array checks for dependency keywords. Add 3 more blocker patterns: "
            "1) detect '前提' in Chinese (means prerequisite), "
            "2) detect 'blocked by' or 'blocked-by' in English, "
            "3) detect '需要.*先完成' in Chinese (means need X to complete first). "
            "Also add corresponding unit-style assertions in tests/test-controller-intake.mjs "
            "to verify these patterns are detected."
        ),
        "expect_ask_user": False,
        "expect_knight": False,
        "timeout_sec": 600,
    },
    # ── test_debug: find and fix real bugs ──────────────────────────────
    {
        "id": "task-08",
        "type": "test_debug",
        "description": (
            "Run 'node tests/test-worker-contracts.mjs' and 'node tests/test-controller-intake.mjs' "
            "and 'node tests/test-role-registry.mjs'. Fix any failing assertions. "
            "These tests check that source files contain expected patterns — "
            "if any are failing, read the test to understand what pattern it expects, "
            "then fix the source code (NOT the test) to match. "
            "Re-run all three test files to confirm they pass."
        ),
        "expect_ask_user": False,
        "expect_knight": False,
        "timeout_sec": 600,
    },
    {
        "id": "task-09",
        "type": "test_debug",
        "description": (
            "There's a potential bug in src/src/task-executor.ts: if a worker "
            "disconnects mid-task, the task may stay in 'in_progress' status forever. "
            "Find the task timeout logic and verify it handles this case. "
            "If there's no timeout handling, add a configurable task timeout "
            "(default 30 minutes) that moves stuck tasks to 'failed' status "
            "with a 'timeout' reason. Add logging when this happens."
        ),
        "expect_ask_user": False,
        "expect_knight": False,
        "timeout_sec": 600,
    },
    {
        "id": "task-10",
        "type": "test_debug",
        "description": (
            "Look at recent commits with 'git log --oneline -20' in this project. "
            "Find 3 bug-fix commits and write regression tests for them. "
            "Place tests in tests/test-regression.mjs using the same "
            "node:assert/strict pattern as the existing tests. "
            "Each test should verify the specific bug scenario doesn't regress."
        ),
        "expect_ask_user": False,
        "expect_knight": False,
        "timeout_sec": 600,
    },
    # ── docs: documentation accuracy ────────────────────────────────────
    {
        "id": "task-11",
        "type": "docs",
        "description": (
            "Compare DESIGN.md against the actual source code. "
            "Check if all 10 roles listed in src/src/roles.ts are documented in DESIGN.md. "
            "Check if the REST API endpoints documented in DESIGN.md match what's actually "
            "implemented in src/src/controller/http-server.ts (look for app.get/app.post/app.put/app.delete). "
            "Fix any discrepancies — update DESIGN.md to match reality."
        ),
        "expect_ask_user": False,
        "expect_knight": False,
        "timeout_sec": 300,
    },
    {
        "id": "task-12",
        "type": "docs",
        "description": (
            "Write a comprehensive JSDoc comment for the normalizeWorkerTaskResultContract "
            "function in src/src/interaction-contracts.ts. Document all parameters it normalizes, "
            "the default values applied, and edge cases handled. "
            "Also add a brief doc comment to the WORKER_TASK_RESULT_OUTCOMES and "
            "RESULT_DELIVERABLE_KINDS constants explaining what they validate."
        ),
        "expect_ask_user": False,
        "expect_knight": False,
        "timeout_sec": 300,
    },
    {
        "id": "task-13",
        "type": "docs",
        "description": (
            "Audit src/skills/ directory. For each skill, check if the SKILL.md "
            "description accurately describes what the skill does. "
            "Also check if the skill references files or paths that actually exist. "
            "Update any inaccurate descriptions or broken references."
        ),
        "expect_ask_user": False,
        "expect_knight": True,
        "timeout_sec": 300,
    },
    # ── code_edit: vague/complex requirements (ask_user expected) ───────
    {
        "id": "task-14",
        "type": "code_edit",
        "description": (
            "Design and implement a 'task dependency' system for TeamClaw. "
            "Tasks should be able to declare dependencies on other tasks, "
            "and the controller should not assign a task until its dependencies "
            "are completed. Think about how this affects TaskInfo, the task router, "
            "and the controller tools. The design is intentionally vague — "
            "you should ask clarifying questions before implementing."
        ),
        "expect_ask_user": True,
        "expect_knight": False,
        "timeout_sec": 900,
    },
    {
        "id": "task-15",
        "type": "code_edit",
        "description": (
            "Add support for 'task priority' in TeamClaw. "
            "This should affect task routing order — higher priority tasks "
            "should be assigned before lower priority ones. "
            "Consider how priority interacts with the existing FIFO task assignment. "
            "Add a priority field to TaskInfo, update the task router, "
            "and add it to the create_task controller tool parameters. "
            "After completing this task, remain idle for 60 seconds to observe "
            "if Knight performs any idle-time analysis."
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
