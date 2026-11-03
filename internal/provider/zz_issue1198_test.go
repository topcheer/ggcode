package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

type mockProber struct {
	errToReturn error
}

func (m *mockProber) Name() string               { return "mock" }
func (m *mockProber) Model() string              { return "mock-model" }
func (m *mockProber) Endpoint() string           { return "https://api.mock.com" }
func (m *mockProber) SetEndpoint(string)         {}
func (m *mockProber) SetModel(string)            {}
func (m *mockProber) Vendor() string             { return "mock" }
func (m *mockProber) SupportsImage() bool        { return false }
func (m *mockProber) SupportsToolChoice() bool   { return true }
func (m *mockProber) SupportsToolResponse() bool { return false }
func (m *mockProber) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	if m.errToReturn != nil {
		return nil, m.errToReturn
	}
	return &ChatResponse{Message: Message{Role: "assistant"}}, nil
}
func (m *mockProber) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent)
	close(ch)
	return ch, nil
}
func (m *mockProber) CountTokens(ctx context.Context, messages []Message) (int, error) {
	return len(messages) * 10, nil
}
func (m *mockProber) probeChat(ctx context.Context, messages []Message) error {
	return m.errToReturn
}

func TestIssue1198_RateLimitDoesNotShrink(t *testing.T) {
	ctx := context.Background()

	provider429 := &mockProber{errToReturn: errors.New("rate limit exceeded: 429 too many requests")}
	result := tryTierProbe(ctx, provider429, 200_000)
	if result == -1 {
		t.Errorf("429 error caused abort (-1), expected inconclusive (0)")
	}
	if result < 0 || result > 0 {

		t.Errorf("429 error returned %d, expected 0 (inconclusive)", result)
	}

	providerRateLimit := &mockProber{errToReturn: errors.New("Rate limit: too many requests")}
	result = tryTierProbe(ctx, providerRateLimit, 200_000)
	if result < 0 || result > 0 {
		t.Errorf("rate limit keyword returned %d, expected 0 (inconclusive)", result)
	}

	provider529 := &mockProber{errToReturn: errors.New("529 overloaded")}
	result = tryTierProbe(ctx, provider529, 200_000)
	if result < 0 || result > 0 {
		t.Errorf("529 overloaded returned %d, expected 0 (inconclusive)", result)
	}
}

func TestIssue1198_ContextOverflowCounts(t *testing.T) {
	ctx := context.Background()

	providerExact := &mockProber{errToReturn: errors.New("this model's maximum context length is 128000 tokens")}
	result := tryTierProbe(ctx, providerExact, 200_000)
	if result != 128_000 {
		t.Errorf("context overflow with exact value returned %d, expected 128000", result)
	}

	providerNoExact := &mockProber{errToReturn: errors.New("prompt is too long")}
	result = tryTierProbe(ctx, providerNoExact, 200_000)
	if result != 0 {
		t.Errorf("context overflow without exact value returned %d, expected 0", result)
	}

	overflowMessages := []string{
		"context_length_exceeded",
		"prompt too long",
		"request too large",
		"exceeds the maximum",
		"too many tokens",
	}

	for _, msg := range overflowMessages {
		provider := &mockProber{errToReturn: errors.New(msg)}
		result := tryTierProbe(ctx, provider, 200_000)
		if result != 0 {

			t.Errorf("overflow message %q returned %d, expected 0", msg, result)
		}
	}

	providerWithExact := &mockProber{errToReturn: errors.New("maximum context length is 200000 tokens")}
	result = tryTierProbe(ctx, providerWithExact, 200_000)
	if result != 200_000 {
		t.Errorf("overflow with exact value returned %d, expected 200000", result)
	}
}

