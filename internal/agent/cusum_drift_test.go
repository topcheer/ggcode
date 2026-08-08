package agent

import (
	"strings"
	"testing"
)

func TestCusumDrift_WarmupOnly(t *testing.T) {
	c := newCusumDriftState()
	// During warmup (first warmupPeriod*checkInterval calls), no alert.
	for i := 0; i < 15; i++ {
		msg := c.record(cusumRecord{isRead: true, tokenDelta: 1000})
		if msg != "" {
			t.Fatalf("expected no alert during warmup at call %d, got: %s", i, msg)
		}
	}
}

func TestCusumDrift_NoDrift(t *testing.T) {
	c := newCusumDriftState()
	// Consistent behavior should not trigger alerts.
	for i := 0; i < 60; i++ {
		msg := c.record(cusumRecord{isRead: true, tokenDelta: 500})
		if msg != "" {
			t.Fatalf("expected no alert with stable behavior at call %d, got: %s", i, msg)
		}
	}
}

func TestCusumDrift_ErrorRateEscalation(t *testing.T) {
	c := newCusumDriftState()
	// Warmup with low error rate.
	for i := 0; i < 15; i++ {
		c.record(cusumRecord{tokenDelta: 500})
	}
	// Gradually escalate error rate - individual calls won't trip
	// threshold-based detectors, but CUSUM should catch the trend.
	var alertFound bool
	for i := 0; i < 60; i++ {
		msg := c.record(cusumRecord{failed: true, tokenDelta: 500})
		if msg != "" && strings.Contains(msg, "CUSUM Drift") {
			alertFound = true
			break
		}
	}
	if !alertFound {
		t.Fatal("expected CUSUM drift alert for escalating error rate")
	}
}

func TestCusumDrift_ReadHeavyShift(t *testing.T) {
	c := newCusumDriftState()
	// Warmup with balanced read/write.
	for i := 0; i < 15; i++ {
		if i%2 == 0 {
			c.record(cusumRecord{isRead: true, tokenDelta: 500})
		} else {
			c.record(cusumRecord{isWrite: true, tokenDelta: 500})
		}
	}
	// Shift to entirely read-heavy (exploratory drift).
	var alertFound bool
	for i := 0; i < 60; i++ {
		msg := c.record(cusumRecord{isRead: true, tokenDelta: 500})
		if msg != "" && strings.Contains(msg, "read-heavy") {
			alertFound = true
			break
		}
	}
	if !alertFound {
		t.Fatal("expected CUSUM drift alert for read-heavy shift")
	}
}

func TestCusumDrift_TokenVelocityCreep(t *testing.T) {
	c := newCusumDriftState()
	// Warmup with small token consumption.
	for i := 0; i < 15; i++ {
		c.record(cusumRecord{tokenDelta: 200})
	}
	// Gradually increase token consumption (verbosity creep).
	var alertFound bool
	for i := 0; i < 60; i++ {
		msg := c.record(cusumRecord{tokenDelta: 5000})
		if msg != "" && strings.Contains(msg, "token-velocity") {
			alertFound = true
			break
		}
	}
	if !alertFound {
		t.Fatal("expected CUSUM drift alert for token velocity creep")
	}
}

func TestCusumDrift_MaxAlerts(t *testing.T) {
	c := newCusumDriftState()
	// Warmup with low error rate.
	for i := 0; i < 15; i++ {
		c.record(cusumRecord{tokenDelta: 500})
	}
	// Escalate errors to trigger alerts.
	alertCount := 0
	for i := 0; i < 200; i++ {
		msg := c.record(cusumRecord{failed: true, tokenDelta: 500})
		if msg != "" && strings.Contains(msg, "CUSUM Drift") {
			alertCount++
		}
	}
	if alertCount > c.maxAlerts {
		t.Fatalf("expected at most %d alerts, got %d", c.maxAlerts, alertCount)
	}
}

func TestCusumDrift_Reset(t *testing.T) {
	c := newCusumDriftState()
	// Record some data.
	for i := 0; i < 10; i++ {
		c.record(cusumRecord{failed: true, tokenDelta: 500})
	}
	c.reset()
	if c.totalToolCalls != 0 || c.errorRateCUSUM != 0 || c.baselined {
		t.Fatal("reset did not clear state")
	}
}

func TestCusumMean(t *testing.T) {
	if cusumMean(nil) != 0 {
		t.Fatal("mean of nil should be 0")
	}
	if cusumMean([]float64{1, 2, 3}) != 2 {
		t.Fatal("mean of [1,2,3] should be 2")
	}
}

func TestCusumDrift_Cooldown(t *testing.T) {
	c := newCusumDriftState()
	// Warmup with low error rate.
	for i := 0; i < 15; i++ {
		c.record(cusumRecord{tokenDelta: 500})
	}
	// Trigger first alert.
	var firstAlertIter int
	for i := 0; i < 60; i++ {
		msg := c.record(cusumRecord{failed: true, tokenDelta: 500})
		if msg != "" {
			firstAlertIter = c.totalToolCalls
			break
		}
	}
	if firstAlertIter == 0 {
		t.Fatal("expected first alert")
	}
	// Immediately after, CUSUM is high but cooldown should prevent
	// firing again within 6 tool calls.
	alertsInCooldown := 0
	for i := 0; i < 2; i++ { // 2 calls < cooldown of 6
		msg := c.record(cusumRecord{failed: true, tokenDelta: 500})
		if msg != "" {
			alertsInCooldown++
		}
	}
	if alertsInCooldown > 0 {
		t.Fatal("cooldown should prevent immediate re-alert")
	}
}

func TestCusumDrift_DeadBand(t *testing.T) {
	c := newCusumDriftState()
	// Warmup with baseline of ~0 error rate.
	for i := 0; i < 15; i++ {
		c.record(cusumRecord{tokenDelta: 500})
	}
	// Small deviations within dead band should not accumulate.
	for i := 0; i < 60; i++ {
		msg := c.record(cusumRecord{tokenDelta: 500})
		if msg != "" {
			t.Fatal("small deviations within dead band should not trigger alert")
		}
	}
}
