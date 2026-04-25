package tool

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestSleep_Basic(t *testing.T) {
	s := SleepTool{}
	input, _ := json.Marshal(map[string]interface{}{
		"seconds": 0,
	})
	result, err := s.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
}

func TestSleep_WithMilliseconds(t *testing.T) {
	s := SleepTool{}
	start := time.Now()
	input, _ := json.Marshal(map[string]interface{}{
		"seconds":      0,
		"milliseconds": 100,
	})
	result, err := s.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	elapsed := time.Since(start)
	if elapsed < 90*time.Millisecond {
		t.Errorf("expected ~100ms sleep, got %v", elapsed)
	}
}

func TestSleep_ExceedsMax(t *testing.T) {
	s := SleepTool{}
	input, _ := json.Marshal(map[string]interface{}{
		"seconds": 1900,
	})
	result, err := s.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for exceeding max sleep")
	}
}

func TestSleep_NegativeSeconds(t *testing.T) {
	s := SleepTool{}
	input, _ := json.Marshal(map[string]interface{}{
		"seconds": -1,
	})
	result, err := s.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for negative seconds")
	}
}

func TestSleep_ContextCancellation(t *testing.T) {
	s := SleepTool{}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	input, _ := json.Marshal(map[string]interface{}{
		"seconds": 120,
	})
	result, err := s.Execute(ctx, input)
	if err == nil {
		t.Error("expected context cancelled error")
	}
	if !containsAny(result.Content, "interrupted") {
		t.Logf("result: %s", result.Content)
	}
}
