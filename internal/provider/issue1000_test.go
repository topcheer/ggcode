package provider

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestIssue1000AnthropicWindowLimitNotQuotaExhausted(t *testing.T) {
	msg := "This request would exceed your usage limit for the 5-hour window. Your limit will reset at 5pm"
	cases := []struct {
		name string
		err  error
	}{
		{"429", fmt.Errorf("unexpected status 429: %s", msg)},
		{"plain", errors.New(msg)},
		{"weekly", errors.New("You have exceeded your weekly limit. Your limit will reset at 9am")},
	}
	for _, c := range cases {
		zh := UserFacingErrorLang(c.err, "zh-CN")
		if zh == "API 额度已用完或套餐已过期。请前往服务商页面查看额度状态、续订或充值后重试" {
			t.Errorf("%s: window limit misreported as permanent quota exhaustion: %q", c.name, zh)
		}
		en := UserFacingErrorLang(c.err, "en")
		if en == "API quota exhausted or plan expired. Check your provider dashboard, renew your plan or add credits, then retry" {
			t.Errorf("%s (en): window limit misreported as permanent quota exhaustion", c.name)
		}
	}
}

func TestIssue1000MiniMaxPermanentQuotaStillMatched(t *testing.T) {
	err := fmt.Errorf("status 429: usage limit exceeded, your 5-hour usage limit has been reached (code: 1028)")
	zh := UserFacingErrorLang(err, "zh-CN")
	if zh != "API 额度已用完或套餐已过期。请前往服务商页面查看额度状态、续订或充值后重试" {
		t.Errorf("permanent quota message lost its guidance: %q", zh)
	}
}

var _ = http.StatusTooManyRequests
