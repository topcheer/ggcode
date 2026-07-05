//go:build integration

package tool

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	chromeSkipOnce sync.Once
	chromeSkipMsg  string
)

// skipIfNoChrome skips the test if Chrome/Chromium is not available or the
// test site is unreachable. Uses sync.Once so the check runs only once per
// test binary execution, avoiding 12 separate Chrome launches.
func skipIfNoChrome(t *testing.T) {
	t.Helper()
	chromeSkipOnce.Do(func() {
		b := NewBrowser()
		defer b.doCloseSession("default", "itest")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		input, _ := json.Marshal(map[string]interface{}{
			"action":      "navigate",
			"url":         "about:blank",
			"description": "chrome availability check",
		})
		_, err := b.Execute(ctx, input)
		if err != nil {
			chromeSkipMsg = "Chrome/Chromium not available: " + err.Error()
			return
		}
		ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel2()
		input2, _ := json.Marshal(map[string]interface{}{
			"action":      "navigate",
			"url":         testSite,
			"description": "test site reachability check",
		})
		result, err := b.Execute(ctx2, input2)
		if err != nil || result.IsError {
			chromeSkipMsg = "test site " + testSite + " not reachable"
			return
		}
	})
	if chromeSkipMsg != "" {
		t.Skip(chromeSkipMsg)
	}
}

// runBrowserAction is a helper that executes a browser action and returns the result.
func runBrowserAction(t *testing.T, b *Browser, action string, extra map[string]interface{}) Result {
	t.Helper()
	args := map[string]interface{}{
		"action":      action,
		"description": "integration test: " + action,
	}
	for k, v := range extra {
		args[k] = v
	}
	input, _ := json.Marshal(args)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	result, err := b.Execute(ctx, input)
	if err != nil {
		t.Fatalf("action %s returned error: %v", action, err)
	}
	return result
}

const testSite = "https://www.facebook.com"

// TestBrowserIntegration_NavigateContent tests navigate + content actions against Facebook.
// Facebook is a heavy SPA — this validates real-world JavaScript rendering.
func TestBrowserIntegration_NavigateContent(t *testing.T) {
	skipIfNoChrome(t)
	b := NewBrowser()
	defer b.doCloseSession("default", "itest")

	result := runBrowserAction(t, b, "navigate", map[string]interface{}{
		"url": testSite,
	})
	if result.IsError {
		t.Fatalf("navigate failed: %s", result.Content)
	}

	result = runBrowserAction(t, b, "content", nil)
	if result.IsError {
		t.Fatalf("content failed: %s", result.Content)
	}
	// Facebook should render something — title or content
	if len(result.Content) < 50 {
		t.Errorf("content too short for a SPA page: %s", result.Content)
	}
}

// TestBrowserIntegration_NavigateWaitFor verifies SPA dynamic content via wait_for.
func TestBrowserIntegration_NavigateWaitFor(t *testing.T) {
	skipIfNoChrome(t)
	b := NewBrowser()
	defer b.doCloseSession("default", "itest")

	result := runBrowserAction(t, b, "navigate", map[string]interface{}{
		"url":      testSite,
		"wait_for": "body",
	})
	if result.IsError {
		t.Fatalf("navigate with wait_for failed: %s", result.Content)
	}
}

// TestBrowserIntegration_Extract tests element extraction.
func TestBrowserIntegration_Extract(t *testing.T) {
	skipIfNoChrome(t)
	b := NewBrowser()
	defer b.doCloseSession("default", "itest")

	runBrowserAction(t, b, "navigate", map[string]interface{}{"url": testSite})

	// Extract all links
	result := runBrowserAction(t, b, "extract", map[string]interface{}{
		"selector": "a",
	})
	if result.IsError {
		t.Fatalf("extract a failed: %s", result.Content)
	}
	// Facebook should have links
	if !strings.Contains(result.Content, "[a") {
		t.Errorf("extract a should find link elements: %s", result.Content)
	}

	// Extract full page text (no selector)
	result = runBrowserAction(t, b, "extract", nil)
	if result.IsError {
		t.Fatalf("extract full page failed: %s", result.Content)
	}
	if len(result.Content) < 50 {
		t.Errorf("full page extract too short: %s", result.Content)
	}
}

