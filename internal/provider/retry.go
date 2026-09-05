package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/sashabaranov/go-openai"
)

const (
	providerRetryAttempts = 20
	// providerRetryBackoffCap caps a single backoff sleep.
	providerRetryBackoffCap = 30 * time.Second
)

// providerRetryTimeBudget caps the CUMULATIVE backoff sleep per logical call
// (#722). Without it, 20 attempts with 1s→30s exponential backoff sleep
// ~7.5min per call; multiplied by the failover layer's 3-call threshold a
// persistently failing primary blocks headless callers (cron/daemon/a2a)
// ~23min before the fallback takes over. Interactive users can Ctrl-C, so
// this is primarily a headless guard. Declared as a var so tests can
// shrink it.
var providerRetryTimeBudget = 2 * time.Minute

// errRetryBudgetExhausted marks a failure whose inner retry loop already
// consumed the full per-call backoff budget — the provider got a best-effort
// chance. FallbackProvider treats it as sustained failure and switches
// immediately instead of demanding failoverThreshold more full-budget calls.
var errRetryBudgetExhausted = errors.New("retry budget exhausted")

// retryBudget tracks cumulative backoff sleep against
// providerRetryTimeBudget. Once the deadline passes, sleep returns
// errRetryBudgetExhausted so callers stop retrying and surface a
// sentinel-wrapped error.
type retryBudget struct {
	deadline time.Time
	done     bool
}

func newRetryBudget() *retryBudget {
	return &retryBudget{deadline: time.Now().Add(providerRetryTimeBudget)}
}

// sleep sleeps for delay (from retryDelay) unless the budget deadline is
// closer, in which case it truncates the sleep and returns
// errRetryBudgetExhausted (the pending retry must not run — the budget is
// spent). Context cancellation propagates unchanged.
func (b *retryBudget) sleep(ctx context.Context, delay time.Duration) error {
	if remaining := time.Until(b.deadline); remaining <= 0 {
		b.done = true
		return errRetryBudgetExhausted
	} else if delay > remaining {
		b.done = true
		delay = remaining
	}
	if err := retrySleep(ctx, delay); err != nil {
		return err
	}
	if b.done {
		return errRetryBudgetExhausted
	}
	return nil
}

