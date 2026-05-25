package main

import (
	"testing"
	"time"
)

func TestRelayTraceLoggerLogsHeadAndTailWithSuppressedCount(t *testing.T) {
	var lines []string
	logger := newRelayTraceLoggerWithSink(50*time.Millisecond, func(line string) {
		lines = append(lines, line)
	})
	now := time.Unix(0, 0)

	logger.logAt(now, "k", "head")
	logger.logAt(now.Add(10*time.Millisecond), "k", "middle")
	logger.logAt(now.Add(20*time.Millisecond), "k", "tail")
	logger.flushAgedAt(now.Add(100 * time.Millisecond))

	if len(lines) != 2 {
		t.Fatalf("expected head and tail logs, got %d: %+v", len(lines), lines)
	}
	if lines[0] != "head" {
		t.Fatalf("head log = %q, want %q", lines[0], "head")
	}
	if lines[1] != "tail tail=true suppressed=1" {
		t.Fatalf("tail log = %q, want %q", lines[1], "tail tail=true suppressed=1")
	}
}

func TestRelayTraceLoggerLogsSecondEventAsTail(t *testing.T) {
	var lines []string
	logger := newRelayTraceLoggerWithSink(50*time.Millisecond, func(line string) {
		lines = append(lines, line)
	})
	now := time.Unix(0, 0)

	logger.logAt(now, "k", "first")
	logger.logAt(now.Add(10*time.Millisecond), "k", "second")
	logger.flushAgedAt(now.Add(100 * time.Millisecond))

	if len(lines) != 2 {
		t.Fatalf("expected head and tail logs, got %d: %+v", len(lines), lines)
	}
	if lines[1] != "second tail=true suppressed=0" {
		t.Fatalf("tail log = %q, want %q", lines[1], "second tail=true suppressed=0")
	}
}
