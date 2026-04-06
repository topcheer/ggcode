package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

type modelDiscoveryResponse struct {
	Data   []modelDiscoveryEntry `json:"data"`
	Models []modelDiscoveryEntry `json:"models"`
}

type modelDiscoveryEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// DiscoverModels fetches the latest model list for a resolved endpoint when the remote API exposes it.
func DiscoverModels(ctx context.Context, resolved *config.ResolvedEndpoint) ([]string, error) {
	if resolved == nil {
		return nil, fmt.Errorf("resolved endpoint is nil")
	}
	if !hasUsableAPIKey(resolved.APIKey) {
		return nil, fmt.Errorf("endpoint %q has no API key configured", resolved.EndpointID)
	}
	if strings.TrimSpace(resolved.BaseURL) == "" {
		return nil, fmt.Errorf("endpoint %q has no base URL configured", resolved.EndpointID)
	}
	if resolved.Protocol != "openai" && resolved.Protocol != "anthropic" && resolved.Protocol != "gemini" {
		return nil, fmt.Errorf("protocol %q does not support model discovery", resolved.Protocol)
	}

	client := &http.Client{Timeout: 8 * time.Second}
	var errs []string
	for _, candidate := range modelDiscoveryCandidates(resolved.BaseURL, resolved.Protocol) {
		models, err := discoverModelsFromURL(ctx, client, candidate, resolved)
		if err == nil && len(models) > 0 {
			return models, nil
		}
		if err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("no models returned by %q", resolved.EndpointName)
	}
	return nil, errors.New(strings.Join(errs, "; "))
}

func discoverModelsFromURL(ctx context.Context, client *http.Client, endpointURL string, resolved *config.ResolvedEndpoint) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building request %s: %w", endpointURL, err)
	}
	req.Header.Set("Accept", "application/json")
	switch resolved.Protocol {
	case "anthropic":
		req.Header.Set("x-api-key", strings.TrimSpace(resolved.APIKey))
		req.Header.Set("anthropic-version", "2023-06-01")
	case "gemini":
		query := req.URL.Query()
		query.Set("key", strings.TrimSpace(resolved.APIKey))
		req.URL.RawQuery = query.Encode()
		req.Header.Set("x-goog-api-key", strings.TrimSpace(resolved.APIKey))
	default:
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(resolved.APIKey))
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", endpointURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		detail := strings.TrimSpace(string(body))
		if detail == "" {
			return nil, fmt.Errorf("GET %s: status %d", endpointURL, resp.StatusCode)
		}
		return nil, fmt.Errorf("GET %s: status %d: %s", endpointURL, resp.StatusCode, detail)
	}

	var payload modelDiscoveryResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode %s: %w", endpointURL, err)
	}

	entries := payload.Data
	if len(entries) == 0 {
		entries = payload.Models
	}
	models := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			id = strings.TrimSpace(entry.Name)
		}
		if resolved.Protocol == "gemini" {
			id = strings.TrimPrefix(id, "models/")
		}
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, id)
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("GET %s: no model IDs returned", endpointURL)
	}
	return models, nil
}

func modelDiscoveryCandidates(baseURL, protocol string) []string {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return []string{trimmed}
	}
	paths := []string{"models"}
	switch protocol {
	case "gemini":
		paths = []string{"v1beta/models", "v1/models", "models"}
	default:
		if !strings.HasSuffix(strings.Trim(parsed.Path, "/"), "/v1") && strings.Trim(parsed.Path, "/") != "v1" {
			paths = append(paths, "v1/models")
		}
	}
	out := make([]string, 0, len(paths))
	seen := map[string]struct{}{}
	for _, path := range paths {
		next := *parsed
		next.Path = joinURLPath(parsed.Path, path)
		next.RawPath = ""
		candidate := next.String()
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func joinURLPath(basePath, suffix string) string {
	basePath = strings.TrimSuffix(basePath, "/")
	suffix = strings.TrimPrefix(suffix, "/")
	if basePath == "" {
		return "/" + suffix
	}
	return basePath + "/" + suffix
}

func hasUsableAPIKey(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	return !(strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}"))
}
