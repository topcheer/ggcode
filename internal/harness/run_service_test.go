package harness

import (
	"context"
	"testing"
	"time"
)

func TestRunService_CTAGeneration(t *testing.T) {
	tests := []struct {
		name    string
		summary *RunSummary
		err     error
		wantCTA CTAAction
		wantMsg string
	}{
		{
			name: "completed with verification passed and pending review",
			summary: &RunSummary{
				Task: &Task{
					ID:                 "t-1",
					Status:             TaskCompleted,
					VerificationStatus: VerificationPassed,
					ReviewStatus:       ReviewPending,
				},
			},
			wantCTA: CTAReview,
			wantMsg: "review",
		},
		{
			name: "completed and approved but not promoted",
			summary: &RunSummary{
				Task: &Task{
					ID:                 "t-2",
					Status:             TaskCompleted,
					VerificationStatus: VerificationPassed,
					ReviewStatus:       ReviewApproved,
					PromotionStatus:    "",
				},
			},
			wantCTA: CTAPromote,
			wantMsg: "promote",
		},
		{
			name: "completed and promoted",
			summary: &RunSummary{
				Task: &Task{
					ID:              "t-3",
					Status:          TaskCompleted,
					ReviewStatus:    ReviewApproved,
					PromotionStatus: PromotionApplied,
				},
			},
			wantCTA: CTANone,
		},
		{
			name: "failed task",
			summary: &RunSummary{
				Task: &Task{
					ID:      "t-4",
					Status:  TaskFailed,
					LogPath: "/tmp/harness/t-4.log",
				},
			},
			wantCTA: CTAInspectLog,
			wantMsg: "t-4.log",
		},
		{
			name: "failed task without log",
			summary: &RunSummary{
				Task: &Task{
					ID:     "t-5",
					Status: TaskFailed,
				},
			},
			wantCTA: CTARerun,
			wantMsg: "rerun",
		},
		{
			name:    "error without summary",
			err:     context.DeadlineExceeded,
			wantCTA: CTARerun,
			wantMsg: "failed",
		},
		{
			name:    "nil summary and nil error",
			wantCTA: CTANone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cta, msg := generateCTA(tt.summary, tt.err)
			if cta != tt.wantCTA {
				t.Errorf("CTA = %q, want %q", cta, tt.wantCTA)
			}
			if tt.wantMsg != "" && msg == "" {
				t.Error("expected non-empty CTA message")
			}
			if tt.wantMsg != "" && !contains(msg, tt.wantMsg) {
				t.Errorf("CTA message = %q, want to contain %q", msg, tt.wantMsg)
			}
		})
	}
}

func TestFormatCTA(t *testing.T) {
	tests := []struct {
		result *RunServiceResult
		want   string
	}{
		{nil, ""},
		{&RunServiceResult{CTA: CTANone}, ""},
		{&RunServiceResult{CTA: CTAReview, CTAMessage: "Run /harness review t-1"}, "Next: Run /harness review t-1"},
	}
	for _, tt := range tests {
		got := FormatCTA(tt.result)
		if tt.want == "" {
			if got != "" {
				t.Errorf("FormatCTA() = %q, want empty", got)
			}
		} else if !contains(got, tt.want) {
			t.Errorf("FormatCTA() = %q, want to contain %q", got, tt.want)
		}
	}
}

func TestFormatRunServiceResult(t *testing.T) {
	// With CTA
	result := &RunServiceResult{
		Summary: &RunSummary{
			Task: &Task{
				ID:                 "t-1",
				Status:             TaskCompleted,
				VerificationStatus: VerificationPassed,
				ReviewStatus:       ReviewPending,
			},
		},
		CTA:        CTAReview,
		CTAMessage: "Run /harness review t-1",
	}
	got := FormatRunServiceResult(result)
	if !contains(got, "t-1") {
		t.Error("should contain task ID")
	}
	if !contains(got, "review") {
		t.Error("should contain review CTA")
	}

	// Nil
	if FormatRunServiceResult(nil) != "No harness run executed." {
		t.Error("nil should return default message")
	}
}

func TestRunService_Timeout(t *testing.T) {
	svc := NewRunService()
	if svc.Timeout != 30*time.Minute {
		t.Errorf("default timeout = %v, want 30m", svc.Timeout)
	}
}

// contains checks if s contains substr (case-sensitive).
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
