//go:build integration

package im

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// findTGAdapterConfig finds the first Telegram adapter config in the user's config.
func findTGAdapterConfig(t *testing.T, cfg *config.Config) (string, config.IMAdapterConfig, bool) {
	t.Helper()
	for name, adapter := range cfg.IM.Adapters {
		if adapter.Platform == "telegram" && adapter.Enabled {
			return name, adapter, true
		}
	}
	return "", config.IMAdapterConfig{}, false
}

// TestE2ETGBotGetMe verifies the Telegram bot token works by calling getMe.
//
// Set GGCODE_E2E=1 to run. Requires:
//   - ~/.ggcode/ggcode.yaml with a Telegram adapter config (bot_token)
//
// This test does NOT send any messages — it only verifies the bot token.
func TestE2ETGBotGetMe(t *testing.T) {
	if os.Getenv("GGCODE_E2E") == "" {
		t.Skip("GGCODE_E2E not set, skipping")
	}

	cfg := loadE2EConfig(t)
	if cfg == nil {
		t.Skip("config not found")
	}

	adapterName, adapterCfg, found := findTGAdapterConfig(t, cfg)
	if !found {
		t.Skip("no enabled Telegram adapter")
	}

	adapter, err := newTGAdapter(adapterName, cfg.IM, adapterCfg, nil)
	if err != nil {
		t.Fatalf("create TG adapter: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := adapter.getMe(ctx)
	if err != nil {
		t.Fatalf("getMe: %v", err)
	}

	// Telegram API returns {"ok":true,"result":{"username":"...",...}}
	// getMe returns the full response map; username is nested under "result"
	username := ""
	if sub, ok := result["result"].(map[string]any); ok {
		username, _ = sub["username"].(string)
	}
	if username == "" {
		// Try top-level (in case apiRequest already unwraps)
		username, _ = result["username"].(string)
	}
	if username == "" {
		t.Errorf("getMe returned no username: %v", result)
	}
	t.Logf("Bot @%s connected (adapter=%s, apiBase=%s)", username, adapterName, adapter.apiBase)
}

// TestE2ETGSendTextMessages sends multiple text messages to a real Telegram chat.
//
// Set GGCODE_E2E=1 to run. Requires:
//   - ~/.ggcode/ggcode.yaml with a Telegram adapter config (bot_token)
//   - GGCODE_E2E_TG_CHAT_ID set to a Telegram chat ID (e.g., your user ID or group ID)
//
// This test sends real messages — check Telegram to verify.
func TestE2ETGSendTextMessages(t *testing.T) {
	if os.Getenv("GGCODE_E2E") == "" {
		t.Skip("GGCODE_E2E not set, skipping")
	}

	cfg := loadE2EConfig(t)
	if cfg == nil {
		t.Skip("config not found")
	}

	adapterName, adapterCfg, found := findTGAdapterConfig(t, cfg)
	if !found {
		t.Skip("no enabled Telegram adapter")
	}

	adapter, err := newTGAdapter(adapterName, cfg.IM, adapterCfg, nil)
	if err != nil {
		t.Fatalf("create TG adapter: %v", err)
	}

	chatID := os.Getenv("GGCODE_E2E_TG_CHAT_ID")
	if chatID == "" {
		t.Skip("GGCODE_E2E_TG_CHAT_ID not set (set to a Telegram chat ID to send test messages to)")
	}

	// Mark as connected for Send to work
	adapter.mu.Lock()
	adapter.connected = true
	adapter.mu.Unlock()

	ctx := context.Background()

	// Verify bot token first
	if _, err := adapter.getMe(ctx); err != nil {
		t.Fatalf("getMe failed: %v", err)
	}

	scenarios := []struct {
		name    string
		content string
	}{
		{
			name:    "01_plain_text",
			content: "E2E 测试 #1：纯文本消息",
		},
		{
			name:    "02_multiline",
			content: "E2E 测试 #2：多行消息\n第二行\n第三行\n完成。",
		},
		{
			name:    "03_code_block",
			content: "E2E 测试 #3：代码块\n\n```go\nfunc main() {\n    fmt.Println(\"hello\")\n}\n```\n\n结束。",
		},
		{
			name:    "04_emoji_unicode",
			content: "E2E 测试 #4：Emoji ✅🎉🚀💡 你好世界 こんにちは",
		},
		{
			name:    "05_long_text",
			content: "E2E 测试 #5：长文本消息。" + strings.Repeat("这是重复内容。", 50),
		},
	}

	passed := 0
	failed := 0
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			binding := ChannelBinding{
				ChannelID: chatID,
				Adapter:   adapterName,
			}

			sendErr := adapter.Send(ctx, binding, OutboundEvent{
				Kind: OutboundEventText,
				Text: sc.content,
			})
			if sendErr != nil {
				t.Errorf("Send failed: %v", sendErr)
				failed++
				return
			}
			passed++
			t.Logf("Sent OK (%d bytes)", len(sc.content))

			time.Sleep(2 * time.Second)
		})
	}

	t.Logf("\n=== TG E2E Results: %d/%d passed, %d failed ===", passed, len(scenarios), failed)
	if failed > 0 {
		t.Errorf("%d scenarios failed", failed)
	}
}

