package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/sashabaranov/go-openai"
	"github.com/spf13/cobra"
	"google.golang.org/genai"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

type probeResult struct {
	Vendor   string
	Endpoint string
	Protocol string
	BaseURL  string
	Model    string

	AuthStatus string // OK, FAIL, TIMEOUT
	AuthErr    string

	ChatStatus   string // OK, FAIL, TIMEOUT, SKIP
	ChatInput    int
	ChatOutput   int
	ChatText     string
	ChatLatency  time.Duration
	ChatErr      string
	ChatReqBody  string
	ChatRespBody string

	StreamStatus  string // OK, FAIL, TIMEOUT, SKIP
	StreamInput   int
	StreamOutput  int
	StreamLatency time.Duration
	StreamErr     string

	Estimate int
	Ratio    float64
}

func newLLMProbeCmd(cfgFile *string) *cobra.Command {
	var vendorFilter, endpointFilter string
	var verbose bool
	var timeoutSec int

	cmd := &cobra.Command{
		Use:   "llm-probe",
		Short: "Test all configured LLM endpoints for connectivity, auth, and usage",
		Long: `Test all configured LLM endpoints with real API keys.

For each endpoint, tests both streaming and non-streaming API calls,
reporting authentication status, response times, and token usage accuracy.

Examples:
  ggcode llm-probe                   # Test all endpoints
  ggcode llm-probe --vendor zai      # Test only zai vendor
  ggcode llm-probe -v                # Verbose: show request/response bodies
  ggcode llm-probe --timeout 30      # Use 30s timeout per endpoint`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLLMProbe(*cfgFile, vendorFilter, endpointFilter, verbose, timeoutSec)
		},
	}

	cmd.Flags().StringVar(&vendorFilter, "vendor", "", "Test only this vendor")
	cmd.Flags().StringVar(&endpointFilter, "endpoint", "", "Test only this endpoint")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show full request/response headers and bodies")
	cmd.Flags().IntVar(&timeoutSec, "timeout", 20, "Timeout per API call in seconds")

	return cmd
}

