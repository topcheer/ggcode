package main

import (
	"github.com/topcheer/ggcode/internal/config"
)

// ChatService is the Wails-bound service that exposes chat and config
// functionality to the frontend via Wails bindings.
type ChatService struct {
	cfg *config.Config
}

// NewChatService creates a new ChatService.
func NewChatService() *ChatService {
	cfg := config.DefaultConfig()
	return &ChatService{
		cfg: cfg,
	}
}

// --- Config/Provider methods ---

// GetVendors returns all configured vendor names.
func (c *ChatService) GetVendors() []VendorInfo {
	vendors := make([]VendorInfo, 0, len(c.cfg.Vendors))
	for name, vc := range c.cfg.Vendors {
		endpoints := make([]EndpointInfo, 0, len(vc.Endpoints))
		for epName, ep := range vc.Endpoints {
			endpoints = append(endpoints, EndpointInfo{
				Name:          epName,
				DisplayName:   ep.DisplayName,
				Models:        ep.Models,
				SelectedModel: ep.SelectedModel,
				Protocol:      ep.Protocol,
			})
		}
		vendors = append(vendors, VendorInfo{
			Name:        name,
			DisplayName: vc.DisplayName,
			Endpoints:   endpoints,
		})
	}
	return vendors
}

// GetActiveProvider returns the currently active vendor/endpoint/model.
func (c *ChatService) GetActiveProvider() ActiveProviderInfo {
	return ActiveProviderInfo{
		Vendor:   c.cfg.Vendor,
		Endpoint: c.cfg.Endpoint,
		Model:    c.cfg.Model,
	}
}

// SetActiveProvider changes the active vendor/endpoint/model.
func (c *ChatService) SetActiveProvider(vendor, endpoint, model string) error {
	c.cfg.Vendor = vendor
	c.cfg.Endpoint = endpoint
	if model != "" {
		c.cfg.Model = model
	}
	return nil
}

// --- Data types for frontend ---

type VendorInfo struct {
	Name        string         `json:"name"`
	DisplayName string         `json:"displayName"`
	Endpoints   []EndpointInfo `json:"endpoints"`
}

type EndpointInfo struct {
	Name          string   `json:"name"`
	DisplayName   string   `json:"displayName"`
	Models        []string `json:"models"`
	SelectedModel string   `json:"selectedModel"`
	Protocol      string   `json:"protocol"`
}

type ActiveProviderInfo struct {
	Vendor   string `json:"vendor"`
	Endpoint string `json:"endpoint"`
	Model    string `json:"model"`
}
