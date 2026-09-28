package im

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

// Seams extracted from feishuAdapter.SendInteractive (r211, behavior-preserving
// split). All builders are pure and byte-for-byte replicate the pre-split
// card layout; sendFeishuInteractiveRequest replicates the rate-limit retry
// loop verbatim (same gate order, error strings and debug.Log calls).

// feishuInteractiveButtonColumn maps one InteractiveButton to a Feishu card
// column element: a button with a callback behavior and style-based type.
func feishuInteractiveButtonColumn(btn InteractiveButton) map[string]any {
	btnElem := map[string]any{
		"tag": "button",
		"text": map[string]any{
			"tag":     "plain_text",
			"content": btn.Label,
		},
		"behaviors": []any{
			map[string]any{
				"type":  "callback",
				"value": map[string]any{"choice": btn.Value},
			},
		},
	}
	switch btn.Style {
	case "primary":
		btnElem["type"] = "primary"
	case "danger":
		btnElem["type"] = "danger"
	default:
		btnElem["type"] = "default"
	}
	return map[string]any{
		"tag":      "column",
		"elements": []any{btnElem},
	}
}

// feishuInteractiveButtonColumns builds one column per button, in order.
func feishuInteractiveButtonColumns(buttons []InteractiveButton) []map[string]any {
	var columns []map[string]any
	for _, btn := range buttons {
		columns = append(columns, feishuInteractiveButtonColumn(btn))
	}
	return columns
}

// feishuInteractiveDoneColumn returns the fixed "✅ Done" primary column
// appended to multi-select cards; its callback carries the reserved
// "__done__" choice value.
func feishuInteractiveDoneColumn() map[string]any {
	return map[string]any{
		"tag": "column",
		"elements": []any{
			map[string]any{
				"tag":  "button",
				"type": "primary",
				"text": map[string]any{
					"tag":     "plain_text",
					"content": "✅ Done",
				},
				"behaviors": []any{
					map[string]any{
						"type":  "callback",
						"value": map[string]any{"choice": "__done__"},
					},
				},
			},
		},
	}
}

// buildFeishuInteractiveCard assembles the Feishu schema-2.0 card: a markdown
// element plus a button column set (with a trailing Done column when the
// message is a multi-select).
func buildFeishuInteractiveCard(msg InteractiveMessage) map[string]any {
	// Build card with markdown + button column set
	elements := []any{
		map[string]any{
			"tag":     "markdown",
			"content": msg.Text,
		},
	}

	// Build buttons as a column_set
	columns := feishuInteractiveButtonColumns(msg.Buttons)
	if msg.MultiSelect {
		columns = append(columns, feishuInteractiveDoneColumn())
	}

	if len(columns) > 0 {
		elements = append(elements, map[string]any{
			"tag":       "column_set",
			"flex_mode": "bisect",
			"columns":   columns,
		})
	}

	card := map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"body": map[string]any{
			"elements": elements,
		},
	}
	return card
}

// feishuInteractiveRequestBody wraps the marshalled card into the
// im/v1/messages request payload.
func feishuInteractiveRequestBody(chatID string, cardBytes []byte) []byte {
	body := map[string]any{
		"receive_id": chatID,
		"msg_type":   "interactive",
		"content":    string(cardBytes),
	}
	bodyBytes, _ := json.Marshal(body)
	return bodyBytes
}

// sendFeishuInteractiveRequest POSTs the interactive card with rate-limit
// retry (429 + Retry-After) and extracts message_id from the response,
// checking the code field for API-level errors.
func (a *feishuAdapter) sendFeishuInteractiveRequest(ctx context.Context, url, token string, bodyBytes []byte) (string, error) {
	for attempt := 0; attempt <= maxRateLimitRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := a.httpClient.Do(req)
		if err != nil {
			return "", err
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxRateLimitRetries {
			delay := parseRetryAfter(resp)
			resp.Body.Close()
			debug.Log("feishu", "adapter=%s SendInteractive rate-limited, retry %d/%d after %v",
				a.name, attempt+1, maxRateLimitRetries, delay)
			if err := sleepRetry(ctx, delay); err != nil {
				return "", err
			}
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			respBody, _ := util.ReadAll(resp.Body, util.ReadLimitGeneral)
			return "", fmt.Errorf("Feishu interactive API [%d] %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
		}

		// Extract message_id from response, checking code field for API errors.
		var result struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
			Data struct {
				MessageID string `json:"message_id"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
			if result.Code != 0 {
				return "", fmt.Errorf("Feishu interactive API error [%d]: %s", result.Code, result.Msg)
			}
			return strings.TrimSpace(result.Data.MessageID), nil
		}
		return "", nil
	}
	return "", rateLimitExhausted("Feishu")
}