// TestE2ETGSendStatusAndApproval tests sending status and approval events to Telegram.
//
// Set GGCODE_E2E=1 to run. Requires:
//   - ~/.ggcode/ggcode.yaml with a Telegram adapter config (bot_token)
//   - GGCODE_E2E_TG_CHAT_ID set to a Telegram chat ID
func TestE2ETGSendStatusAndApproval(t *testing.T) {
	if os.Getenv("GGCODE_E2E") == "" {
		t.Skip("GGCODE_E2E not set, skipping")
	}

	cfg := loadE2EConfig(t)
	if cfg == nil {
		t.Skip("config not found")
	}

	adapterName, adapterCfg, found := findTGAdapterConfig(t, cfg)
	if !found {
		t.Skip("no enabled Telegram adapter")
	}

	adapter, err := newTGAdapter(adapterName, cfg.IM, adapterCfg, nil)
	if err != nil {
		t.Fatalf("create TG adapter: %v", err)
	}

	chatID := os.Getenv("GGCODE_E2E_TG_CHAT_ID")
	if chatID == "" {
		t.Skip("GGCODE_E2E_TG_CHAT_ID not set")
	}

	adapter.mu.Lock()
	adapter.connected = true
	adapter.mu.Unlock()

	ctx := context.Background()

	scenarios := []struct {
		name  string
		event OutboundEvent
	}{
		{
			name:  "status_thinking",
			event: OutboundEvent{Kind: OutboundEventStatus, Status: "🤔 正在思考..."},
		},
		{
			name:  "status_working",
			event: OutboundEvent{Kind: OutboundEventStatus, Status: "⚡ 正在执行 bash 命令..."},
		},
		{
			name: "approval_request",
			event: OutboundEvent{
				Kind: OutboundEventApprovalRequest,
				Approval: &ApprovalRequest{
					ID:       "appr-001",
					ToolName: "bash",
					Input:    "rm -rf /tmp/test",
				},
			},
		},
		{
			name: "approval_result",
			event: OutboundEvent{
				Kind: OutboundEventApprovalResult,
				Result: &ApprovalResult{
					Decision:    1, // permission.Allow
					RespondedBy: "test-user",
				},
			},
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			binding := ChannelBinding{
				ChannelID: chatID,
				Adapter:   adapterName,
			}

			sendErr := adapter.Send(ctx, binding, sc.event)
			if sendErr != nil {
				t.Errorf("Send failed: %v", sendErr)
				return
			}
			t.Logf("Sent OK: %s", sc.name)
			time.Sleep(2 * time.Second)
		})
	}
}

