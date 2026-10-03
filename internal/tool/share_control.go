package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ShareController is the frontend surface the share-control tools drive.
// The frontend (TUI/desktop) injects an adapter that owns the real
// agentruntime.TunnelHost; the tool package keeps zero reverse dependencies
// (same pattern as MobileFileSender / SetIMManager).
//
// Security note: StartShare only opens a relay room; a human must still scan
// the QR / type the URL on the phone to join, so giving the agent start/stop
// control does not bypass any pairing step - it matches what /share already
// does interactively.
type ShareController interface {
	// StartAgentShare starts a mobile tunnel share and returns the full
	// connect URL the user must open (or scan) on the phone.
	StartAgentShare() (ShareStartResult, error)
	// StopAgentShare tears down the active share, if any. Stopping with no
	// active share is a no-op success.
	StopAgentShare() error
	// ShareActive reports whether a share session is currently running.
	ShareActive() bool
}

// ShareStartResult mirrors the connection details the frontend produced.
type ShareStartResult struct {
	ConnectURL string
	RoomID     string
}

// StartShareTool lets the agent open a mobile share session on the user's
// behalf. The full connect URL is returned in the result so the agent MUST
// relay it to the user - the user still completes pairing on the phone.
type StartShareTool struct {
	Controller ShareController
}

func (t StartShareTool) Name() string { return "start_share" }

func (t StartShareTool) Description() string {
	if t.Controller == nil {
		return "Start a mobile share session so the phone can connect to this agent. " +
			"Not available in this frontend (requires an interactive session with tunnel support)."
	}
	if t.Controller.ShareActive() {
		return "A mobile share session is ALREADY active - do not start another. " +
			"Use send_file_to_mobile to push files, or stop_share to end it."
	}
	return "Start a mobile share session (encrypted tunnel) so the user's phone can connect to this agent. " +
		"Returns the full connect URL which you MUST show to the user verbatim - they open it (or scan the QR shown in the UI) on the phone to complete pairing. " +
		"No parameters."
}

func (t StartShareTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

func (t StartShareTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Controller == nil {
		return Result{IsError: true, Content: "Share control is not available in this frontend."}, nil
	}
	if t.Controller.ShareActive() {
		return Result{IsError: true, Content: "A share session is already active. Use stop_share first, or send files with send_file_to_mobile."}, nil
	}
	res, err := t.Controller.StartAgentShare()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("share start failed: %v", err)}, nil
	}
	var sb strings.Builder
	sb.WriteString("Share session started.\n\n")
	sb.WriteString("Show this connect URL to the user VERBATIM (they open it on their phone, or scan the QR code now displayed in the UI):\n\n")
	sb.WriteString(res.ConnectURL + "\n")
	if res.RoomID != "" {
		fmt.Fprintf(&sb, "\nRoom: %s\n", res.RoomID)
	}
	sb.WriteString("\nThe phone completes pairing; until it connects, send_file_to_mobile will report no device. ")
	sb.WriteString("Use stop_share to end the session.")
	return Result{Content: sb.String()}, nil
}

// StopShareTool tears down the active mobile share session.
type StopShareTool struct {
	Controller ShareController
}

func (t StopShareTool) Name() string { return "stop_share" }

func (t StopShareTool) Description() string {
	if t.Controller == nil {
		return "Stop the active mobile share session. " +
			"Not available in this frontend (requires an interactive session with tunnel support)."
	}
	if !t.Controller.ShareActive() {
		return "No share session is active - nothing to stop."
	}
	return "Stop the active mobile share session (disconnects the phone and closes the encrypted tunnel). No parameters."
}

func (t StopShareTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

func (t StopShareTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Controller == nil {
		return Result{IsError: true, Content: "Share control is not available in this frontend."}, nil
	}
	if !t.Controller.ShareActive() {
		return Result{Content: "No active share session; nothing to stop."}, nil
	}
	if err := t.Controller.StopAgentShare(); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("share stop failed: %v", err)}, nil
	}
	return Result{Content: "Share session stopped; the tunnel is closed."}, nil
}
