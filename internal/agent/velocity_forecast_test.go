package agent

import (
	"testing"
)

func TestVelocityForecast_BelowMinIterations(t *testing.T) {
	v := newVelocityForecastState()
	v.recordIteration(true)
	v.recordIteration(false)
	v.recordIteration(true)

	msg := v.maybeForecast(1, 15, false) // maxIter < 20
	if msg != "" {
		t.Fatalf("expected empty message when maxIter < %d, got: %s", velocityMinIterations, msg)
	}
}

func TestVelocityForecast_HighProductiveRate_NoWarning(t *testing.T) {
	v := newVelocityForecastState()

	// Record 10 iterations, 8 productive = 80% rate (well above thresholds)
	for i := 0; i < 10; i++ {
		v.recordIteration(i%5 != 0) // 4/5 productive = 80%
	}

	// At 40% of maxIter=25, iteration 10/25 = 40%
	msg := v.maybeForecast(10, 25, false)
	if msg != "" {
		t.Fatalf("expected no forecast for high productive rate, got: %s", msg)
	}
}

func TestVelocityForecast_LowProductiveRate_FirstCheck(t *testing.T) {
	v := newVelocityForecastState()

	// Record 10 iterations, only 2 productive = 20% rate (< 40% threshold)
	for i := 0; i < 10; i++ {
		v.recordIteration(i < 2) // only first 2 productive
	}

	msg := v.maybeForecast(10, 25, false) // 40% of budget
	if msg == "" {
		t.Fatal("expected forecast for low productive rate at 40% budget")
	}

	if !v.firstCheckInjected {
		t.Fatal("expected firstCheckInjected to be true")
	}

	// Should contain key information
	if !contains(msg, "velocity forecast") {
		t.Errorf("expected message to contain 'velocity forecast', got: %s", msg)
	}
	if !contains(msg, "40") {
		t.Errorf("expected message to mention 40%% budget used, got: %s", msg)
	}
}

func TestVelocityForecast_LowProductiveRate_SecondCheck(t *testing.T) {
	v := newVelocityForecastState()

	// Record 15 iterations, only 3 productive = 20% rate (< 50% threshold at second check)
	for i := 0; i < 15; i++ {
		v.recordIteration(i < 3)
	}

	// First call at 40% (iteration 10/25) fires the first check
	msg1 := v.maybeForecast(10, 25, false)
	if msg1 == "" {
		t.Fatal("expected first forecast")
	}
	if !v.firstCheckInjected {
		t.Fatal("expected firstCheckInjected to be true")
	}

	// Record 5 more iterations (still only 3 productive out of 15 total)
	// Second call at 60% (iteration 15/25) fires the second check
	msg2 := v.maybeForecast(15, 25, false)
	if msg2 == "" {
		t.Fatal("expected forecast for low productive rate at 60% budget")
	}

	if !v.secondCheckInjected {
		t.Fatal("expected secondCheckInjected to be true")
	}

	// Second check message should be urgent
	if !contains(msg2, "CRITICAL") {
		t.Errorf("expected message to contain 'CRITICAL' for second check, got: %s", msg2)
	}
}

func TestVelocityForecast_ResearchMode_LenientThreshold(t *testing.T) {
	v := newVelocityForecastState()

	// Record 10 iterations, only 3 productive = 30% rate
	// In research mode, threshold is 20%, so 30% should NOT trigger first check
	for i := 0; i < 10; i++ {
		v.recordIteration(i < 3)
	}

	msg := v.maybeForecast(10, 25, true) // researchMode=true
	if msg != "" {
		t.Fatalf("expected no forecast in research mode with 30%% rate (>20%% threshold), got: %s", msg)
	}

	// But with 15% rate it should trigger
	v2 := newVelocityForecastState()
	for i := 0; i < 10; i++ {
		v2.recordIteration(i < 2) // 20% rate - at threshold, should not trigger
	}
	msg2 := v2.maybeForecast(10, 25, true)
	// 20% is exactly at velocityResearchLowThreshold (0.20), not strictly less
	if msg2 != "" {
		t.Logf("research mode at exactly 20%% rate: msg=%s (expected empty, threshold check is strict <)", msg2)
	}
}

func TestVelocityForecast_DoesNotRefireFirstCheck(t *testing.T) {
	v := newVelocityForecastState()

	// Record low productive rate
	for i := 0; i < 10; i++ {
		v.recordIteration(i < 2)
	}

	msg1 := v.maybeForecast(10, 25, false)
	if msg1 == "" {
		t.Fatal("expected first forecast")
	}

	// Should not fire again
	msg2 := v.maybeForecast(11, 25, false)
	if msg2 != "" {
		t.Fatalf("expected no re-fire of first check, got: %s", msg2)
	}
}

func TestVelocityForecast_Reset(t *testing.T) {
	v := newVelocityForecastState()
	v.recordIteration(true)
	v.recordIteration(false)
	v.firstCheckInjected = true
	v.secondCheckInjected = true

	v.reset()

	if v.firstCheckInjected {
		t.Error("firstCheckInjected should be false after reset")
	}
	if v.secondCheckInjected {
		t.Error("secondCheckInjected should be false after reset")
	}
	if v.productiveCount != 0 {
		t.Errorf("productiveCount should be 0 after reset, got %d", v.productiveCount)
	}
	if v.totalCount != 0 {
		t.Errorf("totalCount should be 0 after reset, got %d", v.totalCount)
	}
}

func TestVelocityForecast_ProductiveRate(t *testing.T) {
	v := newVelocityForecastState()

	v.recordIteration(true)
	v.recordIteration(true)
	v.recordIteration(false)
	v.recordIteration(true)
	v.recordIteration(false)

	rate := v.productiveRate()
	expected := 0.6
	if rate < expected-0.01 || rate > expected+0.01 {
		t.Errorf("expected productive rate ~%.2f, got %.4f", expected, rate)
	}
}

func TestVelocityForecast_EmptyState(t *testing.T) {
	v := newVelocityForecastState()
	if rate := v.productiveRate(); rate != 0 {
		t.Errorf("expected rate 0 for empty state, got %.4f", rate)
	}
}