// TestE2ETGSendSplitMessage tests that long messages are split correctly.
//
// Set GGCODE_E2E=1 to run. Requires:
//   - ~/.ggcode/ggcode.yaml with a Telegram adapter config (bot_token)
//   - GGCODE_E2E_TG_CHAT_ID set to a Telegram chat ID
func TestE2ETGSendSplitMessage(t *testing.T) {
	if os.Getenv("GGCODE_E2E") == "" {
		t.Skip("GGCODE_E2E not set, skipping")
	}

	cfg := loadE2EConfig(t)
	if cfg == nil {
		t.Skip("config not found")
	}

	adapterName, adapterCfg, found := findTGAdapterConfig(t, cfg)
	if !found {
		t.Skip("no enabled Telegram adapter")
	}

	adapter, err := newTGAdapter(adapterName, cfg.IM, adapterCfg, nil)
	if err != nil {
		t.Fatalf("create TG adapter: %v", err)
	}

	chatID := os.Getenv("GGCODE_E2E_TG_CHAT_ID")
	if chatID == "" {
		t.Skip("GGCODE_E2E_TG_CHAT_ID not set")
	}

	adapter.mu.Lock()
	adapter.connected = true
	adapter.mu.Unlock()

	ctx := context.Background()

	// Create a message longer than 4096 chars (TG max)
	longText := fmt.Sprintf("E2E 测试：长消息分割（共 %d 字符）\n\n", 5000)
	for i := 1; i <= 100; i++ {
		longText += fmt.Sprintf("第 %d 行：这是一段很长的消息内容，用于测试 Telegram 消息分割功能是否正常工作。\n", i)
	}
	t.Logf("Message length: %d chars", len(longText))

	binding := ChannelBinding{
		ChannelID: chatID,
		Adapter:   adapterName,
	}

	sendErr := adapter.Send(ctx, binding, OutboundEvent{
		Kind: OutboundEventText,
		Text: longText,
	})
	if sendErr != nil {
		t.Fatalf("Send long message failed: %v", sendErr)
	}
	t.Log("Long message split and sent OK")
}

// TestE2ETGBotWithCustomAPIRoot tests TG adapter with custom API root (if configured).
func TestE2ETGBotWithCustomAPIRoot(t *testing.T) {
	if os.Getenv("GGCODE_E2E") == "" {
		t.Skip("GGCODE_E2E not set, skipping")
	}

	cfg := loadE2EConfig(t)
	if cfg == nil {
		t.Skip("config not found")
	}

	adapterName, adapterCfg, found := findTGAdapterConfig(t, cfg)
	if !found {
		t.Skip("no enabled Telegram adapter")
	}

	adapter, err := newTGAdapter(adapterName, cfg.IM, adapterCfg, nil)
	if err != nil {
		t.Fatalf("create TG adapter: %v", err)
	}

	t.Logf("API base: %s (default: %s)", adapter.apiBase, tgDefaultAPIBase)

	// Verify connectivity regardless of API root
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := adapter.getMe(ctx)
	if err != nil {
		t.Fatalf("getMe with apiBase=%s: %v", adapter.apiBase, err)
	}

	username := ""
	if sub, ok := result["result"].(map[string]any); ok {
		username, _ = sub["username"].(string)
	}
	t.Logf("Bot @%s reachable via %s", username, adapter.apiBase)
}

// A small 1x1 red PNG for image tests (same as QQ E2E tests).
const tgTestPngBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8/5+hHgAHggJ/PchI7wAAAABJRU5ErkJggg=="

// tgSetupAdapter is a helper that creates and verifies a TG adapter for E2E tests.
func tgSetupAdapter(t *testing.T) (*tgAdapter, string, string) {
	t.Helper()
	if os.Getenv("GGCODE_E2E") == "" {
		t.Skip("GGCODE_E2E not set, skipping")
	}

	cfg := loadE2EConfig(t)
	if cfg == nil {
		t.Skip("config not found")
	}

	adapterName, adapterCfg, found := findTGAdapterConfig(t, cfg)
	if !found {
		t.Skip("no enabled Telegram adapter")
	}

	adapter, err := newTGAdapter(adapterName, cfg.IM, adapterCfg, nil)
	if err != nil {
		t.Fatalf("create TG adapter: %v", err)
	}

	chatID := os.Getenv("GGCODE_E2E_TG_CHAT_ID")
	if chatID == "" {
		t.Skip("GGCODE_E2E_TG_CHAT_ID not set")
	}

	adapter.mu.Lock()
	adapter.connected = true
	adapter.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := adapter.getMe(ctx); err != nil {
		t.Fatalf("getMe: %v", err)
	}

	return adapter, adapterName, chatID
}