var retrySleep = func(ctx context.Context, delay time.Duration) error {
	select {
	case <-time.After(delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// isRetryable returns true for any error that is worth retrying.
//
// We retry aggressively: only 401 (auth), 403 (forbidden), and 404 (not found)
// are considered permanent failures. Everything else — rate limits, server
// errors, timeouts, network glitches, bad gateway, etc. — gets retried.
// IsContextOverflowError checks whether the error indicates the input prompt
// exceeds the model's context window. These errors are never retryable — the
// same request will always fail until the context is compacted.
func IsContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	// Guard against SDK error types whose .Error() panics on nil internals
	// (e.g., anthropic.Error with nil Response).
	msg := func() string {
		defer func() { recover() }()
		return err.Error()
	}()
	s := strings.ToLower(msg)
	keywords := []string{
		"context_length_exceeded",
		"maximum context",
		"maximum is", // e.g. "requested 40123 tokens, maximum is 131072" (#303)
		"context length",
		"prompt is too long",
		"prompt too long",
		"prompt exceeds",
		"max length",
		"超长",
		"exceeds the maximum",
		"request too large",
		"too many tokens",
		"input is too long",
		"exceeds the model's context",
		"token limit",
		"exceeds the limit",
		"token count exceeds",
		"input is too long for",
		"input length exceeds",
		"prompt tokens too long",
		"prompt tokens exceeds",
		"must have less than",
		"range of input length",
		"超出了模型最大",
		"token限制",
		"maximum input tokens",
		"input tokens exceeded",
		"context window",
	}
	for _, kw := range keywords {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}

	// User/session cancellation is never retryable. DeadlineExceeded is handled
	// below as a retryable timeout unless the caller context has already ended.
	if errors.Is(err, context.Canceled) {
		return false
	}

	// Prompt-too-long errors are permanent: retrying the same oversized request
	// will never succeed. Let the agent layer handle reactive compaction instead.
	if IsContextOverflowError(err) {
		return false
	}

	// Fail fast on permanent failures: quota/billing exhaustion and auth
	// errors will never succeed on retry. ClassifyLLMError uses keyword
	// matching that works for ALL error types (typed SDK errors AND raw
	// string errors), catching cases the per-type checks below miss —
	// e.g. a string error like "coding plan expired" or a generic HTTP
	// error wrapper around a 429 quota response.
	switch ClassifyLLMError(err) {
	case FailureQuota, FailureAuth:
		return false
	}

	// Check for HTTP status codes from known SDK error types.
	var openaiAPIErr *openai.APIError
	if errors.As(err, &openaiAPIErr) {
		// 429 is normally retryable, but coding plan providers (ZAI/GLM, Kimi,
		// OpenAI) return 429 for permanent quota exhaustion too. Detect those
		// and don't waste retry attempts.
		if openaiAPIErr.HTTPStatusCode == http.StatusTooManyRequests && isQuotaExhaustedError(err) {
			return false
		}
		return isRetryableHTTPStatus(openaiAPIErr.HTTPStatusCode)
	}
	var openaiReqErr *openai.RequestError
	if errors.As(err, &openaiReqErr) {
		if openaiReqErr.HTTPStatusCode == http.StatusTooManyRequests && isQuotaExhaustedError(err) {
			return false
		}
		return isRetryableHTTPStatus(openaiReqErr.HTTPStatusCode)
	}
	var anthropicErr *anthropic.Error
	if errors.As(err, &anthropicErr) {
		return isRetryableHTTPStatus(anthropicErr.StatusCode)
	}
	var httpErr interface{ HTTPStatusCode() int }
	if errors.As(err, &httpErr) {
		return isRetryableHTTPStatus(httpErr.HTTPStatusCode())
	}

	// Network / timeout errors are always retryable.
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, io.EOF) {
		return true
	}

	// Fallback: check error message for known non-retryable status codes.
	msg := err.Error()
	// #1617-C: bare leading codes ("401 Unauthorized: invalid api key" -
	// a common wrapper/relay format with no 'HTTP'/'status' prefix) hit
	// NONE of the containsHTTPStatus forms and defaulted to retryable,
	// burning the full budget on a permanent auth error. Anchor at start.
	for _, code := range []string{"400", "401", "402", "403", "404", "413", "422"} {
		if strings.HasPrefix(msg, code) && (len(msg) == len(code) || msg[len(code)] == ' ') {
			return false
		}
	}
	if containsHTTPStatus(msg, "401") || containsHTTPStatus(msg, "403") || containsHTTPStatus(msg, "404") {
		return false
	}
	// #306: 400 must be excluded in the string-fallback path too — Bad Request
	// is always permanent (malformed request body, invalid tool_use/tool_result
	// pairing, bad parameters). Without this, a non-typed "status code: 400"
	// from an OpenAI-compatible relay was retried 20 times with exponential
	// backoff (~6-7 minutes) before surfacing.
	if containsHTTPStatus(msg, "400") {
		return false
	}
	// #518: 422 must be excluded too — the typed path (isRetryableHTTPStatus)
	// treats Unprocessable Entity as permanent (schema/semantic rejection;
	// FriendlyError's own guidance is "switch model"). Without this, a
	// non-typed go-openai stream error (plain fmt.Errorf, errors.As misses)
	// from any OpenAI-compatible relay retried 20 times (~7m16s) per turn.
	if containsHTTPStatus(msg, "422") {
		return false
	}
	// #602(R1): 402 (payment required) and 413 (payload too large) complete
	// the string-fallback exclusion list. The typed path
	// (isRetryableHTTPStatus, #267) already treats both as permanent, but the
	// string fallback only excluded 400/401/403/404/422 — a non-typed
	// "status code: 402" from an OpenAI-compatible relay fell through to the
	// default-retryable branch and burned 20 retries (~7 min) on a billing
	// error that can never succeed. Fourth instance of the #306/#518/#267
	// string-vs-typed divergence class.
	if containsHTTPStatus(msg, "402") || containsHTTPStatus(msg, "413") {
		return false
	}

	// Any other error with a recognizable HTTP status code is retryable.
	// (400/402/413/422 are permanently excluded above — see #306/#518/#602.)
	// #518: 422 must NOT be in this string-fallback list — the typed path
	// (isRetryableHTTPStatus) treats 422 as permanent (schema/semantic
	// rejection), so a non-typed go-openai stream error (plain fmt.Errorf,
	// errors.As misses) must not spin ~7 minutes in 20 doomed retries.
	// Anchor via containsHTTPStatus so digit coincidences like "id=42913"
	// don't count as status codes (#518 low-risk side item).
	for _, code := range []string{
		"408", "409", "429",
		"500", "502", "503", "504", "520", "521", "522", "523", "524", "529",
	} {
		if containsHTTPStatus(msg, code) {
			return true
		}
	}

	// ZAI platform transient errors.
	if strings.Contains(msg, "网络错误") {
		return true
	}

	// Default: retry unknown errors. It's better to retry once too many
	// than to fail permanently on a transient issue.
	return true
}

// containsHTTPStatus reports whether msg contains the given HTTP status code
// in a context-anchored position. Bare substring matching ("401") would hit
// digit coincidences like "requested 40123 tokens" (#303/#306); the patterns
// below require the code to be delimited by spacing/punctuation or to follow
// a status label, matching the formats emitted by go-openai ("status code:
// 401, message: ..."), JSON relays (`status":401`), and plain wrappers
// ("statusCode:401").
// #561(F): sentence-final terminators (`.` `)` `;` `:`) are anchors too —
// `"status 401."` (period-terminated) previously matched none of the
// patterns, fell through to the default-retryable path, and burned 19
// retries (~7.5 min) on a permanent auth failure.
func containsHTTPStatus(msg, code string) bool {
	// #602(R3): digit-terminated patterns go through containsPatternAnchored
	// (errclass.go), which requires a non-digit (or end-of-string) boundary
	// after the code. Bare substring matching let a 5-digit coincidence like
	// `"status":40139` count as status 401 — the same digit-piercing class the
	// classifier closed (#303/#456/#577).
	return containsPatternAnchored(msg, " "+code+" ") ||
		containsPatternAnchored(msg, " "+code+",") ||
		containsPatternAnchored(msg, " "+code+"\n") ||
		containsPatternAnchored(msg, " "+code+".") ||
		containsPatternAnchored(msg, " "+code+")") ||
		containsPatternAnchored(msg, " "+code+";") ||
		containsPatternAnchored(msg, " "+code+":") ||
		containsPatternAnchored(msg, "code: "+code) ||
		containsPatternAnchored(msg, `status":`+code) ||
		containsPatternAnchored(msg, "statusCode:"+code)
}

func isRetryableForContext(ctx context.Context, err error) bool {
	// User cancellation is never retryable.
	if ctx != nil && errors.Is(ctx.Err(), context.Canceled) {
		return false
	}
	// DeadlineExceeded from the caller context means the agent turn
	// timed out — not retryable. But DeadlineExceeded from an HTTP
	// client timeout (where ctx is still alive) IS retryable.
	if ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return false
	}
	return isRetryable(err)
}

