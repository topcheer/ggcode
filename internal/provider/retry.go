package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/sashabaranov/go-openai"
)

const (
	providerRetryAttempts   = 20
	providerRetryBackoffCap = 30 * time.Second
)

var providerRetryTimeBudget = 2 * time.Minute

var retryAlwaysEnabled atomic.Bool

func SetRetryAlways(v bool) { retryAlwaysEnabled.Store(v) }

func RetryAlwaysEnabled() bool { return retryAlwaysEnabled.Load() }

const retryAlwaysAttempts = 1 << 30

var errRetryBudgetExhausted = errors.New("retry budget exhausted")

type retryBudget struct {
	deadline  time.Time
	done      bool
	unlimited bool
}

func newRetryBudget() *retryBudget {
	return &retryBudget{deadline: time.Now().Add(providerRetryTimeBudget)}
}

func (b *retryBudget) sleep(ctx context.Context, delay time.Duration) error {
	if b.unlimited {
		return retrySleep(ctx, delay)
	}
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

func IsContextOverflowError(err error) bool {
	if err == nil {
		return false
	}

	msg := func() string {
		defer func() { recover() }()
		return err.Error()
	}()
	s := strings.ToLower(msg)
	keywords := []string{
		"context_length_exceeded",
		"maximum context",
		"maximum is",
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

	if errors.Is(err, context.Canceled) {
		return false
	}

	if IsContextOverflowError(err) {
		return false
	}

	switch ClassifyLLMError(err) {
	case FailureQuota, FailureAuth:
		return false
	}

	var openaiAPIErr *openai.APIError
	if errors.As(err, &openaiAPIErr) {

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

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, io.EOF) {
		return true
	}

	msg := err.Error()

	for _, code := range []string{"400", "401", "402", "403", "404", "413", "422"} {
		if strings.HasPrefix(msg, code) && (len(msg) == len(code) || msg[len(code)] == ' ') {
			return false
		}
	}
	if containsHTTPStatus(msg, "401") || containsHTTPStatus(msg, "403") || containsHTTPStatus(msg, "404") {
		return false
	}

	if containsHTTPStatus(msg, "400") {
		return false
	}

	if containsHTTPStatus(msg, "422") {
		return false
	}

	if containsHTTPStatus(msg, "402") || containsHTTPStatus(msg, "413") {
		return false
	}

	for _, code := range []string{
		"408", "409", "429",
		"500", "502", "503", "504", "520", "521", "522", "523", "524", "529",
	} {
		if containsHTTPStatus(msg, code) {
			return true
		}
	}

	if strings.Contains(msg, "网络错误") {
		return true
	}

	return true
}

func containsHTTPStatus(msg, code string) bool {

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

	if ctx != nil && errors.Is(ctx.Err(), context.Canceled) {
		return false
	}

	if ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return false
	}

	if IsContextOverflowError(err) {
		return false
	}

	if retryAlwaysEnabled.Load() {
		return true
	}
	return isRetryable(err)
}

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

func isQuotaExhaustedError(err error) bool {
	return IsQuotaExhaustedError(err)
}

func retryWithBackoffCtx(ctx context.Context, fn func() error, maxAttempts int, timeoutRetryInPlace bool) error {
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	budget := newRetryBudget()
	if retryAlwaysEnabled.Load() {

		budget.unlimited = true
	}
	for i := 0; ; i++ {
		err := fn()
		if err == nil {
			return nil
		}
		retryable := isRetryableForContext(ctx, err)
		if retryable && timeoutRetryInPlace && isTimeoutClassError(err) {
			budget.unlimited = true
		} else if !retryable || i >= maxAttempts-1 {
			return err
		}
		if sleepErr := budget.sleep(ctx, retryDelay(err, i)); sleepErr != nil {

			if errors.Is(sleepErr, errRetryBudgetExhausted) {
				return fmt.Errorf("%w: %w", errRetryBudgetExhausted, err)
			}
			return sleepErr
		}
	}
}

func retryDelay(err error, attempt int) time.Duration {
	if delay, ok := retryAfterDelay(err); ok && delay > 0 {

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

	jitterRange := delay / 4
	if jitterRange > 0 {
		delay = delay - jitterRange/2 + time.Duration(rand.Int64N(int64(jitterRange)))
	}

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

func isTimeoutClassError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}

	msg := func() (m string) {
		defer func() { recover() }()
		return err.Error()
	}()
	s := strings.ToLower(msg)
	return strings.Contains(s, "connection timed out") ||
		strings.Contains(s, "context deadline exceeded")
}

func TimeoutRetryNotice(err error, attempt int) string {
	if !isTimeoutClassError(err) {
		return ""
	}
	n := attempt + 1
	if n < 3 || n%3 != 0 {
		return ""
	}
	return fmt.Sprintf("[Timeout retry %d: provider still not responding - retrying in place until it recovers (Ctrl-C to stop)]", n)
}

type friendlyWrappedError struct {
	text string
	err  error
}

func (e *friendlyWrappedError) Error() string { return e.text }
func (e *friendlyWrappedError) Unwrap() error { return e.err }

func WrapFriendly(err error) error {
	if err == nil {
		return nil
	}
	return &friendlyWrappedError{text: FriendlyError(err), err: err}
}

func FriendlyError(err error) string {
	if err == nil {
		return ""
	}

	msg := err.Error()
	lower := strings.ToLower(msg)

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

		for _, code := range []int{400, 401, 402, 403, 404, 408, 413, 422, 429, 500, 502, 503, 504} {
			if containsHTTPStatus(msg, strconv.Itoa(code)) {
				statusCode = code
				break
			}
		}
	}

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
			"This is usually temporary - try sending your message again."
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
			"This is temporary - please retry in a moment.", statusCode)
	}

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
			"This is usually temporary - try again."
	}

	if errors.Is(err, context.Canceled) {
		return "Request cancelled."
	}

	return msg
}