func runLLMProbe(cfgFile, vendorFilter, endpointFilter string, verbose bool, timeoutSec int) error {
	// Load keys into environment
	if err := config.LoadKeysEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: LoadKeysEnv: %v\n", err)
	}

	cfg, err := config.LoadWithInstance(cfgFile, "")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Collect all endpoints to test
	type endpointRef struct {
		vendor   string
		endpoint string
	}
	var refs []endpointRef

	for vname, vendor := range cfg.Vendors {
		if vendorFilter != "" && vname != vendorFilter {
			continue
		}
		for epname := range vendor.Endpoints {
			if endpointFilter != "" && epname != endpointFilter {
				continue
			}
			refs = append(refs, endpointRef{vendor: vname, endpoint: epname})
		}
	}

	if len(refs) == 0 {
		if vendorFilter != "" || endpointFilter != "" {
			return fmt.Errorf("no matching endpoints found for vendor=%q endpoint=%q", vendorFilter, endpointFilter)
		}
		return fmt.Errorf("no endpoints configured")
	}

	// Sort by vendor/endpoint name
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].vendor != refs[j].vendor {
			return refs[i].vendor < refs[j].vendor
		}
		return refs[i].endpoint < refs[j].endpoint
	})

	results := make([]*probeResult, 0, len(refs))

	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "Say exactly: Hello world"}}},
	}

	for i, ref := range refs {
		label := fmt.Sprintf("%s/%s", ref.vendor, ref.endpoint)
		fmt.Fprintf(os.Stderr, "\r[%d/%d] Testing %s ...", i+1, len(refs), label)

		resolved, err := cfg.ResolveEndpoint(ref.vendor, ref.endpoint)
		if err != nil || resolved.APIKey == "" {
			results = append(results, &probeResult{
				Vendor:     ref.vendor,
				Endpoint:   ref.endpoint,
				Protocol:   "?",
				AuthStatus: "NO_KEY",
				AuthErr:    "no API key resolved",
			})
			continue
		}

		if resolved.Model == "" {
		}
		if resolved.Model == "" {
			results = append(results, &probeResult{
				Vendor:     ref.vendor,
				Endpoint:   ref.endpoint,
				Protocol:   resolved.Protocol,
				BaseURL:    resolved.BaseURL,
				AuthStatus: "NO_MODEL",
				AuthErr:    "no model configured and no default for protocol",
			})
			continue
		}

		resolved.MaxTokens = 50

		r := &probeResult{
			Vendor:   ref.vendor,
			Endpoint: ref.endpoint,
			Protocol: resolved.Protocol,
			BaseURL:  resolved.BaseURL,
			Model:    resolved.Model,
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)

		prov, perr := provider.NewProvider(resolved)
		if perr != nil {
			r.AuthStatus = "FAIL"
			r.AuthErr = perr.Error()
			results = append(results, r)
			cancel()
			continue
		}

		// Estimate tokens
		r.Estimate, _ = prov.CountTokens(ctx, msgs)

		// Test Chat (non-streaming)
		start := time.Now()
		resp, err := prov.Chat(ctx, msgs, nil)
		r.ChatLatency = time.Since(start)
		if err != nil {
			r.ChatStatus = classifyError(err)
			r.ChatErr = truncateErr(err.Error(), 120)
			// If auth error, mark auth status too
			if isAuthError(err) {
				r.AuthStatus = "FAIL"
				r.AuthErr = truncateErr(err.Error(), 80)
			}
		} else {
			r.AuthStatus = "OK"
			r.ChatStatus = "OK"
			r.ChatInput = resp.Usage.InputTokens
			r.ChatOutput = resp.Usage.OutputTokens
			for _, b := range resp.Message.Content {
				if b.Type == "text" {
					r.ChatText = b.Text
					break
				}
			}
			if r.ChatInput > 0 && r.Estimate > 0 {
				r.Ratio = float64(r.Estimate) / float64(r.ChatInput)
			}
		}

		// Test ChatStream
		start = time.Now()
		ch, err := prov.ChatStream(ctx, msgs, nil)
		if err != nil {
			r.StreamStatus = classifyError(err)
			r.StreamErr = truncateErr(err.Error(), 120)
		} else {
			var gotUsage bool
			for ev := range ch {
				if ev.Type == provider.StreamEventDone && ev.Usage != nil {
					r.StreamInput = ev.Usage.InputTokens
					r.StreamOutput = ev.Usage.OutputTokens
					gotUsage = true
				}
				if ev.Type == provider.StreamEventError {
					r.StreamErr = truncateErr(ev.Error.Error(), 120)
					if r.StreamStatus == "" {
						r.StreamStatus = "FAIL"
					}
				}
			}
			r.StreamLatency = time.Since(start)
			if r.StreamStatus == "" {
				if gotUsage || r.StreamOutput > 0 {
					r.StreamStatus = "OK"
				} else {
					r.StreamStatus = "NO_USAGE"
				}
			}
		}

		// If chat failed but stream might have worked, infer auth
		if r.AuthStatus == "" && r.StreamStatus == "OK" {
			r.AuthStatus = "OK"
		} else if r.AuthStatus == "" {
			r.AuthStatus = "FAIL"
		}

		results = append(results, r)
		cancel()
	}

	fmt.Fprintln(os.Stderr, "\r"+strings.Repeat(" ", 80)+"\r") // clear progress line

	// Print results
	printProbeResults(results, verbose)
	return nil
}

// fetchFirstModel calls the provider's ListModels API and returns the first available model.
// Falls back to a protocol-specific default if the API call fails.
func fetchFirstModel(ctx context.Context, resolved *config.ResolvedEndpoint) (string, error) {
	switch resolved.Protocol {
	case "openai", "copilot":
		return fetchOpenAIModel(ctx, resolved)
	case "anthropic":
		return fetchAnthropicModel(ctx, resolved)
	case "gemini":
		return fetchGeminiModel(ctx, resolved)
	default:
		return "", fmt.Errorf("unsupported protocol: %s", resolved.Protocol)
	}
}

func fetchOpenAIModel(ctx context.Context, resolved *config.ResolvedEndpoint) (string, error) {
	cfg := openai.DefaultConfig(resolved.APIKey)
	if resolved.BaseURL != "" {
		cfg.BaseURL = resolved.BaseURL
	}
	client := openai.NewClientWithConfig(cfg)
	models, err := client.ListModels(ctx)
	if err != nil {
		return "", fmt.Errorf("openai ListModels: %w", err)
	}
	for _, m := range models.Models {
		// Prefer chat-capable models, skip embedding/tts/whisper etc.
		id := m.ID
		if strings.Contains(id, "gpt") || strings.Contains(id, "chat") || strings.Contains(id, "claude") || strings.Contains(id, "deepseek") || strings.Contains(id, "qwen") || strings.Contains(id, "glm") || strings.Contains(id, "doubao") {
			return id, nil
		}
	}
	// Fallback to first non-embedding model
	for _, m := range models.Models {
		if !strings.Contains(m.ID, "embed") && !strings.Contains(m.ID, "tts") && !strings.Contains(m.ID, "whisper") && !strings.Contains(m.ID, "dall-e") {
			return m.ID, nil
		}
	}
	if len(models.Models) > 0 {
		return models.Models[0].ID, nil
	}
	return "", fmt.Errorf("no models returned by API")
}

