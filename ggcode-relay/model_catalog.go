package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/topcheer/ggcode-relay/safego"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

const (
	catwalkOwner                 = "charmbracelet"
	catwalkRepo                  = "catwalk"
	catwalkBranch                = "update-providers"
	catwalkConfigsPath           = "internal/providers/configs"
	catwalkConfigsAPI            = "https://api.github.com/repos/charmbracelet/catwalk/contents/internal/providers/configs"
	catwalkBranchAPI             = "https://api.github.com/repos/charmbracelet/catwalk/branches/update-providers"
	catwalkRawBase               = "https://raw.githubusercontent.com/charmbracelet/catwalk"
	defaultCatalogSyncInterval   = 24 * time.Hour
	defaultCatalogRequestTimeout = 30 * time.Second
	catalogUserAgent             = "ggcode-relay-model-catalog/1.0"
	modelCatalogSourceRef        = catwalkOwner + "/" + catwalkRepo + ":" + catwalkBranch
)

type catwalkProviderConfig struct {
	ID     string               `json:"id"`
	Name   string               `json:"name"`
	Type   string               `json:"type"`
	Models []catwalkModelConfig `json:"models"`
}

type catwalkModelConfig struct {
	ID               string `json:"id"`
	ContextWindow    int    `json:"context_window"`
	DefaultMaxTokens int    `json:"default_max_tokens"`
}

type modelCatalogEntry struct {
	ProviderID      string
	ProviderName    string
	ProviderType    string
	ModelID         string
	ContextWindow   int
	MaxOutputTokens int
	SourceFile      string
	SourceSHA       string
	UpdatedAt       time.Time
}

type modelCatalogSyncState struct {
	SourceRef     string
	SourceSHA     string
	LastAttemptAt time.Time
	LastSuccessAt time.Time
	LastError     string
	RowCount      int
}

type modelCatalogResolveResponse struct {
	Found             bool      `json:"found"`
	MatchKind         string    `json:"match_kind,omitempty"`
	MatchedProviderID string    `json:"matched_provider_id,omitempty"`
	MatchedModelID    string    `json:"matched_model_id,omitempty"`
	ContextWindow     int       `json:"context_window,omitempty"`
	MaxOutputTokens   int       `json:"max_output_tokens,omitempty"`
	CatalogVersion    string    `json:"catalog_version,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
}

type modelCatalogStatusResponse struct {
	SourceRef     string    `json:"source_ref"`
	SourceSHA     string    `json:"source_sha,omitempty"`
	LastAttemptAt time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt time.Time `json:"last_success_at,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	RowCount      int       `json:"row_count"`
	Ready         bool      `json:"ready"`
}

type modelCatalogSnapshot struct {
	Version       string
	UpdatedAt     time.Time
	Entries       []modelCatalogEntry
	ProviderModel map[string]modelCatalogEntry
	ModelExact    map[string][]modelCatalogEntry
}

type modelCatalogManager struct {
	client   *http.Client
	store    *relayStore
	snapshot atomic.Pointer[modelCatalogSnapshot]
	status   atomic.Pointer[modelCatalogSyncState]
}

func newModelCatalogManager(store *relayStore) (*modelCatalogManager, error) {
	manager := &modelCatalogManager{
		client: &http.Client{Timeout: defaultCatalogRequestTimeout},
		store:  store,
	}
	state, entries, err := store.loadModelCatalog()
	if err != nil {
		return nil, err
	}
	state.RowCount = len(entries)
	manager.storeStatus(state)
	if len(entries) > 0 {
		manager.snapshot.Store(buildModelCatalogSnapshot(state, entries))
	}
	return manager, nil
}

func (m *modelCatalogManager) start(ctx context.Context) {
	safego.Go("relay.model-catalog-refresh", func() {
		m.refresh(ctx)
		ticker := time.NewTicker(defaultCatalogSyncInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.refresh(ctx)
			}
		}
	})
}