// isRetryableHTTPStatus returns true unless the status code is a permanent
// client error. 400/401/403/404 plus 402 (payment required — FriendlyError
// already treats billing exhaustion as needing user action), 413 (payload
// too large — the request will not shrink by retrying), and 422
// (unprocessable entity — schema/semantic rejection) are permanent (#267).
func isRetryableHTTPStatus(status int) bool {
	switch status {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden,
		http.StatusNotFound, http.StatusPaymentRequired,
		http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
		return false
	default:
		return true
	}
}

// isQuotaExhaustedError checks whether a 429 error is actually a permanent
// quota/billing exhaustion rather than a transient rate limit. Coding plan
// providers (ZAI/GLM, Kimi, OpenAI) use 429 for both cases.
// Delegates to the shared classifier in errclass.go (single source of truth).
func isQuotaExhaustedError(err error) bool {
	return IsQuotaExhaustedError(err)
}

// retryWithBackoffCtx retries fn up to maxAttempts times with exponential backoff.
// Only retries retryable errors (429 or 5xx), and honors Retry-After where available.
func retryWithBackoffCtx(ctx context.Context, fn func() error, maxAttempts int) error {
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	budget := newRetryBudget()
	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		err := fn()
		if err == nil {
			return nil
		}
		lastErr = err
		if !isRetryableForContext(ctx, err) || i == maxAttempts-1 {
			return err
		}
		if sleepErr := budget.sleep(ctx, retryDelay(err, i)); sleepErr != nil {
			// #722: budget exhaustion wraps the LAST provider error so the
			// failover layer recognizes best-effort exhaustion and switches
			// immediately instead of running the full threshold again.
			if errors.Is(sleepErr, errRetryBudgetExhausted) {
				return fmt.Errorf("%w: %w", errRetryBudgetExhausted, err)
			}
			return sleepErr
		}
	}
	return lastErr
}