func fetchAnthropicModel(ctx context.Context, resolved *config.ResolvedEndpoint) (string, error) {
	opts := []option.RequestOption{option.WithAPIKey(resolved.APIKey)}
	if resolved.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(resolved.BaseURL))
	}
	client := anthropic.NewClient(opts...)
	page, err := client.Models.List(ctx, anthropic.ModelListParams{}, opts...)
	if err != nil {
		return "", fmt.Errorf("anthropic ListModels: %w", err)
	}
	for _, m := range page.Data {
		// Prefer claude models
		if strings.Contains(m.ID, "claude") {
			return m.ID, nil
		}
	}
	if len(page.Data) > 0 {
		return page.Data[0].ID, nil
	}
	return "", fmt.Errorf("no models returned by API")
}

func fetchGeminiModel(ctx context.Context, resolved *config.ResolvedEndpoint) (string, error) {
	clientCfg := &genai.ClientConfig{
		APIKey:  resolved.APIKey,
		Backend: genai.BackendGeminiAPI,
	}
	if resolved.BaseURL != "" {
		clientCfg.HTTPOptions.BaseURL = resolved.BaseURL
	}
	client, err := genai.NewClient(ctx, clientCfg)
	if err != nil {
		return "", fmt.Errorf("gemini client: %w", err)
	}
	page, err := client.Models.List(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("gemini ListModels: %w", err)
	}
	// Prefer gemini flash models (fast, cheap for probing)
	for _, m := range page.Items {
		if strings.Contains(m.Name, "gemini") && strings.Contains(m.Name, "flash") {
			return extractModelID(m.Name), nil
		}
	}
	// Fallback to any gemini model
	for _, m := range page.Items {
		if strings.Contains(m.Name, "gemini") {
			return extractModelID(m.Name), nil
		}
	}
	if len(page.Items) > 0 {
		return extractModelID(page.Items[0].Name), nil
	}
	return "", fmt.Errorf("no models returned by API")
}

func extractModelID(name string) string {
	if strings.HasPrefix(name, "models/") {
		return name[len("models/"):]
	}
	return name
}

