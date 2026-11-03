package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/lsp"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

func TestLSPInstallInFlight_SetAtSubmission(t *testing.T) {
	m := NewModel(nil, nil)
	m.inspectorPanel = &inspectorPanelState{
		kind:              inspectorPanelLSPInstall,
		lspInstallOptions: []lsp.InstallOption{{ID: "npm", Label: "npm", Command: "npm i -g foo-lsp"}},
	}
	items := []inspectorPanelItem{{ID: "lsp-go"}}

	newM, _ := m.handleInspectorLSPInstallAction(items)
	if !newM.lspInstallInFlight {
		t.Fatal("expected lspInstallInFlight to be set when the multi-option install command is submitted")
	}
}

func TestLSPInstallCache_DroppedOnShellDone(t *testing.T) {
	m := NewModel(nil, nil)
	m.activeShellRunID = 7
	m.lspInstallInFlight = true

	newM, _ := m.handleShellCommandDoneMsg(shellCommandDoneMsg{RunID: 7, Status: toolpkg.CommandJobCompleted})
	if newM.lspInstallInFlight {
		t.Fatal("expected lspInstallInFlight to be reset when the install shell command completes")
	}
}

func TestLSPInstallCache_StaleRunIDDoesNotConsumeFlag(t *testing.T) {
	m := NewModel(nil, nil)
	m.activeShellRunID = 7
	m.lspInstallInFlight = true

	newM, _ := m.handleShellCommandDoneMsg(shellCommandDoneMsg{RunID: 3, Status: toolpkg.CommandJobFailed})
	if !newM.lspInstallInFlight {
		t.Fatal("stale RunID done message must not reset lspInstallInFlight")
	}
}

func TestLSPInstallCache_FailedInstallStillDrops(t *testing.T) {
	m := NewModel(nil, nil)
	m.activeShellRunID = 2
	m.lspInstallInFlight = true

	newM, _ := m.handleShellCommandDoneMsg(shellCommandDoneMsg{RunID: 2, Status: toolpkg.CommandJobFailed, ErrText: "boom"})
	if newM.lspInstallInFlight {
		t.Fatal("expected lspInstallInFlight to be reset on failed install too")
	}
}
