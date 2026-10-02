package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// MobileFileSender is the subset of the tunnel broker needed by the mobile
// file tool. Defined here (not by importing internal/tunnel) so the tool
// package keeps zero reverse dependencies; the TUI injects a broker adapter
// at share start (tunnel.go), the same pattern SetIMManager uses for IM.
type MobileFileSender interface {
	SendFileToMobile(ctx context.Context, path, caption string) (MobileFileSendResult, error)
}

// MobileFileSendResult mirrors tunnel.MobileFileResult without the import.
type MobileFileSendResult struct {
	FileID   string
	Filename string
	Mime     string
	Size     int64
	Chunks   int
	SHA256   string
}

// MobileFileTool lets the agent push a file (any format, ≤50 MiB) to the
// connected mobile app over the encrypted tunnel. Chunking, sha256
// verification and the size cap are enforced by the tunnel layer; this tool
// is the thin, discoverable agent surface.
type MobileFileTool struct {
	Sender MobileFileSender
}

func (t MobileFileTool) Name() string { return "send_file_to_mobile" }

func (t MobileFileTool) Description() string {
	if t.Sender == nil {
		return "Send a file (any format, up to 50 MiB) to the connected mobile app. " +
			"No mobile device is connected right now - start a mobile share session (QR) first."
	}
	return "Send a file to the connected mobile app over the encrypted tunnel. " +
		"Accepts any format (images, PDFs, archives, logs) up to 50 MiB. " +
		"The phone verifies sha256 before saving; the file renders as a card with share/save actions. " +
		"Parameters: path (required, local file), caption (optional, shown on the card)."
}

func (t MobileFileTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Path of the file to send (absolute or workspace-relative)."},
			"caption": {"type": "string", "description": "Optional caption shown on the mobile file card."}
		},
		"required": ["path"]
	}`)
}

// Execute validates params and delegates to the injected sender. Errors are
// returned as error Results (the agent relays them), carrying the why+fix.
func (t MobileFileTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Sender == nil {
		return Result{IsError: true, Content: "No mobile device is connected. Start a mobile share session (QR) first, then retry."}, nil
	}
	var args struct {
		Path    string `json:"path"`
		Caption string `json:"caption"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	args.Path = strings.TrimSpace(args.Path)
	if args.Path == "" {
		return Result{IsError: true, Content: "path is required."}, nil
	}
	if fi, err := os.Stat(args.Path); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("cannot access %s: %v", args.Path, err)}, nil
	} else if fi.IsDir() {
		return Result{IsError: true, Content: fmt.Sprintf("%s is a directory; send a single file.", args.Path)}, nil
	}
	res, err := t.Sender.SendFileToMobile(ctx, args.Path, args.Caption)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Sent %s (%s, %d bytes) to mobile as %d chunks.\n", res.Filename, humanSize(res.Size), res.Size, res.Chunks)
	if res.SHA256 != "" {
		fmt.Fprintf(&sb, "sha256: %s (phone verifies before saving)\n", res.SHA256)
	}
	sb.WriteString("The file appears as a card on the phone with share/save actions.")
	return Result{Content: sb.String()}, nil
}

// humanSize renders bytes in a compact human-readable form.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
