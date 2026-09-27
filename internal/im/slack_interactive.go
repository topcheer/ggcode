package im

// Slack interactive (Block Kit) outbound phase seams, extracted verbatim from
// slackAdapter.SendInteractive (r197) to keep the orchestrator flat. Behavior
// is pinned by slack_interactive_seams_test.go; #968/#1237 comment anchors
// and error literals are preserved word-for-word.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// slackInteractiveButtonElement maps one InteractiveButton to a Block Kit
// button element. Only "primary" and "danger" styles survive; every other
// style hint collapses to "".
func slackInteractiveButtonElement(btn InteractiveButton) map[string]any {
	style := ""
	switch btn.Style {
	case "primary":
		style = "primary"
	case "danger":
		style = "danger"
	}
	return map[string]any{
		"type":  "button",
		"text":  map[string]any{"type": "plain_text", "text": btn.Label},
		"value": btn.Value,
		"style": style,
	}
}

// slackInteractiveActionElements builds the actions row: one element per
// button, plus the trailing "✅ Done" button when MultiSelect is set. The
// returned slice may be nil (no buttons, no MultiSelect), matching the
// original inline construction.
func slackInteractiveActionElements(msg InteractiveMessage) []map[string]any {
	var elements []map[string]any
	for _, btn := range msg.Buttons {
		elements = append(elements, slackInteractiveButtonElement(btn))
	}
	if msg.MultiSelect {
		elements = append(elements, map[string]any{
			"type":  "button",
			"text":  map[string]any{"type": "plain_text", "text": "✅ Done"},
			"value": "__done__",
			"style": "primary",
		})
	}
	return elements
}

// slackInteractiveBlocks assembles the Block Kit payload: a mrkdwn section
// followed by the actions row.
func slackInteractiveBlocks(msg InteractiveMessage) []map[string]any {
	// Build Block Kit message
	textBlock := map[string]any{
		"type": "section",
		"text": map[string]any{
			"type": "mrkdwn",
			"text": markdownToMrkdwn(msg.Text),
		},
	}

	elements := slackInteractiveActionElements(msg)

	actionsBlock := map[string]any{
		"type":     "actions",
		"elements": elements,
	}

	return []map[string]any{textBlock, actionsBlock}
}

// postInteractiveMessage POSTs the assembled blocks to chat.postMessage with
// the shared rate-limit retry policy: HTTP 429 + Retry-After backoff, plus
// the #1237 200-body "ratelimited" shape.
func (a *slackAdapter) postInteractiveMessage(ctx context.Context, channelID, threadID string, blocks []map[string]any) (string, error) {
	// Respect the apiBase test override like every other call site (#968) —
	// SendInteractive was the one path still pinned to the production URL,
	// which is also why its missing ratelimited retry went unnoticed (#1237).
	baseURL := slackAPIBase
	if a.apiBase != "" {
		baseURL = a.apiBase
	}
	url := baseURL + "/chat.postMessage"
	body := map[string]any{
		"channel": channelID,
		"blocks":  blocks,
	}
	if strings.TrimSpace(threadID) != "" {
		body["thread_ts"] = threadID
	}
	bodyBytes, _ := json.Marshal(body)

	for attempt := 0; attempt <= maxRateLimitRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+a.botToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err := a.httpClient.Do(req)
		if err != nil {
			return "", err
		}

		// Handle HTTP 429 rate limit with Retry-After backoff.
		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			if attempt < maxRateLimitRetries {
				delay := parseRetryAfter(resp)
				debug.Log("slack", "adapter=%s interactive 429 rate limited, retry %d/%d in %v",
					a.name, attempt+1, maxRateLimitRetries, delay)
				if err := sleepRetry(ctx, delay); err != nil {
					return "", err
				}
				continue
			}
			return "", rateLimitExhausted("Slack")
		}

		var result map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return "", err
		}
		resp.Body.Close()
		if ok, _ := result["ok"].(bool); !ok {
			errMsg, _ := result["error"].(string)
			// #1237: Slack also signals rate limiting as ok=false with
			// error "ratelimited" inside a 200 body (no Retry-After header
			// in this form). sendChannelMessage already retries this shape;
			// the interactive path is the twin that was missed, silently
			// dropping approval questionnaires while ask_user waited
			// forever for a callback that never came.
			if errMsg == "ratelimited" && attempt < maxRateLimitRetries {
				debug.Log("slack", "adapter=%s interactive ratelimited (200 body), retrying (attempt %d/%d)",
					a.name, attempt+1, maxRateLimitRetries)
				if err := sleepRetry(ctx, defaultRetryDelay); err != nil {
					return "", err
				}
				continue
			}
			return "", fmt.Errorf("Slack chat.postMessage: %s", errMsg)
		}
		// Extract ts (message ID)
		messageTs, _ := result["ts"].(string)
		return messageTs, nil
	}
	return "", rateLimitExhausted("Slack")
}