func (m *modelCatalogManager) refresh(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, defaultCatalogRequestTimeout)
	defer cancel()
	attemptedAt := time.Now().UTC()
	sourceSHA, err := m.fetchBranchSHA(ctx)
	if err != nil {
		m.recordFailure(attemptedAt, "", err)
		return
	}
	current := m.loadStatus()
	if current.SourceSHA == sourceSHA && m.snapshot.Load() != nil {
		current.SourceRef = modelCatalogSourceRef
		current.SourceSHA = sourceSHA
		current.LastAttemptAt = attemptedAt
		current.LastSuccessAt = attemptedAt
		current.LastError = ""
		if snap := m.snapshot.Load(); snap != nil {
			current.RowCount = len(snap.Entries)
		}
		if err := m.store.upsertModelCatalogState(current); err != nil {
			log.Printf("[relay] model catalog state update failed: %v", err)
		}
		m.storeStatus(current)
		return
	}

	filenames, err := m.fetchConfigFilenames(ctx, sourceSHA)
	if err != nil {
		m.recordFailure(attemptedAt, sourceSHA, err)
		return
	}
	entries, err := m.fetchEntries(ctx, sourceSHA, filenames, attemptedAt)
	if err != nil {
		m.recordFailure(attemptedAt, sourceSHA, err)
		return
	}
	state := modelCatalogSyncState{
		SourceRef:     modelCatalogSourceRef,
		SourceSHA:     sourceSHA,
		LastAttemptAt: attemptedAt,
		LastSuccessAt: attemptedAt,
		LastError:     "",
		RowCount:      len(entries),
	}
	if err := m.store.replaceModelCatalog(state, entries); err != nil {
		m.recordFailure(attemptedAt, sourceSHA, err)
		return
	}
	m.snapshot.Store(buildModelCatalogSnapshot(state, entries))
	m.storeStatus(state)
	log.Printf("[relay] model catalog synced sha=%s rows=%d", shortSHA(sourceSHA), len(entries))
}

func (m *modelCatalogManager) recordFailure(attemptedAt time.Time, sourceSHA string, err error) {
	if err == nil {
		return
	}
	state := m.loadStatus()
	state.SourceRef = modelCatalogSourceRef
	if strings.TrimSpace(sourceSHA) != "" {
		state.SourceSHA = strings.TrimSpace(sourceSHA)
	}
	state.LastAttemptAt = attemptedAt
	state.LastError = err.Error()
	if persistErr := m.store.upsertModelCatalogState(state); persistErr != nil {
		log.Printf("[relay] model catalog state persist failed after error: %v", persistErr)
	}
	m.storeStatus(state)
	log.Printf("[relay] model catalog sync failed: %v", err)
}

func (m *modelCatalogManager) fetchBranchSHA(ctx context.Context) (string, error) {
	var payload struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := m.doJSON(ctx, catwalkBranchAPI, &payload); err != nil {
		return "", err
	}
	sha := strings.TrimSpace(payload.Commit.SHA)
	if sha == "" {
		return "", fmt.Errorf("catwalk branch response missing commit sha")
	}
	return sha, nil
}

func (m *modelCatalogManager) fetchConfigFilenames(ctx context.Context, sourceSHA string) ([]string, error) {
	url := fmt.Sprintf("%s?ref=%s", catwalkConfigsAPI, sourceSHA)
	var entries []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := m.doJSON(ctx, url, &entries); err != nil {
		return nil, err
	}
	filenames := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Type != "file" || !strings.HasSuffix(entry.Name, ".json") {
			continue
		}
		filenames = append(filenames, entry.Name)
	}
	sort.Strings(filenames)
	if len(filenames) == 0 {
		return nil, fmt.Errorf("catwalk config listing at %s returned no json files", catwalkConfigsPath)
	}
	return filenames, nil
}

