package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// #4869 follow-up (user round-5, "create = verify"): credential creation
// must FAIL FAST - the createInput spec is validated against the QQ token
// endpoint BEFORE anything persists, so a bad appid/secret surfaces as an
// immediate red error in the panel instead of lurking until first use.

// qqCreateVerifyURL is the endpoint the create-time probe hits; a package
// seam so tests can point it at an httptest server.
var qqCreateVerifyURL = "https://bots.qq.com/app/getAppAccessToken"

// qqCreateVerifyTimeout bounds the create-time probe - a slow network must
// not freeze the panel interaction for the default client timeout.
const qqCreateVerifyTimeout = 10 * time.Second

// qqCreateVerifyClient is the HTTP seam for the probe (nil -> default).
var qqCreateVerifyClient = &http.Client{Timeout: qqCreateVerifyTimeout}

// validateQQCreateSpec validates the pasted "<bot-id> <appid> <appsecret>"
// spec against the QQ token endpoint. nil error means the credentials are
// live and the binding may be created; a non-nil error is the red-message
// text (already localized where possible; the network detail rides along).
func validateQQCreateSpec(botID, appID, appSecret string) error {
	if botID == "" || appID == "" || appSecret == "" {
		return fmt.Errorf("incomplete spec")
	}
	ctx, cancel := context.WithTimeout(context.Background(), qqCreateVerifyTimeout)
	defer cancel()
	payload := map[string]string{
		"appId":        appID,
		"clientSecret": appSecret,
	}
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		return fmt.Errorf("encode verify request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, qqCreateVerifyURL, &body)
	if err != nil {
		return fmt.Errorf("build verify request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := qqCreateVerifyClient
	if client == nil {
		client = &http.Client{Timeout: qqCreateVerifyTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("verify request failed (network): %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("QQ rejected the credentials (HTTP %d) - check appid/appsecret at bots.qq.com", resp.StatusCode)
	}
	// HTTP 200 = the endpoint accepted the credential pair. (QQ can return
	// 200 with an empty token on transient first-try flakiness - the
	// adapter's refresh-retry path absorbs that at use time; the create-time
	// gate only needs the credential-validity verdict.)
	return nil
}

// qqCreateLink renders the OSC8 clickable deep-link to the bot-app creation
// page shown beside the create input (logo.go OSC8 helper form).
func qqCreateLink(label string) string {
	return "\x1b]8;;https://bots.qq.com/app/manage\x1b\\" + label + "\x1b]8;;\x1b\\"
}