func retryDelay(err error, attempt int) time.Duration {
	if delay, ok := retryAfterDelay(err); ok && delay > 0 {
		// Cap Retry-After at providerRetryBackoffCap. Some providers return
		// very large Retry-After values (60-300s); an interactive agent must
		// not freeze for minutes. If the cap expires and the server is still
		// rate-limiting, it will return 429 again and we retry — acceptable
		// for an interactive tool.
		if delay > providerRetryBackoffCap {
			delay = providerRetryBackoffCap
		}
		return delay
	}
	if attempt < 0 {
		attempt = 0
	}
	delay := time.Second * time.Duration(1<<minInt(attempt, 5))
	if delay > providerRetryBackoffCap {
		delay = providerRetryBackoffCap
	}
	// Add ±25% jitter to prevent thundering herd when multiple clients
	// retry simultaneously (e.g., shared rate limit across instances).
	jitterRange := delay / 4
	if jitterRange > 0 {
		delay = delay - jitterRange/2 + time.Duration(rand.Int64N(int64(jitterRange)))
	}
	// Re-clamp after jitter so the effective delay never exceeds the cap.
	if delay > providerRetryBackoffCap {
		delay = providerRetryBackoffCap
	}
	return delay
}

func retryAfterDelay(err error) (time.Duration, bool) {
	if err == nil {
		return 0, false
	}
	var anthropicErr *anthropic.Error
	if errors.As(err, &anthropicErr) && anthropicErr.Response != nil {
		return parseRetryAfterHeader(anthropicErr.Response.Header)
	}
	var responseErr interface{ Response() *http.Response }
	if errors.As(err, &responseErr) {
		if resp := responseErr.Response(); resp != nil {
			return parseRetryAfterHeader(resp.Header)
		}
	}
	return 0, false
}