// TestE2ETGSendDataUrlImage sends a message with an embedded data:image/png;base64,... image.
//
// Set GGCODE_E2E=1 and GGCODE_E2E_TG_CHAT_ID to run.
// This test sends a real image message — check Telegram to verify.
func TestE2ETGSendDataUrlImage(t *testing.T) {
	adapter, adapterName, chatID := tgSetupAdapter(t)
	ctx := context.Background()

	content := fmt.Sprintf("E2E 图片测试 #1：data URL 图片\n\n![test](data:image/png;base64,%s)\n\n图片已发送。", tgTestPngBase64)

	binding := ChannelBinding{ChannelID: chatID, Adapter: adapterName}
	if err := adapter.Send(ctx, binding, OutboundEvent{Kind: OutboundEventText, Text: content}); err != nil {
		t.Fatalf("Send data URL image: %v", err)
	}
	t.Log("Data URL image sent OK")
}

// TestE2ETGSendTextWithMultipleImages sends a message with multiple embedded images.
func TestE2ETGSendTextWithMultipleImages(t *testing.T) {
	adapter, adapterName, chatID := tgSetupAdapter(t)
	ctx := context.Background()

	content := fmt.Sprintf("E2E 图片测试 #2：多张图片\n\n![img1](data:image/png;base64,%s)\n\n![img2](data:image/png;base64,%s)\n\n两张图片发送完毕。", tgTestPngBase64, tgTestPngBase64)

	binding := ChannelBinding{ChannelID: chatID, Adapter: adapterName}
	if err := adapter.Send(ctx, binding, OutboundEvent{Kind: OutboundEventText, Text: content}); err != nil {
		t.Fatalf("Send multiple images: %v", err)
	}
	t.Log("Multiple images sent OK")
}

// TestE2ETGSendImagePlusText sends a message with an image followed by analysis text.
func TestE2ETGSendImagePlusText(t *testing.T) {
	adapter, adapterName, chatID := tgSetupAdapter(t)
	ctx := context.Background()

	content := fmt.Sprintf("E2E 图片测试 #3：图片+文字\n\n分析结果：数据呈上升趋势。\n\n![chart](data:image/png;base64,%s)\n\n以上是分析图表，请查看。", tgTestPngBase64)

	binding := ChannelBinding{ChannelID: chatID, Adapter: adapterName}
	if err := adapter.Send(ctx, binding, OutboundEvent{Kind: OutboundEventText, Text: content}); err != nil {
		t.Fatalf("Send image+text: %v", err)
	}
	t.Log("Image + text sent OK")
}

// TestE2ETGSendPhotoByUpload directly tests sendPhotoByUpload with raw image bytes.
func TestE2ETGSendPhotoByUpload(t *testing.T) {
	adapter, _, chatID := tgSetupAdapter(t)
	ctx := context.Background()

	pngData, err := base64.StdEncoding.DecodeString(tgTestPngBase64)
	if err != nil {
		t.Fatalf("decode test PNG: %v", err)
	}

	if err := adapter.sendPhotoByUpload(ctx, chatID, pngData, "test_image.png", "E2E 测试：直接上传图片", ""); err != nil {
		t.Fatalf("sendPhotoByUpload: %v", err)
	}
	t.Log("Photo upload sent OK")
}

// TestE2ETGSendPhotoByURL tests sending a photo via public URL.
// Uses httpbin.org/image/png as a publicly accessible test image.
func TestE2ETGSendPhotoByURL(t *testing.T) {
	adapter, _, chatID := tgSetupAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	imageURL := "https://httpbin.org/image/png"
	if err := adapter.sendPhotoByURL(ctx, chatID, imageURL, "E2E 测试：URL 图片", ""); err != nil {
		t.Fatalf("sendPhotoByURL: %v", err)
	}
	t.Log("Photo by URL sent OK")
}

