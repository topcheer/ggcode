package im

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func Test1743FeishuSTTStrongValidation(t *testing.T) {
	full := config.IMSTTConfig{Provider: "openai", BaseURL: "https://x", APIKey: "k", Model: "m"}

	if got := resolveFeishuSTTConfig(full, nil); got == nil {
		t.Fatal("full global config must yield a primary")
	}

	noModel := full
	noModel.Model = ""
	if got := resolveFeishuSTTConfig(noModel, nil); got != nil {
		t.Fatal("partial global config must skip the primary (sibling contract)")
	}

	for i, mutate := range []func(*config.IMSTTConfig){
		func(c *config.IMSTTConfig) { c.BaseURL = "" },
		func(c *config.IMSTTConfig) { c.APIKey = "" },
		func(c *config.IMSTTConfig) { c.Model = "" },
	} {
		c := full
		mutate(&c)
		if got := resolveFeishuSTTConfig(c, nil); got != nil {
			t.Fatalf("global missing field %d must skip the primary", i)
		}
	}

	if got := resolveFeishuSTTConfig(config.IMSTTConfig{}, nil); got != nil {
		t.Fatal("empty global must skip the primary")
	}

	extra := map[string]interface{}{"stt": map[string]interface{}{"provider": "openai"}}
	if got := resolveFeishuSTTConfig(config.IMSTTConfig{}, extra); got != nil {
		t.Fatal("partial override over empty global must skip the primary")
	}

	extraFull := map[string]interface{}{"stt": map[string]interface{}{
		"provider": "openai", "baseUrl": "https://y", "apiKey": "k2", "model": "m2"}}
	if got := resolveFeishuSTTConfig(config.IMSTTConfig{}, extraFull); got == nil || got.Model != "m2" {
		t.Fatal("full override must yield a primary with overridden model")
	}
}
