package cost

import "testing"

func TestPricingTable_ExactMatch(t *testing.T) {
	pt := DefaultPricingTable()
	rate, ok := pt.Get("anthropic", "claude-sonnet-4-20250514")
	if !ok {
		t.Fatal("expected exact match for claude-sonnet-4-20250514")
	}
	if rate.InputPerM != 3.0 {
		t.Errorf("expected input rate 3.0, got %f", rate.InputPerM)
	}
}

func TestPricingTable_CaseInsensitive(t *testing.T) {
	pt := DefaultPricingTable()
	rate, ok := pt.Get("OpenAI", "GPT-4o")
	if !ok {
		t.Fatal("expected case-insensitive match for OpenAI/GPT-4o")
	}
	if rate.InputPerM != 2.50 {
		t.Errorf("expected input rate 2.50, got %f", rate.InputPerM)
	}
}

func TestPricingTable_PrefixMatch(t *testing.T) {
	pt := DefaultPricingTable()
	// "gpt-4o-2024-08-06" should match "gpt-4o" prefix
	rate, ok := pt.Get("openai", "gpt-4o-2024-08-06")
	if !ok {
		t.Fatal("expected prefix match for gpt-4o-2024-08-06")
	}
	if rate.OutputPerM != 10.0 {
		t.Errorf("expected output rate 10.0, got %f", rate.OutputPerM)
	}
}

func TestPricingTable_SuffixMatch(t *testing.T) {
	pt := DefaultPricingTable()
	// "anthropic/claude-sonnet-4" should match "claude-sonnet-4" suffix
	rate, ok := pt.Get("anthropic", "anthropic/claude-sonnet-4")
	if !ok {
		t.Fatal("expected suffix match")
	}
	if rate.InputPerM != 3.0 {
		t.Errorf("expected input rate 3.0, got %f", rate.InputPerM)
	}
}

func TestPricingTable_DeepSeek(t *testing.T) {
	pt := DefaultPricingTable()
	rate, ok := pt.Get("deepseek", "deepseek-chat")
	if !ok {
		t.Fatal("expected match for deepseek/deepseek-chat")
	}
	if rate.InputPerM != 0.27 {
		t.Errorf("expected input rate 0.27, got %f", rate.InputPerM)
	}
}

func TestPricingTable_Groq(t *testing.T) {
	pt := DefaultPricingTable()
	_, ok := pt.Get("groq", "llama-3.1-8b-instant")
	if !ok {
		t.Fatal("expected match for groq/llama-3.1-8b-instant")
	}
}

func TestPricingTable_Zhipu(t *testing.T) {
	pt := DefaultPricingTable()
	rate, ok := pt.Get("zhipu", "glm-4-flash")
	if !ok {
		t.Fatal("expected match for zhipu/glm-4-flash")
	}
	if rate.InputPerM != 0.10 {
		t.Errorf("expected input rate 0.10, got %f", rate.InputPerM)
	}
}

func TestPricingTable_NotFound(t *testing.T) {
	pt := DefaultPricingTable()
	_, ok := pt.Get("unknown", "unknown-model")
	if ok {
		t.Error("expected not found for unknown provider/model")
	}
}

func TestPricingTable_Merge(t *testing.T) {
	base := DefaultPricingTable()
	custom := PricingTable{
		"custom-provider": {
			"custom-model": {InputPerM: 1.0, OutputPerM: 2.0},
		},
	}
	merged := base.Merge(custom)
	_, ok := merged.Get("anthropic", "claude-sonnet-4")
	if !ok {
		t.Error("base entries should survive merge")
	}
	rate, ok := merged.Get("custom-provider", "custom-model")
	if !ok {
		t.Error("custom entries should be added")
	}
	if rate.InputPerM != 1.0 {
		t.Errorf("expected custom rate, got %f", rate.InputPerM)
	}
}

func TestPricingTable_AllProvidersPresent(t *testing.T) {
	pt := DefaultPricingTable()
	expected := []string{
		"anthropic", "openai", "gemini", "deepseek",
		"moonshot", "kimi", "groq", "mistral",
		"zhipu", "zai", "minimax", "ark",
		"together", "perplexity",
	}
	for _, p := range expected {
		if _, ok := pt[p]; !ok {
			t.Errorf("expected provider %q in pricing table", p)
		}
	}
}