func (m *modelCatalogManager) fetchEntries(ctx context.Context, sourceSHA string, filenames []string, updatedAt time.Time) ([]modelCatalogEntry, error) {
	entries := make([]modelCatalogEntry, 0, len(filenames)*16)
	for _, filename := range filenames {
		cfg, err := m.fetchProviderConfig(ctx, sourceSHA, filename)
		if err != nil {
			return nil, fmt.Errorf("fetch %s: %w", filename, err)
		}
		providerID := strings.TrimSpace(cfg.ID)
		if providerID == "" {
			return nil, fmt.Errorf("fetch %s: missing provider id", filename)
		}
		for _, model := range cfg.Models {
			modelID := strings.TrimSpace(model.ID)
			if modelID == "" {
				continue
			}
			entries = append(entries, modelCatalogEntry{
				ProviderID:      providerID,
				ProviderName:    strings.TrimSpace(cfg.Name),
				ProviderType:    strings.TrimSpace(cfg.Type),
				ModelID:         modelID,
				ContextWindow:   model.ContextWindow,
				MaxOutputTokens: model.DefaultMaxTokens,
				SourceFile:      filename,
				SourceSHA:       sourceSHA,
				UpdatedAt:       updatedAt,
			})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ProviderID != entries[j].ProviderID {
			return entries[i].ProviderID < entries[j].ProviderID
		}
		return entries[i].ModelID < entries[j].ModelID
	})
	return entries, nil
}

func (m *modelCatalogManager) fetchProviderConfig(ctx context.Context, sourceSHA, filename string) (*catwalkProviderConfig, error) {
	rawURL := fmt.Sprintf("%s/%s/%s/%s", catwalkRawBase, sourceSHA, catwalkConfigsPath, filename)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", catalogUserAgent)
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return nil, fmt.Errorf("status %s: %s", resp.Status, msg)
	}
	var cfg catwalkProviderConfig
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (m *modelCatalogManager) doJSON(ctx context.Context, requestURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", catalogUserAgent)
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("github api %s: %s", requestURL, msg)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode github api %s: %w", requestURL, err)
	}
	return nil
}

func (m *modelCatalogManager) loadStatus() modelCatalogSyncState {
	if state := m.status.Load(); state != nil {
		return *state
	}
	return modelCatalogSyncState{SourceRef: modelCatalogSourceRef}
}

func (m *modelCatalogManager) storeStatus(state modelCatalogSyncState) {
	copy := state
	m.status.Store(&copy)
}

func buildModelCatalogSnapshot(state modelCatalogSyncState, entries []modelCatalogEntry) *modelCatalogSnapshot {
	snapshot := &modelCatalogSnapshot{
		Version:       state.SourceSHA,
		UpdatedAt:     state.LastSuccessAt,
		Entries:       append([]modelCatalogEntry(nil), entries...),
		ProviderModel: make(map[string]modelCatalogEntry, len(entries)),
		ModelExact:    make(map[string][]modelCatalogEntry),
	}
	for _, entry := range entries {
		providerKey := normalizeModelCatalogID(entry.ProviderID)
		modelKey := normalizeModelCatalogID(entry.ModelID)
		if providerKey == "" || modelKey == "" {
			continue
		}
		snapshot.ProviderModel[providerModelKey(providerKey, modelKey)] = entry
		snapshot.ModelExact[modelKey] = append(snapshot.ModelExact[modelKey], entry)
	}
	for key, values := range snapshot.ModelExact {
		sort.Slice(values, func(i, j int) bool {
			if values[i].ProviderID != values[j].ProviderID {
				return values[i].ProviderID < values[j].ProviderID
			}
			return values[i].ModelID < values[j].ModelID
		})
		snapshot.ModelExact[key] = values
	}
	return snapshot
}

func (s *modelCatalogSnapshot) resolve(providerID, modelID string) modelCatalogResolveResponse {
	modelKey := normalizeModelCatalogID(modelID)
	if s == nil || modelKey == "" {
		return modelCatalogResolveResponse{}
	}
	providerCandidates := providerMatchCandidates(providerID)
	for _, provider := range providerCandidates {
		if entry, ok := s.ProviderModel[providerModelKey(provider, modelKey)]; ok {
			return makeResolveResponse("provider_model_exact", s, entry)
		}
	}
	if exact := s.ModelExact[modelKey]; len(exact) > 0 {
		return makeResolveResponse("model_exact", s, pickPreferredCatalogEntry(exact, providerCandidates))
	}
	var prefixMatches []modelCatalogEntry
	for _, entry := range s.Entries {
		if strings.HasPrefix(normalizeModelCatalogID(entry.ModelID), modelKey) {
			prefixMatches = append(prefixMatches, entry)
		}
	}
	if len(prefixMatches) > 0 {
		return makeResolveResponse("model_prefix", s, pickPreferredCatalogEntry(prefixMatches, providerCandidates))
	}
	return modelCatalogResolveResponse{}
}