func parseRetryAfterHeader(header http.Header) (time.Duration, bool) {
	if header == nil {
		return 0, false
	}
	retries := []struct {
		key    string
		units  time.Duration
		custom func(string) (time.Duration, bool)
	}{
		{
			key:   "Retry-After-Ms",
			units: time.Millisecond,
			custom: func(string) (time.Duration, bool) {
				return 0, false
			},
		},
		{
			key:   "Retry-After",
			units: time.Second,
			custom: func(v string) (time.Duration, bool) {
				t, err := time.Parse(time.RFC1123, v)
				if err != nil {
					return 0, false
				}
				return time.Until(t), true
			},
		},
	}
	for _, retry := range retries {
		value := header.Get(retry.key)
		if value == "" {
			continue
		}
		if retryAfter, err := strconv.ParseFloat(value, 64); err == nil {
			delay := time.Duration(retryAfter * float64(retry.units))
			if delay > 0 {
				return delay, true
			}
			return 0, false
		}
		if delay, ok := retry.custom(value); ok && delay > 0 {
			return delay, true
		}
	}
	return 0, false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// FriendlyError translates a raw provider error into a human-readable message
// with actionable advice. Returns the original error message if no pattern matches.
func FriendlyError(err error) string {
	if err == nil {
		return ""
	}

	msg := err.Error()
	lower := strings.ToLower(msg)

	// Extract HTTP status code if available
	statusCode := 0
	var anthropicErr *anthropic.Error
	if errors.As(err, &anthropicErr) {
		statusCode = anthropicErr.StatusCode
	}
	if statusCode == 0 {
		var openaiErr *openai.APIError
		if errors.As(err, &openaiErr) {
			statusCode = openaiErr.HTTPStatusCode
		}
	}
	if statusCode == 0 {
		var openaiReqErr *openai.RequestError
		if errors.As(err, &openaiReqErr) {
			statusCode = openaiReqErr.HTTPStatusCode
		}
	}
	if statusCode == 0 {
		// #577(A): anchor via containsHTTPStatus — the same helper isRetryable
		// uses — instead of bare substring matching. Digit coincidences like
		// "40123 tokens remaining" in a quota message were misreported as
		// auth failure ("invalid API key, run config set api_key"), sending
		// users down the wrong debugging path. Third occurrence of this bug
		// class (#303, #561-F fixed the other two).
		for _, code := range []int{400, 401, 402, 403, 404, 408, 413, 422, 429, 500, 502, 503, 504} {
			if containsHTTPStatus(msg, strconv.Itoa(code)) {
				statusCode = code
				break
			}
		}
	}

	// Context overflow — special handling
	if IsContextOverflowError(err) {
		return "The conversation has exceeded the model's context window. " +
			"Run /compact to compress the conversation history, or start a new session with /clear."
	}

	switch statusCode {
	case 401:
		return "Authentication failed (401). Your API key is invalid or expired. " +
			"Check your API key with: config set api_key=<your-key> " +
			"or verify the key in your provider dashboard."
	case 402:
		return "Payment required (402). Your API account has insufficient credits or billing. " +
			"Add credits or update billing in your provider dashboard."
	case 403:
		if strings.Contains(lower, "access_terminated") ||
			(strings.Contains(lower, "usage limit") && strings.Contains(lower, "billing cycle")) {
			return "API quota exhausted (403). You've reached the usage limit for this billing cycle. " +
				"Your quota will refresh in the next cycle. " +
				"To continue now, purchase extra usage or upgrade your plan."
		}
		if strings.Contains(lower, "rate limit") || strings.Contains(lower, "quota") {
			return "Rate limit exceeded (403). You've hit your API quota. " +
				"Wait a moment and try again, or upgrade your plan for higher limits."
		}
		return "Access forbidden (403). Your API key may not have permission for this model, " +
			"or your account may be suspended. Check your provider dashboard."
	case 404:
		return "Model not found (404). The configured model may be deprecated or misspelled. " +
			"Check available models with: /model"
	case 408:
		return "Request timed out (408). The provider took too long to respond. " +
			"This is usually temporary — try sending your message again."
	case 413:
		return "Request too large (413). The message payload exceeds the server's limit. " +
			"Run /compact to reduce conversation size, or simplify your request."
	case 422:
		return "Request rejected (422). The provider couldn't process the request format. " +
			"This may be due to an unsupported feature (e.g., tool calling) for this model. " +
			"Try a different model or simplify your request."
	case 429:
		return "Rate limited (429). Too many requests in a short period. " +
			"Wait a few seconds and try again. Consider using a model with higher rate limits."
	case 500, 502, 503, 504:
		return fmt.Sprintf("Server error (%d). The provider is experiencing issues. "+
			"This is temporary — please retry in a moment.", statusCode)
	}

	// Check for common network errors
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return "Connection timed out. The provider server didn't respond in time. " +
				"Check your internet connection and try again."
		}
		return "Network error: unable to reach the provider server. " +
			"Check your internet connection and try again."
	}
	if errors.Is(err, io.EOF) {
		return "Connection closed unexpectedly by the provider. " +
			"This is usually temporary — try again."
	}

	// Cancellation
	if errors.Is(err, context.Canceled) {
		return "Request cancelled."
	}

	// Fallback: return original error
	return msg
}
