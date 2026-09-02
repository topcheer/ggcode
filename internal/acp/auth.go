package acp

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

// GitHub Device Flow constants
const (
	githubDeviceCodeURL  = "https://github.com/login/device/code"
	githubAccessTokenURL = "https://github.com/login/oauth/access_token"
	githubDeviceClientID = "Iv1.b514d6eb6e6f3a8e" // Placeholder — replace with actual Client ID
	githubDeviceScope    = "read:user"
	authProviderID       = "ggcode-acp"
)

// DeviceCodeResponse represents GitHub's device code response.
type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// DeviceTokenResponse represents GitHub's token polling response.
type DeviceTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error,omitempty"`
}

// AuthHandler manages ACP authentication.
type AuthHandler struct {
	transport *Transport
	sessionID string
	store     *auth.Store

	// accessTokenURL overrides the token polling endpoint (tests).
	accessTokenURL string
}

// NewAuthHandler creates a new auth handler.
func NewAuthHandler(transport *Transport, sessionID string) *AuthHandler {
	return &AuthHandler{
		transport: transport,
		sessionID: sessionID,
		store:     auth.DefaultStore(),
	}
}

// HandleAgentAuth performs GitHub Device Flow authentication.
// It sends the user_code and verification_uri to the Client via session/update
// notifications, then polls GitHub for the token.
func (ah *AuthHandler) HandleAgentAuth(ctx context.Context) error {
	// Step 1: Request device code
	deviceResp, err := ah.requestDeviceCode(ctx)
	if err != nil {
		return fmt.Errorf("requesting device code: %w", err)
	}

	// Step 2: Send user_code to Client via notification
	ah.sendAuthInstructions(deviceResp)

	// Step 3: Poll for token
	token, err := ah.pollForToken(ctx, deviceResp)
	if err != nil {
		return fmt.Errorf("polling for token: %w", err)
	}

	// Step 4: Save token
	if err := ah.saveToken(token); err != nil {
		return fmt.Errorf("saving token: %w", err)
	}

	return nil
}

// HandleEnvVarAuth validates that required environment variables are set.
func (ah *AuthHandler) HandleEnvVarAuth(vars []AuthEnvVar) error {
	for _, v := range vars {
		val := os.Getenv(v.Name)
		if val == "" {
			optional := v.Optional != nil && *v.Optional
			if !optional {
				return fmt.Errorf("required environment variable %s is not set", v.Name)
			}
		}
	}
	return nil
}

// requestDeviceCode initiates the GitHub Device Flow.
func (ah *AuthHandler) requestDeviceCode(ctx context.Context) (*DeviceCodeResponse, error) {
	data := url.Values{
		"client_id": {githubDeviceClientID},
		"scope":     {githubDeviceScope},
	}

	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "POST", githubDeviceCodeURL, bytes.NewBufferString(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("building device/code request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST device/code: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := util.ReadAll(resp.Body, util.ReadLimitAuth)
		return nil, fmt.Errorf("device/code returned %d: %s", resp.StatusCode, string(body))
	}

	var result DeviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding device code response: %w", err)
	}

	return &result, nil
}

// sendAuthInstructions sends the verification instructions to the Client.
func (ah *AuthHandler) sendAuthInstructions(resp *DeviceCodeResponse) {
	// Send user_code + verification_uri as a session/update notification
	_ = ah.transport.WriteNotification("session/update", SessionUpdateParams{
		SessionID: ah.sessionID,
		Update: SessionUpdate{
			Type: "auth_required",
			Content: &ContentBlock{
				Type: "text",
				Text: fmt.Sprintf("To authenticate, visit: %s\nEnter code: %s", resp.VerificationURI, resp.UserCode),
			},
		},
	})
}