// TestBrowserIntegration_Links tests links action.
func TestBrowserIntegration_Links(t *testing.T) {
	skipIfNoChrome(t)
	b := NewBrowser()
	defer b.doCloseSession("default", "itest")

	runBrowserAction(t, b, "navigate", map[string]interface{}{"url": testSite})
	result := runBrowserAction(t, b, "links", nil)
	if result.IsError {
		t.Fatalf("links failed: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Links on") {
		t.Errorf("links output unexpected: %s", result.Content)
	}
}

// TestBrowserIntegration_Evaluate tests JS evaluation.
func TestBrowserIntegration_Evaluate(t *testing.T) {
	skipIfNoChrome(t)
	b := NewBrowser()
	defer b.doCloseSession("default", "itest")

	runBrowserAction(t, b, "navigate", map[string]interface{}{"url": testSite})

	// Get title via JS
	result := runBrowserAction(t, b, "evaluate", map[string]interface{}{
		"expression": "document.title",
	})
	if result.IsError {
		t.Fatalf("evaluate title failed: %s", result.Content)
	}
	// Facebook title should contain "Facebook"
	if !strings.Contains(strings.ToLower(result.Content), "facebook") {
		t.Logf("title was: %s (Facebook may have changed title)", result.Content)
	}

	// Arithmetic
	result = runBrowserAction(t, b, "evaluate", map[string]interface{}{
		"expression": "6 * 7",
	})
	if result.IsError {
		t.Fatalf("evaluate arithmetic failed: %s", result.Content)
	}
	if !strings.Contains(result.Content, "42") {
		t.Errorf("evaluate 6*7 should be 42: %s", result.Content)
	}

	// DOM query — count forms/inputs
	result = runBrowserAction(t, b, "evaluate", map[string]interface{}{
		"expression": "document.querySelectorAll('input').length",
	})
	if result.IsError {
		t.Fatalf("evaluate input count failed: %s", result.Content)
	}
	t.Logf("Facebook has %s input elements", result.Content)
}

// TestBrowserIntegration_Type tests typing into a form field.
func TestBrowserIntegration_Type(t *testing.T) {
	skipIfNoChrome(t)
	b := NewBrowser()
	defer b.doCloseSession("default", "itest")

	runBrowserAction(t, b, "navigate", map[string]interface{}{"url": testSite})

	// Facebook login page has email/password inputs
	// Type into the email field
	result := runBrowserAction(t, b, "type", map[string]interface{}{
		"selector": "input[name=email]",
		"text":     "test@example.com",
	})
	if result.IsError {
		t.Fatalf("type into email failed: %s", result.Content)
	}

	// Verify the text was typed
	result = runBrowserAction(t, b, "evaluate", map[string]interface{}{
		"expression": `document.querySelector('input[name=email]').value`,
	})
	if result.IsError {
		t.Fatalf("verify type failed: %s", result.Content)
	}
	if !strings.Contains(result.Content, "test@example.com") {
		t.Errorf("input should contain 'test@example.com': %s", result.Content)
	}
}

// TestBrowserIntegration_Scroll tests scroll action.
func TestBrowserIntegration_Scroll(t *testing.T) {
	skipIfNoChrome(t)
	b := NewBrowser()
	defer b.doCloseSession("default", "itest")

	runBrowserAction(t, b, "navigate", map[string]interface{}{"url": testSite})

	result := runBrowserAction(t, b, "scroll", nil)
	if result.IsError {
		t.Fatalf("scroll failed: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Scrolled") {
		t.Errorf("scroll output: %s", result.Content)
	}
}

// TestBrowserIntegration_Screenshot tests PNG screenshot capture.
func TestBrowserIntegration_Screenshot(t *testing.T) {
	skipIfNoChrome(t)
	b := NewBrowser()
	defer b.doCloseSession("default", "itest")

	runBrowserAction(t, b, "navigate", map[string]interface{}{"url": testSite})

	result := runBrowserAction(t, b, "screenshot", nil)
	if result.IsError {
		t.Fatalf("screenshot failed: %s", result.Content)
	}
	if len(result.Images) == 0 {
		t.Fatal("screenshot should return an image")
	}
	if len(result.Images[0].Base64) < 1000 {
		t.Errorf("screenshot base64 data too small: %d chars", len(result.Images[0].Base64))
	}
	t.Logf("captured %d chars of base64 image data", len(result.Images[0].Base64))
}

// TestBrowserIntegration_WaitTimeout tests wait timeout for missing element.
func TestBrowserIntegration_WaitTimeout(t *testing.T) {
	skipIfNoChrome(t)
	b := NewBrowser()
	defer b.doCloseSession("default", "itest")

	runBrowserAction(t, b, "navigate", map[string]interface{}{"url": testSite})

	result := runBrowserAction(t, b, "wait", map[string]interface{}{
		"wait_for":     "#this-element-does-not-exist-xyz123",
		"wait_timeout": 3,
	})
	if !result.IsError {
		t.Error("wait should timeout for non-existent element")
	}
	if !strings.Contains(result.Content, "timed out") {
		t.Errorf("wait timeout message unexpected: %s", result.Content)
	}
}

// TestBrowserIntegration_Back tests back navigation.
func TestBrowserIntegration_Back(t *testing.T) {
	skipIfNoChrome(t)
	b := NewBrowser()
	defer b.doCloseSession("default", "itest")

	// Navigate to Facebook
	runBrowserAction(t, b, "navigate", map[string]interface{}{"url": testSite})

	// Navigate to a different page
	runBrowserAction(t, b, "navigate", map[string]interface{}{"url": "https://www.google.com"})

	// Go back
	result := runBrowserAction(t, b, "back", nil)
	if result.IsError {
		t.Fatalf("back failed: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Navigated back") {
		t.Errorf("back output unexpected: %s", result.Content)
	}
}

// TestBrowserIntegration_MultiSession tests multiple independent sessions (tabs).
func TestBrowserIntegration_MultiSession(t *testing.T) {
	skipIfNoChrome(t)
	b := NewBrowser()
	defer b.doCloseSession("default", "sess-a")
	defer b.doCloseSession("default", "sess-b")

	// Session A: Facebook
	result := runBrowserAction(t, b, "navigate", map[string]interface{}{
		"url":     testSite,
		"session": "sess-a",
	})
	if result.IsError {
		t.Fatalf("sess-a navigate: %s", result.Content)
	}

	// Session B: Google
	result = runBrowserAction(t, b, "navigate", map[string]interface{}{
		"url":     "https://www.google.com",
		"session": "sess-b",
	})
	if result.IsError {
		t.Fatalf("sess-b navigate: %s", result.Content)
	}

	// Verify session A still on Facebook
	result = runBrowserAction(t, b, "evaluate", map[string]interface{}{
		"session":    "sess-a",
		"expression": "window.location.hostname",
	})
	if result.IsError {
		t.Fatalf("sess-a evaluate: %s", result.Content)
	}
	if !strings.Contains(result.Content, "facebook") {
		t.Errorf("sess-a should be on facebook: %s", result.Content)
	}

	// Verify session B on Google
	result = runBrowserAction(t, b, "evaluate", map[string]interface{}{
		"session":    "sess-b",
		"expression": "window.location.hostname",
	})
	if result.IsError {
		t.Fatalf("sess-b evaluate: %s", result.Content)
	}
	if !strings.Contains(result.Content, "google") {
		t.Errorf("sess-b should be on google: %s", result.Content)
	}
}

// TestBrowserIntegration_Close tests close action.
func TestBrowserIntegration_Close(t *testing.T) {
	skipIfNoChrome(t)
	b := NewBrowser()

	runBrowserAction(t, b, "navigate", map[string]interface{}{
		"url":     testSite,
		"session": "close-test",
	})

	result := runBrowserAction(t, b, "close", map[string]interface{}{
		"session": "close-test",
	})
	if result.IsError {
		t.Fatalf("close failed: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Closed session") {
		t.Errorf("close output: %s", result.Content)
	}
}
