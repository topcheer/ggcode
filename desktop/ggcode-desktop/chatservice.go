package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"sync"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/update"
	"github.com/topcheer/ggcode/internal/version"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// ─── Desktop-only config (window, theme, last workdir) ─────────────

type DesktopConfig struct {
	WorkDir  string `json:"workDir"`
	Theme    string `json:"theme"`
	FontSize int    `json:"fontSize"`
	WinX     int    `json:"winX"`
	WinY     int    `json:"winY"`
	WinW     int    `json:"winW"`
	WinH     int    `json:"winH"`
}

func desktopConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ggcode", "desktop-config.json")
}

func loadDesktopConfig() DesktopConfig {
	dc := DesktopConfig{Theme: "system", FontSize: 14, WinW: 1200, WinH: 800}
	data, err := os.ReadFile(desktopConfigPath())
	if err == nil {
		_ = json.Unmarshal(data, &dc)
	}
	if dc.FontSize == 0 {
		dc.FontSize = 14
	}
	return dc
}

func saveDesktopConfig(dc DesktopConfig) error {
	_ = os.MkdirAll(filepath.Dir(desktopConfigPath()), 0o755)
	data, _ := json.MarshalIndent(dc, "", "  ")
	return os.WriteFile(desktopConfigPath(), data, 0o644)
}

// ─── ChatService ───────────────────────────────────────────────────

type ChatService struct {
	mu            sync.Mutex
	cfg           *config.Config
	dc            DesktopConfig
	sessionStore  *session.JSONLStore
	agent         *agent.Agent
	app           *application.App
	eventName     string
	updateService *update.Service
}

func NewChatService() *ChatService {
	dc := loadDesktopConfig()
	return &ChatService{
		cfg:       config.DefaultConfig(),
		dc:        dc,
		eventName: "ggcode:chat:stream",
	}
}

func (c *ChatService) SetApp(app *application.App) { c.app = app }

// ─── Desktop config ────────────────────────────────────────────────

func (c *ChatService) GetDesktopConfig() DesktopConfig { return c.dc }

func (c *ChatService) SaveDesktopConfig(dc DesktopConfig) error {
	c.dc = dc
	return saveDesktopConfig(dc)
}

// ─── Initialization flow (mirrors cmd/ggcode/root.go) ──────────────

// InitFromWorkDir loads config for the given workspace directory.
// This mirrors: resolveConfigFilePath → config.LoadWithInstance.
func (c *ChatService) InitFromWorkDir(dir string) error {
	cfgPath := resolveConfigFilePath(dir)
	log.Printf("[desktop] InitFromWorkDir: dir=%s cfgPath=%s", dir, cfgPath)

	cfg, err := config.LoadWithInstance(cfgPath, dir)
	if err != nil {
		log.Printf("[desktop] LoadWithInstance error: %v", err)
		return fmt.Errorf("load config: %w", err)
	}

	c.mu.Lock()
	c.cfg = cfg
	c.agent = nil
	c.mu.Unlock()

	// Persist for next launch.
	c.dc.WorkDir = dir
	_ = saveDesktopConfig(c.dc)

	// Init session store.
	store, err := session.NewDefaultStore()
	if err != nil {
		log.Printf("[desktop] session store: %v", err)
	} else {
		c.sessionStore = store
	}

	// Init update service.
	execPath, _ := os.Executable()
	c.updateService = update.NewService(version.Version, execPath, cfgPath, dir)

	log.Printf("[desktop] Loaded: dir=%s vendor=%s endpoint=%s model=%s", dir, cfg.Vendor, cfg.Endpoint, cfg.Model)
	return nil
}

// NeedsOnboard mirrors cfg.NeedsOnboard().
func (c *ChatService) NeedsOnboard() bool {
	return c.cfg.NeedsOnboard()
}

// SelectWorkDir opens a native directory picker.
// SelectWorkDir is called from frontend after directory is chosen.
func (c *ChatService) SetWorkDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("not a valid directory: %s", dir)
	}
	return c.InitFromWorkDir(dir)
}