func printProbeResults(results []*probeResult, verbose bool) {
	if verbose {
		// Verbose: detailed per-endpoint output
		for _, r := range results {
			label := fmt.Sprintf("%s/%s", r.Vendor, r.Endpoint)
			fmt.Printf("\n═══════════════════════════════════════════════════════════════\n")
			fmt.Printf("  %s  (%s)  base=%s  model=%s\n", label, r.Protocol, r.BaseURL, r.Model)
			fmt.Printf("═══════════════════════════════════════════════════════════════\n")

			fmt.Printf("  Auth:     %s", r.AuthStatus)
			if r.AuthErr != "" {
				fmt.Printf("  (%s)", r.AuthErr)
			}
			fmt.Println()

			fmt.Printf("  Chat:     %s  latency=%v  input=%d  output=%d",
				r.ChatStatus, r.ChatLatency.Round(time.Millisecond), r.ChatInput, r.ChatOutput)
			if r.ChatText != "" {
				fmt.Printf("  text=%q", r.ChatText)
			}
			if r.ChatErr != "" {
				fmt.Printf("  err=%s", r.ChatErr)
			}
			fmt.Println()

			fmt.Printf("  Stream:   %s  latency=%v  input=%d  output=%d",
				r.StreamStatus, r.StreamLatency.Round(time.Millisecond), r.StreamInput, r.StreamOutput)
			if r.StreamErr != "" {
				fmt.Printf("  err=%s", r.StreamErr)
			}
			fmt.Println()

			fmt.Printf("  Estimate: %d tokens", r.Estimate)
			if r.Ratio > 0 {
				fmt.Printf("  ratio=%.2f", r.Ratio)
			}
			fmt.Println()
		}
		return
	}

	// Table mode
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "VENDOR/ENDPOINT\tPROTOCOL\tAUTH\tCHAT\tSTREAM\tCHAT_IN\tCHAT_OUT\tSTREAM_IN\tSTREAM_OUT\tESTIMATE\tRATIO\tLATENCY")

	for _, r := range results {
		label := fmt.Sprintf("%s/%s", r.Vendor, r.Endpoint)
		chatIn := dash(r.ChatInput)
		chatOut := dash(r.ChatOutput)
		streamIn := dash(r.StreamInput)
		streamOut := dash(r.StreamOutput)
		estimate := dash(r.Estimate)
		ratio := "-"
		if r.Ratio > 0 {
			ratio = fmt.Sprintf("%.2f", r.Ratio)
		}
		latency := "-"
		if r.ChatLatency > 0 {
			latency = fmt.Sprintf("%.1fs", r.ChatLatency.Seconds())
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			label, r.Protocol, r.AuthStatus, r.ChatStatus, r.StreamStatus,
			chatIn, chatOut, streamIn, streamOut, estimate, ratio, latency)
	}
	w.Flush()

	// Summary
	ok, fail, timeout, other := 0, 0, 0, 0
	for _, r := range results {
		switch r.AuthStatus {
		case "OK":
			ok++
		case "FAIL", "NO_KEY":
			fail++
		case "TIMEOUT":
			timeout++
		default:
			other++
		}
	}
	fmt.Printf("\nSummary: %d endpoints tested, %d OK, %d failed, %d timeout, %d other\n",
		len(results), ok, fail, timeout, other)

	// Show failures detail
	hasFails := false
	for _, r := range results {
		if r.AuthStatus != "OK" {
			if !hasFails {
				fmt.Println("\nFailed endpoints:")
				hasFails = true
			}
			fmt.Printf("  %-40s %s: %s\n", fmt.Sprintf("%s/%s", r.Vendor, r.Endpoint), r.AuthStatus, r.AuthErr)
		}
	}

	// Show usage gaps
	fmt.Println("\nUsage accuracy (estimate vs real input tokens):")
	for _, r := range results {
		if r.ChatInput > 0 {
			status := "OK"
			if r.Ratio < 0.5 {
				status = "LOW"
			} else if r.Ratio > 1.5 {
				status = "HIGH"
			}
			streamNote := ""
			if r.StreamInput == 0 {
				streamNote = " [stream input=0!]"
			}
			fmt.Printf("  %-40s estimate=%d real=%d ratio=%.2f %s%s\n",
				fmt.Sprintf("%s/%s", r.Vendor, r.Endpoint), r.Estimate, r.ChatInput, r.Ratio, status, streamNote)
		}
	}
}

func dash(v int) string {
	if v == 0 {
		return "-"
	}
	return fmt.Sprintf("%d", v)
}

func classifyError(err error) string {
	if err == nil {
		return "OK"
	}
	msg := err.Error()
	if isAuthError(err) {
		return "AUTH"
	}
	if strings.Contains(msg, "context deadline exceeded") || strings.Contains(msg, "timeout") {
		return "TIMEOUT"
	}
	return "FAIL"
}

func isAuthError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "401") ||
		strings.Contains(msg, "403") ||
		strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "forbidden") ||
		strings.Contains(msg, "invalid api key") ||
		strings.Contains(msg, "invalid x-api-key") ||
		strings.Contains(msg, "authentication_error") ||
		strings.Contains(msg, "invalid access token")
}

func truncateErr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// probeRoundTripper captures HTTP request/response for verbose mode.
// Not used in current implementation but available for future enhancement.
type probeRoundTripper struct {
	base    http.RoundTripper
	reqBody *bytes.Buffer
}

func (t *probeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Capture request body
	var reqBody string
	if req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		reqBody = string(body)
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	t.reqBody.WriteString(reqBody)

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	// Capture response body
	if resp.Body != nil {
		body, _ := io.ReadAll(resp.Body)
		_ = body // store for verbose output
		resp.Body = io.NopCloser(bytes.NewReader(body))
	}

	return resp, nil
}

// maskHeaders masks sensitive values in HTTP headers for display.
func maskHeaders(h http.Header) map[string][]string {
	masked := make(map[string][]string)
	for k, vs := range h {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "auth") || strings.Contains(lk, "key") || strings.Contains(lk, "token") || strings.Contains(lk, "secret") {
			masked[k] = []string{"***"}
		} else {
			masked[k] = vs
		}
	}
	return masked
}

// formatJSON pretty-prints JSON bytes, truncating to maxLen.
func formatJSON(data []byte, maxLen int) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, data, "", "  "); err != nil {
		s := string(data)
		if len(s) > maxLen {
			return s[:maxLen] + "..."
		}
		return s
	}
	s := buf.String()
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
