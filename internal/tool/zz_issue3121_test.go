package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func execIssue3121Code(t *testing.T, code string) (fullText string, isError bool) {
	t.Helper()
	reg := NewRegistry()
	ce := CodeExecution{Registry: reg}
	result, err := ce.Execute(context.Background(), json.RawMessage(
		fmt.Sprintf(`{"code": %q}`, code),
	))
	if err != nil {
		fullText += err.Error()
	}
	fullText += result.Content
	return fullText, err != nil || result.IsError
}

func TestIssue3121_UnawaitedRejectionReported(t *testing.T) {
	fullText, isError := execIssue3121Code(t, `Promise.reject(new Error("boom-unawaited"));`)
	if !isError {
		t.Fatalf("expected error for un-awaited rejected promise, got success: %s", fullText)
	}
	if !strings.Contains(fullText, "unhandled promise rejection") {
		t.Errorf("expected 'unhandled promise rejection' diagnostic, got: %s", fullText)
	}
	if !strings.Contains(fullText, "boom-unawaited") {
		t.Errorf("expected rejection reason 'boom-unawaited' in error, got: %s", fullText)
	}
}

func TestIssue3121_AwaitedRejectionKeepsTryCatchPath(t *testing.T) {
	fullText, isError := execIssue3121Code(t, `await Promise.reject(new Error("boom-awaited"));`)
	if !isError {
		t.Fatalf("expected error for awaited rejection, got success: %s", fullText)
	}
	if !strings.Contains(fullText, "boom-awaited") {
		t.Errorf("expected reason 'boom-awaited' in error, got: %s", fullText)
	}
	if strings.Contains(fullText, "unhandled promise rejection") {
		t.Errorf("awaited rejection must use the try/catch path, not the unhandled diagnostic: %s", fullText)
	}
}

func TestIssue3121_CatchChainNotReported(t *testing.T) {
	fullText, isError := execIssue3121Code(t,
		`Promise.reject(new Error("boom-caught")).catch(function(e) { console.log("caught:", e.message); });`)
	if isError {
		t.Fatalf("expected success for .catch-handled rejection, got error: %s", fullText)
	}
	if !strings.Contains(fullText, "caught: boom-caught") {
		t.Errorf("expected catch handler output, got: %s", fullText)
	}
}

func TestIssue3121_ThenWithoutCatchReported(t *testing.T) {
	fullText, isError := execIssue3121Code(t,
		`Promise.reject(new Error("boom-then")).then(function(v) { return v; });`)
	if !isError {
		t.Fatalf("expected error for .then chain without catch, got success: %s", fullText)
	}
	if !strings.Contains(fullText, "boom-then") {
		t.Errorf("expected rejection reason 'boom-then' in error, got: %s", fullText)
	}
}
