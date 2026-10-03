package config

// r415 probe: session_time_budget validation branch mirrors
// session_token_budget's (negative rejected, zero and positive pass).

import (
	"strings"
	"testing"
	"time"
)

func TestR415_SessionTimeBudgetValidation(t *testing.T) {
	for _, c := range []*Config{
		{SessionTimeBudget: 0},
		{SessionTimeBudget: 10 * time.Minute},
	} {
		if err := c.Validate(); err != nil {
			t.Fatalf("Validate() = %v, want nil for budget %v", err, c.SessionTimeBudget)
		}
	}
	c := &Config{SessionTimeBudget: -time.Minute}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "session_time_budget") {
		t.Fatalf("negative budget: err = %v, want session_time_budget error", err)
	}
}