// pollForToken polls GitHub for the access token.
func (ah *AuthHandler) pollForToken(ctx context.Context, deviceResp *DeviceCodeResponse) (string, error) {
	interval := deviceResp.Interval
	if interval <= 0 {
		interval = 5
	}

	expiresIn := deviceResp.ExpiresIn
	if expiresIn <= 0 {
		// RFC 8628 §3.2 requires expires_in, but non-GitHub or proxied
		// device endpoints sometimes omit it. A zero value would fire
		// time.After(0) and expire the flow before the first poll ever
		// runs. Fall back to the RFC-recommended 900s, mirroring the
		// interval guard below.
		expiresIn = 900
	}

	expiry := time.After(time.Duration(expiresIn) * time.Second)
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	var streak transientStreakTracker
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-expiry:
			return "", fmt.Errorf("device code expired")
		case <-ticker.C:
			token, err := ah.checkToken(ctx, deviceResp.DeviceCode)
			if err == nil {
				return token, nil
			}
			switch classifyDevicePollError(err) {
			case devicePollSlowDown:
				// RFC 8628 §3.5: each slow_down response requires the polling
				// interval to grow by 5 seconds cumulatively (5→10→15…), not a
				// constant reset back to base+5. (#668) Growth is capped so a
				// misbehaving endpoint cannot render polling useless. (#672)
				interval = growDevicePollInterval(interval)
				ticker.Reset(time.Duration(interval) * time.Second)
			case devicePollAbort:
				return "", err
			default:
				// authorization_pending, or a transient network/transport error
				// (timeouts, 429, 5xx — see classifyTransportError). The user may
				// already have visited the verification URI and entered the
				// user_code — aborting on a proxy blip would waste that. Keep
				// polling until the device code expires. (#668) The transient
				// streak is bounded so a failure misclassified as transient
				// cannot burn the whole device-code lifetime. (#672)
				if streak.record(err) {
					return "", fmt.Errorf("device flow: %d consecutive transient token poll failures, giving up (last error: %w)", maxConsecutiveTransientPollErrors, err)
				}
				debug.Log("acp", "device flow: token poll error (will retry): %v", err)
			}
		}
	}
}