// resolveConfigFilePath checks local dir then falls back to global config.
// This mirrors cmd/ggcode/root.go:resolveConfigFilePath.
func resolveConfigFilePath(dir string) string {
	for _, candidate := range []string{
		filepath.Join(dir, "ggcode.yaml"),
		filepath.Join(dir, ".ggcode", "ggcode.yaml"),
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return config.ConfigPath()
}

// ─── Onboard ───────────────────────────────────────────────────────

// GetVendorPresets returns built-in vendor templates for the onboard wizard.
func (c *ChatService) GetVendorPresets() []config.VendorPreset {
	return config.VendorPresets()
}

// CompleteOnboard applies onboard results (mirrors cmd/ggcode/onboard.go).
func (c *ChatService) CompleteOnboard(lang, vendorID, endpointID, apiKey, model, mode string, knight, a2a bool, imAdapters map[string]config.IMAdapterConfig) error {
	cfg := c.cfg
	cfg.Language = lang
	cfg.Vendor = vendorID
	cfg.Endpoint = endpointID
	cfg.Model = model

	if apiKey != "" {
		vc, ok := cfg.Vendors[vendorID]
		if ok {
			vc.APIKey = apiKey
			cfg.Vendors[vendorID] = vc
		}
	}

	if ep, ok := cfg.Vendors[vendorID].Endpoints[endpointID]; ok {
		ep.SelectedModel = model
		cfg.Vendors[vendorID].Endpoints[endpointID] = ep
	}

	cfg.DefaultMode = mode
	if knight {
		cfg.KnightConfig = config.KnightConfig{Enabled: true}
	}
	if a2a {
		cfg.A2A = config.A2AConfig{Disabled: false}
	}

	if len(imAdapters) > 0 {
		if cfg.IM.Adapters == nil {
			cfg.IM.Adapters = make(map[string]config.IMAdapterConfig)
		}
		for name, acfg := range imAdapters {
			cfg.IM.Adapters[name] = acfg
		}
		cfg.IM.Enabled = true
	}

	cfg.FirstRun = false
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	// Reset agent so next message uses new config.
	c.mu.Lock()
	c.agent = nil
	c.mu.Unlock()

	return nil
}

// DiscoverModels fetches available models for the current endpoint.
func (c *ChatService) DiscoverModels() ([]string, error) {
	resolved, err := c.cfg.ResolveActiveEndpoint()
	if err != nil {
		return nil, err
	}
	return provider.DiscoverModels(context.Background(), resolved)
}

// ─── Chat ──────────────────────────────────────────────────────────

func (c *ChatService) Initialize() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.agent != nil {
		return nil
	}
	if c.cfg.Vendor == "" {
		return fmt.Errorf("no provider configured")
	}
	resolved, err := c.cfg.ResolveActiveEndpoint()
	if err != nil {
		return fmt.Errorf("resolve provider: %w", err)
	}
	prov, err := provider.NewProvider(resolved)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}
	registry := tool.NewRegistry()
	a := agent.NewAgent(prov, registry, "You are ggcode, an AI coding assistant.", c.cfg.MaxIterations)
	probeKey := provider.MakeProbeKey(resolved.VendorID, resolved.BaseURL, resolved.Model)
	a.SetProbeKey(probeKey)
	c.agent = a
	return nil
}

func (c *ChatService) SendMessage(text string) error {
	if err := c.Initialize(); err != nil {
		return err
	}
	c.mu.Lock()
	a := c.agent
	c.mu.Unlock()
	go func() {
		ctx := context.Background()
		content := []provider.ContentBlock{{Type: "text", Text: text}}
		err := a.RunStreamWithContent(ctx, content, func(event provider.StreamEvent) {
			c.emitStreamEvent(event)
		})
		if err != nil {
			c.emitStreamEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: err})
		}
	}()
	return nil
}

func (c *ChatService) ClearMessages() {
	c.mu.Lock()
	c.agent = nil
	c.mu.Unlock()
}

// ─── Sessions (via session.JSONLStore) ─────────────────────────────

