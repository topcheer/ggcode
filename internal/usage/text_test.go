package usage

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// #2150 batch 3: the IM /usage renderer must keep the balance/windows
// payload and degrade to one explanatory line on error/nil.
func TestRenderText(t *testing.T) {
	bal := 12.5
	got := RenderText("zai", &UsageInfo{
		Vendor:  "zai",
		Balance: &bal,
		Windows: []UsageWindow{{Label: "5h", UsedPercent: 42.3, ResetsAt: time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)}},
	}, nil)
	for _, want := range []string{"zai", "12.50", "5h", "42%", "resets"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}

	if got := RenderText("kimi", nil, errors.New("unsupported vendor")); !strings.Contains(got, "kimi") || !strings.Contains(got, "unsupported") {
		t.Fatalf("error case: %q", got)
	}
	if got := RenderText("x", nil, nil); !strings.Contains(got, "no data") {
		t.Fatalf("nil case: %q", got)
	}
	// No balance concept: no "| balance" segment.
	if got := RenderText("deepseek", &UsageInfo{Vendor: "deepseek", Windows: []UsageWindow{{Label: "weekly", UsedPercent: 7}}}, nil); strings.Contains(got, "balance") {
		t.Fatalf("nil balance must not render a balance line: %q", got)
	}
}

// TestDeepSeekBalanceStringForm pins the 2026-09-19 user report:
// `deepseek json: cannot unmarshal string into Go struct` - DeepSeek's
// /user/balance returns total_balance as a QUOTED string ("110.00").
func TestDeepSeekBalanceStringForm(t *testing.T) {
	var p struct {
		BalanceInfos []struct {
			Currency     string    `json:"currency"`
			TotalBalance flexFloat `json:"total_balance"`
		} `json:"balance_infos"`
	}
	if err := json.Unmarshal([]byte(`{"balance_infos":[{"currency":"CNY","total_balance":"110.00"}]}`), &p); err != nil {
		t.Fatalf("string-form total_balance failed: %v", err)
	}
	if len(p.BalanceInfos) != 1 || float64(p.BalanceInfos[0].TotalBalance) != 110.0 {
		t.Fatalf("string form = %+v", p.BalanceInfos)
	}
	// Bare-number form still works.
	if err := json.Unmarshal([]byte(`{"balance_infos":[{"currency":"CNY","total_balance":3.5}]}`), &p); err != nil {
		t.Fatalf("number-form total_balance failed: %v", err)
	}
	if float64(p.BalanceInfos[0].TotalBalance) != 3.5 {
		t.Fatalf("number form = %+v", p.BalanceInfos)
	}
}
