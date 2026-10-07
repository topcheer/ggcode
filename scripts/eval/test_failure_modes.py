"""sa-124 tests: failure-mode taxonomy for reliability eval (arXiv:2602.16666).

Covers classify_failure_mode branch priority (infra > verify prefix > other),
_failure_mode_mix rates, and the CSV field surfacing the column.
"""

from run_eval import CSV_FIELDS, _failure_mode_mix, classify_failure_mode


def _row(**over):
    base = {
        "success": True, "error": "", "timed_out": False, "rounds": 3,
        "verify_status": "artifact_verified", "verify_detail": "ok",
    }
    base.update(over)
    return base


def test_success_has_no_mode():
    assert classify_failure_mode(_row()) == ""


def test_send_error_beats_timeout():
    r = _row(success=False, error="Failed to send", timed_out=True)
    assert classify_failure_mode(r) == "send_error"


def test_timeout_beats_verify():
    r = _row(success=False, timed_out=True, verify_detail="command exit 1: x")
    assert classify_failure_mode(r) == "timeout"


def test_no_response():
    r = _row(success=False, rounds=0)
    assert classify_failure_mode(r) == "no_response"


def test_verify_prefixes():
    cases = [
        ("artifact missing: out.txt", "verify_artifact_missing"),
        ("content missing 'x' in out.txt", "verify_content_missing"),
        ("command exit 1: boom", "verify_command_fail"),
        ("verify command timeout (60s)", "verify_timeout"),
        ("verify error: kaboom", "verify_error"),
        ("verify block has no artifact/command assertion", "verify_no_assertion"),
    ]
    for detail, want in cases:
        r = _row(success=False, verify_status="verify_failed", verify_detail=detail)
        assert classify_failure_mode(r) == want, detail


def test_verify_fallback_and_other():
    r = _row(success=False, verify_status="verify_failed", verify_detail="weird detail")
    assert classify_failure_mode(r) == "verify_other"
    r2 = _row(success=False, verify_status="text_only", verify_detail="")
    assert classify_failure_mode(r2) == "other"


def test_mix_rates_exclude_successes():
    rows = [
        _row(),
        _row(success=False, timed_out=True),
        _row(success=False, timed_out=True),
        _row(success=False, verify_detail="command exit 1: x",
             verify_status="verify_failed"),
    ]
    assert _failure_mode_mix(rows) == {"timeout": 0.5, "verify_command_fail": 0.25}


def test_mix_empty_and_all_pass():
    assert _failure_mode_mix([]) == {}
    assert _failure_mode_mix([_row(), _row()]) == {}


def test_csv_fields_carry_failure_mode():
    assert "failure_mode" in CSV_FIELDS
