package im

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestDingtalkKeyAliases(t *testing.T) {
	a, err := newDingtalkAdapter("d", nil, config.IMAdapterConfig{
		Extra: map[string]interface{}{"client_id": "cid", "client_secret": "sec"},
	})
	if err != nil || a.appKey != "cid" || a.appSecret != "sec" {
		t.Fatalf("legacy keys rejected: err=%v", err)
	}
	a2, err := newDingtalkAdapter("d", nil, config.IMAdapterConfig{
		Extra: map[string]interface{}{"app_key": "ak", "app_secret": "as"},
	})
	if err != nil || a2.appKey != "ak" || a2.appSecret != "as" {
		t.Fatalf("canonical keys rejected: err=%v", err)
	}

	_, err = newDingtalkAdapter("d", nil, config.IMAdapterConfig{
		Extra: map[string]interface{}{"app_key": nil, "app_secret": nil},
	})
	if err == nil {
		t.Fatal("nil credentials must be rejected")
	}
}