// GetSessions returns all saved sessions (from session.JSONLStore).
func (c *ChatService) GetSessions() ([]SessionSummary, error) {
	if c.sessionStore == nil {
		return nil, nil
	}
	list, err := c.sessionStore.List()
	if err != nil {
		return nil, err
	}
	out := make([]SessionSummary, 0, len(list))
	for _, s := range list {
		out = append(out, SessionSummary{
			ID:        s.ID,
			Title:     s.Title,
			UpdatedAt: s.UpdatedAt.Format("2006-01-02 15:04"),
			Workspace: s.Workspace,
			Vendor:    s.Vendor,
			Model:     s.Model,
			MsgCount:  len(s.Messages),
		})
	}
	return out, nil
}

// ResumeSession loads a saved session and restores its messages into the agent.
func (c *ChatService) ResumeSession(id string) error {
	if c.sessionStore == nil {
		return fmt.Errorf("no session store")
	}
	ses, err := c.sessionStore.Load(id)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}

	// Reinitialize agent.
	if err := c.Initialize(); err != nil {
		return err
	}

	c.mu.Lock()
	a := c.agent
	c.mu.Unlock()

	// Inject saved messages into agent.
	for _, msg := range ses.Messages {
		a.AddMessage(msg)
	}

	return nil
}

// DeleteSession removes a session.
func (c *ChatService) DeleteSession(id string) error {
	if c.sessionStore == nil {
		return nil
	}
	return c.sessionStore.Delete(id)
}

// ─── Config: Provider ──────────────────────────────────────────────

func (c *ChatService) GetVendors() []VendorInfo {
	vendors := make([]VendorInfo, 0, len(c.cfg.Vendors))
	for name, vc := range c.cfg.Vendors {
		endpoints := make([]EndpointInfo, 0, len(vc.Endpoints))
		for epName, ep := range vc.Endpoints {
			endpoints = append(endpoints, EndpointInfo{
				Name: epName, DisplayName: ep.DisplayName,
				Models: ep.Models, SelectedModel: ep.SelectedModel,
				DefaultModel: ep.DefaultModel, Protocol: ep.Protocol, BaseURL: ep.BaseURL,
			})
		}
		vendors = append(vendors, VendorInfo{Name: name, DisplayName: vc.DisplayName, Endpoints: endpoints})
	}
	return vendors
}

func (c *ChatService) GetActiveProvider() ActiveProviderInfo {
	return ActiveProviderInfo{Vendor: c.cfg.Vendor, Endpoint: c.cfg.Endpoint, Model: c.cfg.Model}
}

func (c *ChatService) SetActiveProvider(vendor, endpoint, model string) error {
	c.cfg.Vendor = vendor
	c.cfg.Endpoint = endpoint
	if model != "" {
		c.cfg.Model = model
	}
	c.mu.Lock()
	c.agent = nil
	c.mu.Unlock()
	return c.cfg.SaveScoped("instance")
}

func (c *ChatService) SetEndpointAPIKey(vendor, endpoint, apiKey string) error {
	return c.cfg.SetEndpointAPIKey(vendor, endpoint, apiKey, true)
}

// ─── Config: IM ────────────────────────────────────────────────────

func (c *ChatService) GetIMAdapters() []IMAdapterInfo {
	adapters := make([]IMAdapterInfo, 0, len(c.cfg.IM.Adapters))
	for name, a := range c.cfg.IM.Adapters {
		adapters = append(adapters, IMAdapterInfo{
			Name: name, Platform: a.Platform, Enabled: a.Enabled,
			OutputMode: a.OutputMode, AllowFrom: a.AllowFrom,
		})
	}
	return adapters
}

func (c *ChatService) SetIMAdapterEnabled(name string, enabled bool) error {
	return c.cfg.SetIMAdapterEnabled(name, enabled)
}

func (c *ChatService) AddIMAdapter(name, platform string) error {
	return c.cfg.AddIMAdapter(name, config.IMAdapterConfig{Platform: platform, Enabled: true})
}

func (c *ChatService) RemoveIMAdapter(name string) error {
	return c.cfg.RemoveIMAdapter(name)
}

// ─── Config: General Settings ──────────────────────────────────────

func (c *ChatService) GetSettings() GeneralSettings {
	return GeneralSettings{
		MaxIterations: c.cfg.MaxIterations,
		Language:      c.cfg.Language,
		Version:       version.Version,
	}
}

