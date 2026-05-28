package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

const modelDiscoveryCacheTTL = 6 * time.Hour

var (
	modelDiscoveryCacheMu     sync.Mutex
	modelDiscoveryCacheLoaded bool
	modelDiscoveryCache       = map[string]modelDiscoveryCacheEntry{}
)

type modelDiscoveryCacheEntry struct {
	Models    []string  `json:"models"`
	FetchedAt time.Time `json:"fetched_at"`
}

type modelDiscoveryCacheFile struct {
	Entries map[string]modelDiscoveryCacheEntry `json:"entries"`
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
	if resolved.Protocol != "openai" && resolved.Protocol != "anthropic" && resolved.Protocol != "gemini" && resolved.Protocol != "copilot" {
		return nil, fmt.Errorf("protocol %q does not support model discovery", resolved.Protocol)
	}
	cacheKey := modelDiscoveryCacheKey(resolved)
	cachedModels, cacheErr := loadCachedDiscoveredModels(cacheKey)
	if len(cachedModels) > 0 {
		return cachedModels, nil
	}

	client := &http.Client{Timeout: 8 * time.Second}
	var errs []string
	if cacheErr != nil {
		errs = append(errs, fmt.Sprintf("cache %s: %v", cacheKey, cacheErr))
	}
	for _, candidate := range modelDiscoveryCandidates(resolved.BaseURL, resolved.Protocol) {
		models, err := discoverModelsFromURL(ctx, client, candidate, resolved)
		if err == nil && len(models) > 0 {
			if cacheKey != "" {
				if err := saveCachedDiscoveredModels(cacheKey, models); err != nil {
					errs = append(errs, fmt.Sprintf("cache save %s: %v", cacheKey, err))
				}
			}
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
	case "copilot":
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(resolved.APIKey))
		req.Header.Set("User-Agent", "ggcode")
	default:
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(resolved.APIKey))
	}
	for key, values := range vendorSpecificAuthHeaders(resolved.BaseURL, resolved.APIKey) {
		for _, value := range values {
			req.Header.Set(key, value)
		}
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

func modelDiscoveryCacheKey(resolved *config.ResolvedEndpoint) string {
	if resolved == nil {
		return ""
	}
	host := normalizedDiscoveryHost(resolved.BaseURL)
	if host == "" {
		return ""
	}
	vendorID := strings.TrimSpace(strings.ToLower(resolved.VendorID))
	if vendorID == "" {
		vendorID = "unknown"
	}
	if isGatewayModelDiscovery(resolved) {
		return fmt.Sprintf("endpoint:%s:%s:%s", vendorID, strings.TrimSpace(strings.ToLower(resolved.EndpointID)), host)
	}
	return fmt.Sprintf("host:%s:%s", vendorID, host)
}

func isGatewayModelDiscovery(resolved *config.ResolvedEndpoint) bool {
	if resolved == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(resolved.VendorID), "ai-gateway") {
		return true
	}
	for _, tag := range resolved.Tags {
		if strings.EqualFold(strings.TrimSpace(tag), "gateway") {
			return true
		}
	}
	return false
}

func normalizedDiscoveryHost(baseURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return strings.ToLower(strings.TrimSpace(baseURL))
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		host = strings.TrimSpace(parsed.Host)
	}
	return strings.ToLower(host)
}

func loadCachedDiscoveredModels(cacheKey string) ([]string, error) {
	if cacheKey == "" {
		return nil, nil
	}
	modelDiscoveryCacheMu.Lock()
	defer modelDiscoveryCacheMu.Unlock()
	if err := ensureModelDiscoveryCacheLoadedLocked(); err != nil {
		return nil, err
	}
	entry, ok := modelDiscoveryCache[cacheKey]
	if !ok {
		return nil, nil
	}
	if time.Since(entry.FetchedAt) > modelDiscoveryCacheTTL {
		delete(modelDiscoveryCache, cacheKey)
		if err := persistModelDiscoveryCacheLocked(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	return append([]string(nil), entry.Models...), nil
}

func saveCachedDiscoveredModels(cacheKey string, models []string) error {
	if cacheKey == "" || len(models) == 0 {
		return nil
	}
	modelDiscoveryCacheMu.Lock()
	defer modelDiscoveryCacheMu.Unlock()
	if err := ensureModelDiscoveryCacheLoadedLocked(); err != nil {
		return err
	}
	modelDiscoveryCache[cacheKey] = modelDiscoveryCacheEntry{
		Models:    append([]string(nil), models...),
		FetchedAt: time.Now().UTC(),
	}
	return persistModelDiscoveryCacheLocked()
}

func ensureModelDiscoveryCacheLoadedLocked() error {
	if modelDiscoveryCacheLoaded {
		return nil
	}
	modelDiscoveryCacheLoaded = true
	modelDiscoveryCache = map[string]modelDiscoveryCacheEntry{}
	path := modelDiscoveryCachePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var file modelDiscoveryCacheFile
	if err := json.Unmarshal(data, &file); err != nil {
		return err
	}
	if file.Entries != nil {
		modelDiscoveryCache = file.Entries
	}
	return nil
}

func persistModelDiscoveryCacheLocked() error {
	path := modelDiscoveryCachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(modelDiscoveryCacheFile{Entries: modelDiscoveryCache}, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func modelDiscoveryCachePath() string {
	return filepath.Join(config.ConfigDir(), "model_discovery_cache.json")
}
