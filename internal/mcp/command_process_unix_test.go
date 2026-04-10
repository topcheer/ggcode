//go:build darwin || linux

package mcp

import (
	"os/exec"
	"testing"
)

func TestConfigureMCPCommandProcessDetachesTTYSession(t *testing.T) {
	cmd := exec.Command("echo")
	configureMCPCommandProcess(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("expected SysProcAttr to be configured")
	}
	if !cmd.SysProcAttr.Setsid {
		t.Fatal("expected stdio MCP commands to start in a new session")
	}
}
