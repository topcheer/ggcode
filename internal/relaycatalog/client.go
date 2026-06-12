package relaycatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/tunnel"
	"github.com/topcheer/ggcode/internal/util"
)

const (
	resolvePath            = "/model-catalog/resolve"
	modelLimitsRelayURLEnv = "GGCODE_MODEL_LIMITS_RELAY_URL"
	generalRelayURLEnv     = "GGCODE_RELAY_URL"
	defaultResolveTimeout  = 8 * time.Second
)

type ResolveResponse struct {
	Found             bool      `json:"found"`
	MatchKind         string    `json:"match_kind,omitempty"`
	MatchedProviderID string    `json:"matched_provider_id,omitempty"`
	MatchedModelID    string    `json:"matched_model_id,omitempty"`
	ContextWindow     int       `json:"context_window,omitempty"`
	MaxOutputTokens   int       `json:"max_output_tokens,omitempty"`
	CatalogVersion    string    `json:"catalog_version,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
}

func RelayURL() string {
	for _, key := range []string{modelLimitsRelayURLEnv, generalRelayURLEnv} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return tunnel.DefaultRelayURL
}

func Resolve(ctx context.Context, relayURL, providerID, modelID string) (ResolveResponse, error) {
	relayURL = strings.TrimSpace(relayURL)
	if relayURL == "" {
		relayURL = RelayURL()
	}
	endpoint, err := tunnel.HTTPEndpoint(relayURL, resolvePath)
	if err != nil {
		return ResolveResponse{}, err
	}
	reqURL, err := url.Parse(endpoint)
	if err != nil {
		return ResolveResponse{}, fmt.Errorf("parse resolve endpoint: %w", err)
	}
	query := reqURL.Query()
	if trimmed := strings.TrimSpace(providerID); trimmed != "" {
		query.Set("provider_id", trimmed)
	}
	query.Set("model_id", strings.TrimSpace(modelID))
	reqURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return ResolveResponse{}, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := util.NewInsecureAwareClient(defaultResolveTimeout).Do(req)
	if err != nil {
		return ResolveResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return ResolveResponse{}, fmt.Errorf("relay model catalog resolve: %s", msg)
	}
	var decoded ResolveResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return ResolveResponse{}, fmt.Errorf("decode relay model catalog resolve: %w", err)
	}
	return decoded, nil
}