// TestE2ETGSendLocalFileImage tests sending an image from a local file path.
func TestE2ETGSendLocalFileImage(t *testing.T) {
	adapter, adapterName, chatID := tgSetupAdapter(t)
	ctx := context.Background()

	// Create a temp PNG file
	tmpDir := t.TempDir()
	pngPath := filepath.Join(tmpDir, "local_test.png")
	pngData, _ := base64.StdEncoding.DecodeString(tgTestPngBase64)
	if err := os.WriteFile(pngPath, pngData, 0o644); err != nil {
		t.Fatalf("write temp PNG: %v", err)
	}

	content := fmt.Sprintf("E2E 图片测试 #4：本地文件图片\n\n![local](%s)\n\n本地文件图片已发送。", pngPath)

	binding := ChannelBinding{ChannelID: chatID, Adapter: adapterName}
	if err := adapter.Send(ctx, binding, OutboundEvent{Kind: OutboundEventText, Text: content}); err != nil {
		t.Fatalf("Send local file image: %v", err)
	}
	t.Logf("Local file image sent OK (path=%s)", pngPath)
}

// TestE2ETGSendMixedContent sends a mixed content message with text, code, and multiple images.
func TestE2ETGSendMixedContent(t *testing.T) {
	adapter, adapterName, chatID := tgSetupAdapter(t)
	ctx := context.Background()

	content := fmt.Sprintf("E2E 图片测试 #5：混合内容\n\n1. 纯文本段落\n2. 代码: `x = 42`\n3. 图片:\n\n![first](data:image/png;base64,%s)\n\n4. 另一张图片:\n\n![second](data:image/png;base64,%s)\n\n✅ 所有内容测试完成！", tgTestPngBase64, tgTestPngBase64)

	binding := ChannelBinding{ChannelID: chatID, Adapter: adapterName}
	if err := adapter.Send(ctx, binding, OutboundEvent{Kind: OutboundEventText, Text: content}); err != nil {
		t.Fatalf("Send mixed content: %v", err)
	}
	t.Log("Mixed content sent OK")
}

// TestE2ETGSend10ImageScenarios sends 10 different message types with images to a real TG chat.
func TestE2ETGSend10ImageScenarios(t *testing.T) {
	adapter, adapterName, chatID := tgSetupAdapter(t)
	ctx := context.Background()

	scenarios := []struct {
		name    string
		content string
	}{
		{
			name:    "01_plain_text",
			content: "TG E2E 测试 #1：纯文本消息（无图片）",
		},
		{
			name:    "02_data_url_image",
			content: fmt.Sprintf("TG E2E 测试 #2：data URL 图片\n\n![test](data:image/png;base64,%s)\n\n完成。", tgTestPngBase64),
		},
		{
			name:    "03_multiline_text",
			content: "TG E2E 测试 #3：多行文本\n第二行内容\n第三行内容\n完成。",
		},
		{
			name:    "04_text_with_code_block",
			content: "TG E2E 测试 #4：包含代码块\n\n```go\nfunc main() {\n    fmt.Println(\"hello\")\n}\n```\n\n代码结束。",
		},
		{
			name:    "05_image_plus_text",
			content: fmt.Sprintf("TG E2E 测试 #5：图片+文字\n分析结果如下：\n\n![chart](data:image/png;base64,%s)\n\n以上是分析图表。", tgTestPngBase64),
		},
		{
			name:    "06_chinese_special_chars",
			content: "TG E2E 测试 #6：中文特殊字符 ~!@#$%%^&*()_+-={}[]|\\:\";'<>?,./",
		},
		{
			name:    "07_long_text",
			content: "TG E2E 测试 #7：长文本消息。" + strings.Repeat(" 重复内容。", 20),
		},
		{
			name:    "08_emoji_unicode",
			content: "TG E2E 测试 #8：Emoji ✅🎉🚀💡 你好世界 こんにちは 안녕하세요",
		},
		{
			name:    "09_multiple_images",
			content: fmt.Sprintf("TG E2E 测试 #9：多张图片\n\n![img1](data:image/png;base64,%s)\n\n![img2](data:image/png;base64,%s)\n\n两张图。", tgTestPngBase64, tgTestPngBase64),
		},
		{
			name:    "10_mixed_content",
			content: fmt.Sprintf("TG E2E 测试 #10：混合内容\n\n1. 纯文本\n2. 代码: `x = 42`\n3. 图片:\n\n![final](data:image/png;base64,%s)\n\n✅ 完成！", tgTestPngBase64),
		},
	}

	passed := 0
	failed := 0
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			binding := ChannelBinding{ChannelID: chatID, Adapter: adapterName}

			if err := adapter.Send(ctx, binding, OutboundEvent{Kind: OutboundEventText, Text: sc.content}); err != nil {
				t.Errorf("Send failed: %v", err)
				failed++
				return
			}
			passed++
			t.Logf("Sent OK (%d bytes)", len(sc.content))
			time.Sleep(2 * time.Second)
		})
	}

	t.Logf("\n=== TG E2E 10 Scenarios: %d/%d passed, %d failed ===", passed, len(scenarios), failed)
	if failed > 0 {
		t.Errorf("%d scenarios failed", failed)
	}
}