func TestIssue1198_NetworkErrorsDoNotShrink(t *testing.T) {
	ctx := context.Background()

	networkErrors := []string{
		"connection reset by peer",
		"unexpected eof",
		"broken pipe",
		"i/o timeout",
		"connection refused",
		"no such host",
		"tls handshake timeout",
	}

	for _, msg := range networkErrors {
		provider := &mockProber{errToReturn: errors.New(msg)}
		result := tryTierProbe(ctx, provider, 200_000)
		if result < 0 || result > 0 {
			t.Errorf("network error %q returned %d, expected 0 (inconclusive)", msg, result)
		}
	}
}

func TestIssue1198_TimeoutErrorsDoNotShrink(t *testing.T) {
	ctx := context.Background()

	timeoutErrors := []string{
		"context deadline exceeded",
		"request timeout",
		"client timeout",
		"deadline exceeded",
	}

	for _, msg := range timeoutErrors {
		provider := &mockProber{errToReturn: errors.New(msg)}
		result := tryTierProbe(ctx, provider, 200_000)
		if result < 0 || result > 0 {
			t.Errorf("timeout error %q returned %d, expected 0 (inconclusive)", msg, result)
		}
	}
}

func TestIssue1198_AuthErrorsAbort(t *testing.T) {
	ctx := context.Background()

	authErrors := []string{
		"unauthorized",
		"invalid api key",
		"authentication failed",
		"401 unauthorized",
		"permission denied",
	}

	for _, msg := range authErrors {
		provider := &mockProber{errToReturn: errors.New(msg)}
		result := tryTierProbe(ctx, provider, 200_000)
		if result != -1 {
			t.Errorf("auth error %q returned %d, expected -1 (abort)", msg, result)
		}
	}
}

func TestIssue1198_FiveXXErrorsDoNotShrink(t *testing.T) {
	ctx := context.Background()

	fiveXXErrors := []string{
		"500 internal server error",
		"502 bad gateway",
		"503 service unavailable",
		"504 gateway timeout",
	}

	for _, msg := range fiveXXErrors {
		provider := &mockProber{errToReturn: errors.New(msg)}
		result := tryTierProbe(ctx, provider, 200_000)
		if result < 0 || result > 0 {
			t.Errorf("5xx error %q returned %d, expected 0 (inconclusive)", msg, result)
		}
	}
}