func (c *ChatService) SetMaxIterations(n int) error {
	c.cfg.MaxIterations = n
	return c.cfg.SaveScoped("instance")
}

func (c *ChatService) SetLanguage(lang string) error {
	c.cfg.Language = lang
	return c.cfg.SaveScoped("instance")
}

// ─── Updates ───────────────────────────────────────────────────────

func (c *ChatService) CheckForUpdates() UpdateInfo {
	if c.updateService == nil {
		return UpdateInfo{CurrentVersion: version.Version, Error: "unavailable"}
	}
	result, err := c.updateService.Check(context.Background())
	if err != nil {
		return UpdateInfo{CurrentVersion: version.Version, Error: err.Error()}
	}
	return UpdateInfo{
		CurrentVersion: result.CurrentVersion,
		LatestVersion:  result.LatestVersion,
		HasUpdate:      result.HasUpdate,
	}
}

// ─── Helpers ───────────────────────────────────────────────────────

func homeDir() string {
	if u, err := user.Current(); err == nil {
		return u.HomeDir
	}
	return os.Getenv("HOME")
}

func (c *ChatService) emitStreamEvent(event provider.StreamEvent) {
	if c.app == nil {
		return
	}
	payload := map[string]interface{}{"type": streamEventTypeString(event.Type)}
	switch event.Type {
	case provider.StreamEventText, provider.StreamEventReasoning:
		payload["text"] = event.Text
	case provider.StreamEventToolCallDone:
		payload["tool"] = map[string]interface{}{
			"id": event.Tool.ID, "name": event.Tool.Name,
			"input": json.RawMessage(event.Tool.Arguments),
		}
	case provider.StreamEventToolResult:
		payload["tool"] = map[string]interface{}{"id": event.Tool.ID, "name": event.Tool.Name}
		payload["result"] = event.Result
		payload["is_error"] = event.IsError
	case provider.StreamEventDone:
		if event.Usage != nil {
			payload["usage"] = map[string]interface{}{
				"input_tokens":  event.Usage.InputTokens,
				"output_tokens": event.Usage.OutputTokens,
			}
		}
	case provider.StreamEventError:
		if event.Error != nil {
			payload["error"] = event.Error.Error()
		}
	case provider.StreamEventSystem:
		payload["text"] = event.Text
	}
	data, _ := json.Marshal(payload)
	c.app.Event.Emit(c.eventName, string(data))
}

func streamEventTypeString(t provider.StreamEventType) string {
	switch t {
	case provider.StreamEventText:
		return "text"
	case provider.StreamEventToolCallChunk:
		return "tool_call_chunk"
	case provider.StreamEventToolCallDone:
		return "tool_call_done"
	case provider.StreamEventToolResult:
		return "tool_result"
	case provider.StreamEventDone:
		return "done"
	case provider.StreamEventError:
		return "error"
	case provider.StreamEventReasoning:
		return "reasoning"
	case provider.StreamEventSystem:
		return "system"
	default:
		return "unknown"
	}
}

// ─── Data types for frontend ───────────────────────────────────────

type SessionSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updatedAt"`
	Workspace string `json:"workspace"`
	Vendor    string `json:"vendor"`
	Model     string `json:"model"`
	MsgCount  int    `json:"msgCount"`
}

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
	DefaultModel  string   `json:"defaultModel"`
	Protocol      string   `json:"protocol"`
	BaseURL       string   `json:"baseUrl"`
}

type ActiveProviderInfo struct {
	Vendor   string `json:"vendor"`
	Endpoint string `json:"endpoint"`
	Model    string `json:"model"`
}

type IMAdapterInfo struct {
	Name       string   `json:"name"`
	Platform   string   `json:"platform"`
	Enabled    bool     `json:"enabled"`
	OutputMode string   `json:"outputMode"`
	AllowFrom  []string `json:"allowFrom"`
}

type GeneralSettings struct {
	MaxIterations int    `json:"maxIterations"`
	Language      string `json:"language"`
	Version       string `json:"version"`
}

type UpdateInfo struct {
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	HasUpdate      bool   `json:"hasUpdate"`
	Error          string `json:"error,omitempty"`
}