// TestE2ERealLLMToTGCallRealLLM is a TRUE end-to-end test that mimics the exact
// user workflow: user prompt → agent (LLM + tools) → response → TG send.
//
// Each sub-test creates a fresh project directory and agent with full built-in tools,
// just like the TUI does. The prompts are real project tasks designed to trigger the
// LLM to write files, execute commands, and include image references in its response.
//
// Image pipeline: LLM writes HTML → run_command takes screenshot → LLM references
// the screenshot file path in its text response → ExtractImagesFromText extracts it
// → sendExtractedImage uploads to TG → send remaining text.
//
// Tool call events are formatted through the formal TUI pipeline:
//
//	DescribeTool → FormatIMStatus → LocalizeIMProgress → adapter.Send
//
// This exactly mirrors the TUI's submit.go → im_emit.go processing chain.
//
// Set GGCODE_E2E=1 to run. Requires:
//   - ~/.ggcode/ggcode.yaml with a valid provider and TG adapter config
//   - GGCODE_E2E_TG_CHAT_ID set to a Telegram chat ID
//
// This test sends real messages — check Telegram to verify.
func TestE2ERealLLMToTGCallRealLLM(t *testing.T) {
	if os.Getenv("GGCODE_E2E") == "" {
		t.Skip("GGCODE_E2E not set, skipping")
	}

	cfg := loadE2EConfig(t)
	if cfg == nil {
		t.Skip("config not found")
	}

	prov := resolveE2EProvider(t, cfg)
	if prov == nil {
		t.Skip("no valid provider in config")
	}

	adapter, adapterName, chatID := tgSetupAdapter(t)
	ctx := context.Background()

	resolved, resolveErr := cfg.ResolveActiveEndpoint()
	if resolveErr != nil {
		t.Fatalf("resolve endpoint: %v", resolveErr)
	}

	// Real project-task prompts that trigger the LLM to:
	//  1. write_file → create HTML/JS files
	//  2. run_command → open browser + take screenshot
	//  3. Reference the screenshot file in its text response
	//  4. The file path gets extracted by ExtractImagesFromText → uploaded to TG
	prompts := []struct {
		name    string
		prompt  string
		maxIter int
		timeout time.Duration
	}{
		{
			name:    "01_bar_chart",
			prompt:  "用HTML+JavaScript画一个柱状图展示以下销售数据：Q1=120, Q2=180, Q3=150, Q4=200。保存为 chart.html，完成后用open命令打开并截图保存为 chart_screenshot.png，在回复中附上截图。",
			maxIter: 15,
			timeout: 150 * time.Second,
		},
		{
			name:    "02_pie_chart",
			prompt:  "用HTML+JavaScript+Canvas画一个简单的饼图展示以下数据：技术30%, 产品25%, 设计20%, 运营15%, 其他10%。保存为 pie.html，完成后用open命令打开并截图保存为 pie_screenshot.png，在回复中附上截图。",
			maxIter: 15,
			timeout: 150 * time.Second,
		},
		{
			name:    "03_counter",
			prompt:  "做一个简单的网页计数器（counter.html），有一个数字显示和加减按钮。完成后用open命令打开并截图保存为 counter_screenshot.png，在回复中附上截图。",
			maxIter: 15,
			timeout: 150 * time.Second,
		},
		{
			name:    "04_color_blocks",
			prompt:  "做一个简单的网页，展示6个不同颜色的方块（红橙黄绿蓝紫），排列成两行三列。保存为 colors.html，完成后用open命令打开并截图保存为 colors_screenshot.png，在回复中附上截图。",
			maxIter: 15,
			timeout: 150 * time.Second,
		},
		{
			name:    "05_line_chart",
			prompt:  "用HTML+JavaScript+Canvas画一个折线图展示月度趋势：1月=50, 2月=65, 3月=80, 4月=72, 5月=90。保存为 line.html，完成后用open命令打开并截图保存为 line_screenshot.png，在回复中附上截图。",
			maxIter: 15,
			timeout: 150 * time.Second,
		},
		{
			name:    "06_bar_and_line",
			prompt:  "分别做两个简单的图表页面：\n1. 柱状图展示：A=30, B=50, C=40。保存为 bar.html，用open打开并截图保存为 bar_screenshot.png\n2. 折线图展示：1月=10, 2月=30, 3月=20。保存为 line2.html，用open打开并截图保存为 line2_screenshot.png\n\n在回复中同时附上两张截图。",
			maxIter: 20,
			timeout: 200 * time.Second,
		},
		{
			name:    "07_two_buttons",
			prompt:  "做两个简单的按钮网页：\n1. 红色按钮页面，保存为 red.html，用open打开并截图保存为 red_screenshot.png\n2. 蓝色按钮页面，保存为 blue.html，用open打开并截图保存为 blue_screenshot.png\n\n每个页面只有一个居中的大按钮。在回复中同时附上两张截图。",
			maxIter: 20,
			timeout: 200 * time.Second,
		},
	}

	passed := 0
	failed := 0
	totalImages := 0
	totalToolCalls := 0
	for _, p := range prompts {
		t.Run(p.name, func(t *testing.T) {
			// Each sub-test gets its own project directory (like a real user workspace).
			projectDir := t.TempDir()
			t.Logf("Project dir: %s", projectDir)

			// Create agent with full built-in tools (exactly like TUI does).
			registry := tool.NewRegistry()
			if regErr := tool.RegisterBuiltinTools(registry, nil, projectDir); regErr != nil {
				t.Fatalf("register builtin tools: %v", regErr)
			}

			sysPrompt := "You are a helpful coding assistant. Respond in Chinese (Simplified).\n\n" +
				"## Environment\n" +
				fmt.Sprintf("- Working directory: %s\n", projectDir) +
				fmt.Sprintf("- OS: %s/%s\n", runtime.GOOS, runtime.GOARCH) +
				"\n" +
				"When creating files, use write_file with an **absolute path** under the working directory. " +
				"After creating HTML files, use run_command to open them with the 'open' command on macOS and take a " +
				"screenshot with 'screencapture -x'. Always include the screenshot file path " +
				"in your response using markdown image syntax like " +
				"![screenshot](/path/to/screenshot.png)."
			ag := agent.NewAgent(prov, registry, sysPrompt, p.maxIter)
			ag.SetWorkingDir(projectDir)
			ag.SetSupportsVision(resolved.SupportsVision)

			llmCtx, llmCancel := context.WithTimeout(ctx, p.timeout)
			defer llmCancel()

			// Capture all events using the formal TUI formatting pipeline:
			// DescribeTool → FormatIMStatus → LocalizeIMProgress → adapter.Send
			// This exactly mirrors submit.go → im_emit.go in the TUI.
			var responseText strings.Builder
			toolCallCount := 0
			toolResultCount := 0
			var lastToolPresent ToolPresentation

			streamErr := ag.RunStream(llmCtx, p.prompt, func(event provider.StreamEvent) {
				switch event.Type {
				case provider.StreamEventText:
					responseText.WriteString(event.Text)

				case provider.StreamEventToolCallDone:
					// Tool call completed — format through the formal pipeline.
					// Same as TUI's submit.go: describeTool → statusMsg → emitIMStatusMsg
					toolCallCount++
					lastToolPresent = DescribeTool(ToolLangZhCN, event.Tool.Name, string(event.Tool.Arguments))

					// Format status: FormatIMStatus(activity, displayName, detail) → LocalizeIMProgress
					statusMsg := FormatIMStatus(ToolLangZhCN, lastToolPresent.Activity, lastToolPresent.DisplayName, lastToolPresent.Detail)
					if statusMsg == "" {
						statusMsg = lastToolPresent.Activity
					}
					t.Logf("[tool_call #%d] %s (display=%q detail=%q activity=%q)",
						toolCallCount, statusMsg, lastToolPresent.DisplayName, lastToolPresent.Detail, lastToolPresent.Activity)

					// Send tool call status to TG (non-blocking, ignore errors)
					binding := ChannelBinding{ChannelID: chatID, Adapter: adapterName}
					if sendErr := adapter.Send(ctx, binding, OutboundEvent{
						Kind:   OutboundEventStatus,
						Status: statusMsg,
					}); sendErr != nil {
						t.Logf("[status send error] %v", sendErr)
					}

				case provider.StreamEventToolResult:
					// Tool result — format through the formal pipeline.
					// Same as TUI: describeTool → statusMsg → emitIMStatusMsg
					toolResultCount++
					resultPreview := event.Result
					if len(resultPreview) > 100 {
						resultPreview = resultPreview[:100] + "..."
					}
					t.Logf("[tool_result #%d] tool=%s ok=%v preview=%s",
						toolResultCount, lastToolPresent.DisplayName, !event.IsError, resultPreview)

					// For tool results, TUI sends "thinking" status with tool context
					thinkingStatus := FormatIMStatus(ToolLangZhCN,
						localizedThinking(ToolLangZhCN),
						lastToolPresent.DisplayName,
						lastToolPresent.Detail,
					)
					if thinkingStatus == "" {
						thinkingStatus = localizedThinking(ToolLangZhCN)
					}

					binding := ChannelBinding{ChannelID: chatID, Adapter: adapterName}
					if sendErr := adapter.Send(ctx, binding, OutboundEvent{
						Kind:   OutboundEventStatus,
						Status: thinkingStatus,
					}); sendErr != nil {
						t.Logf("[status send error] %v", sendErr)
					}

				case provider.StreamEventDone:
					if event.Usage != nil {
						t.Logf("[done] tokens: in=%d out=%d", event.Usage.InputTokens, event.Usage.OutputTokens)
					}

				case provider.StreamEventError:
					t.Logf("[error] %v", event.Error)
				}
			})
			if streamErr != nil {
				t.Errorf("LLM RunStream error: %v", streamErr)
				failed++
				return
			}

			output := strings.TrimSpace(responseText.String())
			if output == "" {
				t.Error("LLM returned empty response")
				failed++
				return
			}
			t.Logf("LLM response (%d chars): %s", len(output), truncateStr(output, 500))

			// Extract images from LLM response text.
			images, remainingText := ExtractImagesFromText(output)
			t.Logf("Extracted %d image(s) from response", len(images))
			for i, img := range images {
				t.Logf("  image[%d]: kind=%s data_len=%d preview=%s", i, img.Kind, len(img.Data), truncateStr(img.Data, 100))
			}

			// Send the full LLM response to real TG.
			binding := ChannelBinding{
				ChannelID: chatID,
				Adapter:   adapterName,
			}

			sendErr := adapter.Send(ctx, binding, OutboundEvent{
				Kind: OutboundEventText,
				Text: output,
			})
			if sendErr != nil {
				t.Errorf("TG Send failed: %v", sendErr)
				t.Logf("FULL LLM response that failed: %s", output)
				// Show what happens after extract + escape
				_, rem := ExtractImagesFromText(output)
				t.Logf("After extract images, remaining (%d chars): %s", len(rem), rem)
				escaped := EscapeMarkdownV2(rem)
				t.Logf("After EscapeMarkdownV2 (%d chars): %s", len(escaped), escaped)
				failed++
				return
			}
			passed++
			totalImages += len(images)
			totalToolCalls += toolCallCount

			remaining := strings.TrimSpace(remainingText)
			if len(remaining) > 100 {
				remaining = remaining[:100] + "..."
			}
			t.Logf("Sent to TG OK (%d chars text, %d images, %d tool calls, remaining: %q)",
				len(output), len(images), toolCallCount, remaining)

			// Delay between messages to avoid rate limiting
			time.Sleep(5 * time.Second)
		})
	}

	t.Logf("\n=== Real LLM→TG E2E Results: %d/%d passed, %d failed, %d total images, %d total tool calls ===",
		passed, len(prompts), failed, totalImages, totalToolCalls)
	if failed > 0 {
		t.Errorf("%d scenarios failed", failed)
	}
}
