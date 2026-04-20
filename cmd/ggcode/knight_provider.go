package main

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

func resolveKnightProvider(cfg *config.Config, mainResolved *config.ResolvedEndpoint, mainProv provider.Provider) (*config.ResolvedEndpoint, provider.Provider, error) {
	knightResolved, err := cfg.ResolveKnightEndpoint()
	if err != nil {
		return nil, nil, fmt.Errorf("resolving knight endpoint: %w", err)
	}
	if knightResolved == nil {
		return mainResolved, mainProv, nil
	}
	if mainResolved != nil &&
		knightResolved.VendorID == mainResolved.VendorID &&
		knightResolved.EndpointID == mainResolved.EndpointID &&
		knightResolved.Model == mainResolved.Model {
		return mainResolved, mainProv, nil
	}
	knightProv, err := provider.NewProvider(knightResolved)
	if err != nil {
		return nil, nil, fmt.Errorf("creating knight provider: %w", err)
	}
	return knightResolved, knightProv, nil
}