// checkToken checks if the user has completed the device flow.
func (ah *AuthHandler) checkToken(ctx context.Context, deviceCode string) (string, error) {
	data := url.Values{
		"client_id":   {githubDeviceClientID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}

	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	endpoint := ah.accessTokenURL
	if endpoint == "" {
		endpoint = githubAccessTokenURL
	}
	req, err := http.NewRequestWithContext(reqCtx, "POST", endpoint, bytes.NewBufferString(data.Encode()))
	if err != nil {
		// An unparseable endpoint URL is a configuration error, not a blip.
		return "", &permanentDeviceFlowError{err: fmt.Errorf("building access_token request: %w", err)}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Wrap with a transient/permanent classification so the poll loop can
		// tell a proxy blip (retry) from a broken endpoint (abort). (#672)
		return "", classifyTransportError(fmt.Errorf("POST access_token: %w", err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// A non-200 from the token endpoint is an HTTP-level failure, not an
		// in-band OAuth error. 429 (rate limit) and 5xx (server hiccup) are
		// transient and stay retryable; any other 4xx (404 wrong endpoint,
		// 400 malformed client) is a permanent configuration error. (#672)
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return "", &transientDeviceFlowError{err: fmt.Errorf("access_token returned HTTP %d", resp.StatusCode)}
		}
		body, _ := util.ReadAll(resp.Body, util.ReadLimitAuth)
		return "", &permanentDeviceFlowError{err: fmt.Errorf("access_token returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))}
	}

	var result DeviceTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		// A 200 whose body is not JSON (HTML error page, empty body) means
		// the endpoint is not the real token endpoint — permanent. (#672)
		return "", &permanentDeviceFlowError{err: fmt.Errorf("decoding token response: %w", err)}
	}

	if result.Error != "" {
		switch result.Error {
		case "authorization_pending":
			return "", errDeviceAuthPending
		case "slow_down":
			return "", errDeviceSlowDown
		case "access_denied", "expired_token":
			return "", &terminalDeviceFlowError{err: fmt.Errorf("device flow: %s", result.Error)}
		}
		return "", &terminalDeviceFlowError{err: fmt.Errorf("device flow: unexpected OAuth error: %s", result.Error)}
	}

	// A 200 with no access_token (e.g. `{}`) is NOT success: before #672 the
	// empty string flowed back as the token, was persisted, and the whole
	// flow reported success. Treat it as a terminal failure. (#672)
	if result.AccessToken == "" {
		return "", &permanentDeviceFlowError{err: fmt.Errorf("device flow: token response carried no access_token")}
	}

	return result.AccessToken, nil
}

// saveToken persists the token to the auth store.
func (ah *AuthHandler) saveToken(token string) error {
	return ah.store.Save(&auth.Info{
		ProviderID:  authProviderID,
		Type:        "github_device_flow",
		AccessToken: token,
	})
}

// Device-flow poll error sentinels (#668).
var (
	errDeviceAuthPending = errors.New("authorization_pending")
	errDeviceSlowDown    = errors.New("slow_down")
)

// terminalDeviceFlowError marks OAuth errors that must abort the whole device
// flow (RFC 8628 §3.5: access_denied, expired_token — plus unrecognized OAuth
// errors). Network/transport failures are NOT terminal: the user may already
// have entered the user_code, so a transient blip must not invalidate it.
type terminalDeviceFlowError struct{ err error }

func (e *terminalDeviceFlowError) Error() string { return e.err.Error() }
func (e *terminalDeviceFlowError) Unwrap() error { return e.err }

// transientDeviceFlowError marks transport-level failures worth retrying:
// timeouts, connection resets/blips, HTTP 429/5xx. One transient failure
// must not waste an already-entered user_code (#668) — only the consecutive
// streak cap (#672) eventually gives up.
type transientDeviceFlowError struct{ err error }

func (e *transientDeviceFlowError) Error() string { return e.err.Error() }
func (e *transientDeviceFlowError) Unwrap() error { return e.err }

// permanentDeviceFlowError marks configuration-level failures that retrying
// cannot fix: DNS resolution failure (no such host), TLS/certificate errors,
// 4xx-non-429 HTTP statuses, non-JSON bodies, and 200-with-empty-token
// responses. #668's blanket retry regressed these into ~180 doomed requests
// across the whole 15-minute device-code lifetime; they abort immediately.
// (#672)
type permanentDeviceFlowError struct{ err error }

func (e *permanentDeviceFlowError) Error() string { return e.err.Error() }
func (e *permanentDeviceFlowError) Unwrap() error { return e.err }

// classifyTransportError inspects a raw transport error from the token poll
// and wraps it as transient or permanent (#672): a DNS lookup that came back
// authoritatively unresolved (NXDOMAIN) and TLS certificate verification
// failures are permanent (wrong hostname / misconfigured endpoint);
// everything else — including DNS hiccups (IsTemporary/IsTimeout) and plain
// timeouts — stays transient so a proxy blip cannot waste an already-entered
// user_code (#668).
func classifyTransportError(err error) error {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && !dnsErr.IsTemporary && !dnsErr.IsTimeout {
		return &permanentDeviceFlowError{err: err}
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return &permanentDeviceFlowError{err: err}
	}
	var unknownCA x509.UnknownAuthorityError
	if errors.As(err, &unknownCA) {
		return &permanentDeviceFlowError{err: err}
	}
	var hostErr x509.HostnameError
	if errors.As(err, &hostErr) {
		return &permanentDeviceFlowError{err: err}
	}
	return &transientDeviceFlowError{err: err}
}

// Device-flow poll tuning (#672).
const (
	// maxDevicePollIntervalSeconds caps the cumulative RFC 8628 §3.5
	// slow_down interval growth.
	maxDevicePollIntervalSeconds = 60
	// maxConsecutiveTransientPollErrors bounds how many consecutive
	// transient poll failures are retried before giving up, so a failure
	// misclassified as transient cannot burn the whole device-code lifetime.
	maxConsecutiveTransientPollErrors = 30
)

// growDevicePollInterval grows the poll interval by the RFC 8628 §3.5 delta
// (cumulative +5s), clamped to maxDevicePollIntervalSeconds. (#672)
func growDevicePollInterval(cur int) int {
	cur += 5
	if cur > maxDevicePollIntervalSeconds {
		cur = maxDevicePollIntervalSeconds
	}
	return cur
}

// transientStreakTracker counts consecutive transient token-poll failures
// (#672). authorization_pending does not count — it is the normal "user has
// not entered the code yet" state, not a failure.
type transientStreakTracker struct{ n int }

// record folds one poll error into the streak and reports whether the retry
// budget is exhausted (true → the caller must abort the flow).
func (t *transientStreakTracker) record(err error) bool {
	if errors.Is(err, errDeviceAuthPending) {
		t.n = 0
		return false
	}
	t.n++
	return t.n >= maxConsecutiveTransientPollErrors
}

type devicePollAction int

const (
	devicePollContinue devicePollAction = iota // keep polling at the current interval
	devicePollSlowDown                         // grow the interval per RFC 8628 §3.5
	devicePollAbort                            // terminal failure, abort the flow
)

// classifyDevicePollError decides how the token poll loop reacts to a
// checkToken error (#668, tightened by #672): terminal OAuth errors abort;
// authorization_pending continues at the same interval; slow_down grows the
// interval (cumulative +5s, capped); permanent configuration errors (DNS
// resolution failure, TLS, 4xx-non-429, non-JSON body, empty token) abort
// immediately instead of burning the device-code lifetime on doomed
// retries; anything else — transient transport failures — is retried until
// the device code expires (bounded by maxConsecutiveTransientPollErrors) so
// one transient blip cannot waste an already-entered user_code.
func classifyDevicePollError(err error) devicePollAction {
	switch {
	case errors.Is(err, errDeviceAuthPending):
		return devicePollContinue
	case errors.Is(err, errDeviceSlowDown):
		return devicePollSlowDown
	default:
		var te *terminalDeviceFlowError
		if errors.As(err, &te) {
			return devicePollAbort
		}
		var pe *permanentDeviceFlowError
		if errors.As(err, &pe) {
			return devicePollAbort
		}
		return devicePollContinue
	}
}
