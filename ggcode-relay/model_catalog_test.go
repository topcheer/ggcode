package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRelayStoreReplaceAndLoadModelCatalog(t *testing.T) {
	store := newStoreForTest(t)
	now := time.Now().UTC().Truncate(time.Second)
	state := modelCatalogSyncState{
		SourceRef:     modelCatalogSourceRef,
		SourceSHA:     "sha-123",
		LastAttemptAt: now,
		LastSuccessAt: now,
		RowCount:      2,
	}
	entries := []modelCatalogEntry{
		{ProviderID: "zai", ModelID: "glm-5-turbo", ContextWindow: 200000, MaxOutputTokens: 8192, SourceSHA: "sha-123", UpdatedAt: now},
		{ProviderID: "openai", ModelID: "gpt-5.4", ContextWindow: 400000, MaxOutputTokens: 32768, SourceSHA: "sha-123", UpdatedAt: now},
	}
	if err := store.replaceModelCatalog(state, entries); err != nil {
		t.Fatal(err)
	}

	loadedState, loadedEntries, err := store.loadModelCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if loadedState.SourceSHA != "sha-123" {
		t.Fatalf("source_sha = %q, want sha-123", loadedState.SourceSHA)
	}
	if len(loadedEntries) != 2 {
		t.Fatalf("loaded entries = %d, want 2", len(loadedEntries))
	}
	if loadedEntries[0].ProviderID != "openai" || loadedEntries[1].ProviderID != "zai" {
		t.Fatalf("entries not sorted as expected: %+v", loadedEntries)
	}
}

func TestModelCatalogSnapshotResolvePrefersProviderAliasExact(t *testing.T) {
	now := time.Now().UTC()
	snapshot := buildModelCatalogSnapshot(modelCatalogSyncState{
		SourceRef:     modelCatalogSourceRef,
		SourceSHA:     "sha-123",
		LastSuccessAt: now,
	}, []modelCatalogEntry{
		{ProviderID: "zhipu-coding", ModelID: "glm-5-turbo", ContextWindow: 200000, MaxOutputTokens: 8192, UpdatedAt: now},
		{ProviderID: "openrouter", ModelID: "glm-5-turbo", ContextWindow: 128000, MaxOutputTokens: 4096, UpdatedAt: now},
	})

	resp := snapshot.resolve("zai", "glm-5-turbo")
	if !resp.Found {
		t.Fatal("expected found response")
	}
	if resp.MatchKind != "provider_model_exact" {
		t.Fatalf("match_kind = %q, want provider_model_exact", resp.MatchKind)
	}
	if resp.MatchedProviderID != "zhipu-coding" {
		t.Fatalf("matched_provider_id = %q, want zhipu-coding", resp.MatchedProviderID)
	}
}

func TestModelCatalogSnapshotResolveFallsBackToPrefix(t *testing.T) {
	now := time.Now().UTC()
	snapshot := buildModelCatalogSnapshot(modelCatalogSyncState{
		SourceRef:     modelCatalogSourceRef,
		SourceSHA:     "sha-456",
		LastSuccessAt: now,
	}, []modelCatalogEntry{
		{ProviderID: "anthropic", ModelID: "claude-sonnet-4-5-20250929", ContextWindow: 200000, MaxOutputTokens: 8192, UpdatedAt: now},
		{ProviderID: "anthropic", ModelID: "claude-sonnet-4-20250514", ContextWindow: 200000, MaxOutputTokens: 8192, UpdatedAt: now},
	})

	resp := snapshot.resolve("anthropic", "claude-sonnet-4")
	if !resp.Found {
		t.Fatal("expected prefix match")
	}
	if resp.MatchKind != "model_prefix" {
		t.Fatalf("match_kind = %q, want model_prefix", resp.MatchKind)
	}
	if resp.MatchedModelID != "claude-sonnet-4-5-20250929" {
		t.Fatalf("matched_model_id = %q, want claude-sonnet-4-5-20250929", resp.MatchedModelID)
	}
}

func TestModelCatalogResolveHandler(t *testing.T) {
	now := time.Now().UTC()
	manager := &modelCatalogManager{}
	manager.snapshot.Store(buildModelCatalogSnapshot(modelCatalogSyncState{
		SourceRef:     modelCatalogSourceRef,
		SourceSHA:     "sha-789",
		LastSuccessAt: now,
	}, []modelCatalogEntry{
		{ProviderID: "openai", ModelID: "gpt-5.4", ContextWindow: 400000, MaxOutputTokens: 32768, UpdatedAt: now},
	}))
	manager.storeStatus(modelCatalogSyncState{
		SourceRef:     modelCatalogSourceRef,
		SourceSHA:     "sha-789",
		LastSuccessAt: now,
		RowCount:      1,
	})

	req := httptest.NewRequest(http.MethodGet, "/model-catalog/resolve?provider_id=openai&model_id=gpt-5.4", nil)
	rec := httptest.NewRecorder()
	newModelCatalogResolveHandler(manager).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp modelCatalogResolveResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Found {
		t.Fatal("expected found response")
	}
	if resp.ContextWindow != 400000 {
		t.Fatalf("context_window = %d, want 400000", resp.ContextWindow)
	}
}