func makeResolveResponse(matchKind string, snapshot *modelCatalogSnapshot, entry modelCatalogEntry) modelCatalogResolveResponse {
	return modelCatalogResolveResponse{
		Found:             true,
		MatchKind:         matchKind,
		MatchedProviderID: entry.ProviderID,
		MatchedModelID:    entry.ModelID,
		ContextWindow:     entry.ContextWindow,
		MaxOutputTokens:   entry.MaxOutputTokens,
		CatalogVersion:    snapshot.Version,
		UpdatedAt:         snapshot.UpdatedAt,
	}
}

func pickPreferredCatalogEntry(entries []modelCatalogEntry, providers []string) modelCatalogEntry {
	providerRank := make(map[string]int, len(providers))
	for i, provider := range providers {
		providerRank[provider] = i
	}
	sort.Slice(entries, func(i, j int) bool {
		pi, okI := providerRank[normalizeModelCatalogID(entries[i].ProviderID)]
		pj, okJ := providerRank[normalizeModelCatalogID(entries[j].ProviderID)]
		switch {
		case okI && !okJ:
			return true
		case !okI && okJ:
			return false
		case okI && okJ && pi != pj:
			return pi < pj
		}
		if len(entries[i].ModelID) != len(entries[j].ModelID) {
			return len(entries[i].ModelID) > len(entries[j].ModelID)
		}
		if entries[i].ProviderID != entries[j].ProviderID {
			return entries[i].ProviderID < entries[j].ProviderID
		}
		return entries[i].ModelID < entries[j].ModelID
	})
	return entries[0]
}

func providerMatchCandidates(providerID string) []string {
	provider := normalizeModelCatalogID(providerID)
	if provider == "" {
		return nil
	}
	aliases := map[string][]string{
		"zai":            {"zai", "zhipu-coding"},
		"zhipu":          {"zhipu"},
		"anthropic":      {"anthropic"},
		"openai":         {"openai"},
		"google":         {"gemini"},
		"openrouter":     {"openrouter"},
		"groq":           {"groq"},
		"mistral":        {"mistral"},
		"deepseek":       {"deepseek"},
		"moonshot":       {"moonshot", "kimi"},
		"kimi":           {"kimi"},
		"minimax":        {"minimax", "minimax-china"},
		"github-copilot": {"copilot"},
		"xai":            {"xai"},
		"aliyun":         {"aliyun"},
		"xiaomi-mimo":    {"xiaomi-mimo"},
	}
	list := []string{provider}
	if extra, ok := aliases[provider]; ok {
		list = append(list, extra...)
	}
	seen := make(map[string]struct{}, len(list))
	out := make([]string, 0, len(list))
	for _, item := range list {
		normalized := normalizeModelCatalogID(item)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func providerModelKey(providerID, modelID string) string {
	return providerID + "\x00" + modelID
}

func normalizeModelCatalogID(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func shortSHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}

func newModelCatalogResolveHandler(manager *modelCatalogManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		modelID := strings.TrimSpace(r.URL.Query().Get("model_id"))
		if modelID == "" {
			http.Error(w, "missing model_id", http.StatusBadRequest)
			return
		}
		var resp modelCatalogResolveResponse
		if snapshot := manager.snapshot.Load(); snapshot != nil {
			resp = snapshot.resolve(r.URL.Query().Get("provider_id"), modelID)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func newModelCatalogStatusHandler(manager *modelCatalogManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		state := manager.loadStatus()
		if snapshot := manager.snapshot.Load(); snapshot != nil && state.RowCount == 0 {
			state.RowCount = len(snapshot.Entries)
		}
		resp := modelCatalogStatusResponse{
			SourceRef:     state.SourceRef,
			SourceSHA:     state.SourceSHA,
			LastAttemptAt: state.LastAttemptAt,
			LastSuccessAt: state.LastSuccessAt,
			LastError:     state.LastError,
			RowCount:      state.RowCount,
			Ready:         manager.snapshot.Load() != nil,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