func TestIssue1198_Sub128KCachePersists(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := filepath.Join(config.ConfigDir(), "state")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	cacheJSON := `{
  "version": 2,
  "entries": {
    "vendor|url|tiny-model": 8192,
    "vendor|url|normal-model": 200000
  }
}`
	cachePath := filepath.Join(dir, "context_windows.json")
	if err := os.WriteFile(cachePath, []byte(cacheJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	probeCacheMu.Lock()
	probeCache = nil
	probeLoaded = false
	probeCacheMu.Unlock()

	tinyModel := LookupProbeCache("vendor|url|tiny-model")
	if tinyModel != 8192 {
		t.Errorf("sub-128K entry was dropped: got %d, want 8192", tinyModel)
	}

	normalModel := LookupProbeCache("vendor|url|normal-model")
	if normalModel != 200_000 {
		t.Errorf("normal entry incorrect: got %d, want 200000", normalModel)
	}
}

func TestIssue1198_LegacyCacheMigration(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := filepath.Join(config.ConfigDir(), "state")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	legacyJSON := `{
  "vendor|url|legacy64k": 64000,
  "vendor|url|legacy100k": 100000,
  "vendor|url|claude": 200000
}`
	cachePath := filepath.Join(dir, "context_windows.json")
	if err := os.WriteFile(cachePath, []byte(legacyJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	probeCacheMu.Lock()
	probeCache = nil
	probeLoaded = false
	probeCacheMu.Unlock()

	legacy64k := LookupProbeCache("vendor|url|legacy64k")
	if legacy64k != 0 {
		t.Errorf("legacy 64K entry survived migration: got %d, want 0", legacy64k)
	}

	legacy100k := LookupProbeCache("vendor|url|legacy100k")
	if legacy100k != 0 {
		t.Errorf("legacy 100K entry survived migration: got %d, want 0", legacy100k)
	}

	claude := LookupProbeCache("vendor|url|claude")
	if claude != 200_000 {
		t.Errorf("claude entry incorrect after migration: got %d, want 200000", claude)
	}

	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	dataStr := string(data)
	if !containsJSONString(dataStr, "version") {
		t.Error("cache file was not upgraded to include version field")
	}
	if !containsJSONString(dataStr, fmt.Sprintf(`"version": %d`, probeCacheVersion)) {
		t.Error("cache file version is not current after migration")
	}
}

func TestIssue1198_CacheVersionDowngrade(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := filepath.Join(config.ConfigDir(), "state")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	v1JSON := `{
  "version": 1,
  "entries": {
    "vendor|url|old64k": 64000,
    "vendor|url|good-model": 200000
  }
}`
	cachePath := filepath.Join(dir, "context_windows.json")
	if err := os.WriteFile(cachePath, []byte(v1JSON), 0o644); err != nil {
		t.Fatal(err)
	}

	probeCacheMu.Lock()
	probeCache = nil
	probeLoaded = false
	probeCacheMu.Unlock()

	old64k := LookupProbeCache("vendor|url|old64k")
	if old64k != 0 {
		t.Errorf("v1 sub-128K entry survived migration: got %d, want 0", old64k)
	}

	goodModel := LookupProbeCache("vendor|url|good-model")
	if goodModel != 200_000 {
		t.Errorf("good model entry incorrect after migration: got %d, want 200000", goodModel)
	}

	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	dataStr := string(data)
	if !containsJSONString(dataStr, fmt.Sprintf(`"version": %d`, probeCacheVersion)) {
		t.Error("cache version was not updated by migration")
	}
}

func TestIssue1198_InferSub128KThenSurviveReload(t *testing.T) {
	resetProbeCacheForTest(t)

	err := errors.New("maximum context length is 8192 tokens")
	var setMax int
	probeKey := "vendor|url|tiny-model"

	result := InferContextWindowFromError(
		err,
		10_000,
		200_000,
		probeKey,
		func(n int) { setMax = n },
	)

	if result != 8192 {
		t.Errorf("expected 8192, got %d", result)
	}
	if setMax != 8192 {
		t.Errorf("setMax called with %d, want 8192", setMax)
	}

	cached := LookupProbeCache(probeKey)
	if cached != 8192 {
		t.Errorf("cache = %d, want 8192", cached)
	}

	probeCacheMu.Lock()
	probeCache = nil
	probeLoaded = false
	probeCacheMu.Unlock()

	reloaded := LookupProbeCache(probeKey)
	if reloaded != 8192 {
		t.Errorf("after reload, cache = %d, want 8192", reloaded)
	}
}

func containsJSONString(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && indexOfSubstring(s, substr) >= 0
}

func indexOfSubstring(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func TestIssue1198_TierProbeRetryLogic(t *testing.T) {
	ctx := context.Background()

	provider := &mockProber{errToReturn: errors.New("429 rate limit exceeded")}

	result := tryTierProbe(ctx, provider, 200_000)
	if result != 0 {
		t.Errorf("429 error returned %d, expected 0 (inconclusive, allows retry)", result)
	}

	provider.errToReturn = nil
	result = tryTierProbe(ctx, provider, 200_000)
	if result != 200_000 {
		t.Errorf("after retry, success returned %d, expected 200000", result)
	}
}

func TestIssue1198_ExactValueInTransientError(t *testing.T) {
	ctx := context.Background()

	provider := &mockProber{errToReturn: errors.New("429 rate limit: maximum context length is 128000 tokens")}
	result := tryTierProbe(ctx, provider, 200_000)
	if result != 128_000 {
		t.Errorf("429 with exact value returned %d, expected 128000", result)
	}
}
