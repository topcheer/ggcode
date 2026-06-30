package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
)

func TestREPLRuntimeStatusBasic(t *testing.T) {
	r := &REPL{
		workingDir: "/test/workspace",
		cfg: &config.Config{
			Vendor:   "openai",
			Endpoint: "test-ep",
			Model:    "gpt-4",
			Language: "en",
		},
		model: Model{
			mode: permission.AutoMode,
		},
	}

	st := r.RuntimeStatus()

	if st.Workspace != "/test/workspace" {
		t.Errorf("Workspace = %q, want /test/workspace", st.Workspace)
	}
	if st.Vendor != "openai" {
		t.Errorf("Vendor = %q, want openai", st.Vendor)
	}
	if st.Model != "gpt-4" {
		t.Errorf("Model = %q, want gpt-4", st.Model)
	}
	if st.PermissionMode != "auto" {
		t.Errorf("PermissionMode = %q, want auto", st.PermissionMode)
	}
	if st.AgentBusy {
		t.Error("AgentBusy should be false (model.loading=false)")
	}
}

func TestREPLRuntimeStatusNilConfig(t *testing.T) {
	r := &REPL{
		workingDir: "/test/ws",
		model: Model{
			mode: permission.SupervisedMode,
		},
	}

	st := r.RuntimeStatus()

	if st.Vendor != "" {
		t.Error("Vendor should be empty with nil config")
	}
	if st.Workspace != "/test/ws" {
		t.Errorf("Workspace = %q", st.Workspace)
	}
}

func TestREPLSetWorkingDir(t *testing.T) {
	r := &REPL{}
	r.SetWorkingDir("/custom/path")
	if r.workingDir != "/custom/path" {
		t.Errorf("workingDir = %q, want /custom/path", r.workingDir)
	}
}

func TestREPLSetConfigStoresCfg(t *testing.T) {
	r := &REPL{}
	cfg := config.DefaultConfig()
	cfg.Vendor = "test-vendor"
	r.SetConfig(cfg)
	if r.cfg == nil {
		t.Fatal("cfg should be set")
	}
	if r.cfg.Vendor != "test-vendor" {
		t.Errorf("cfg.Vendor = %q, want test-vendor", r.cfg.Vendor)
	}
}
