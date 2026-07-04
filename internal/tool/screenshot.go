package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/image"
)

// ScreenshotTool captures screenshots of the screen, a specific display,
// a window, or a screen region. Supports cursor inclusion, delay, format
// selection, auto-resize, and listing available displays/windows.
type ScreenshotTool struct{}

type screenshotParams struct {
	Action     string            `json:"action"`      // capture, list_displays, list_windows
	Window     string            `json:"window"`      // window title/app name
	Display    int               `json:"display"`     // 1-based monitor index
	Region     *screenshotRegion `json:"region"`      // rectangular area
	Cursor     bool              `json:"cursor"`      // include cursor
	DelayMs    int               `json:"delay_ms"`    // delay before capture
	Format     string            `json:"format"`      // png or jpeg
	Quality    int               `json:"quality"`     // jpeg quality 1-100
	OutputPath string            `json:"output_path"` // save to file
	MaxWidth   int               `json:"max_width"`   // auto-resize max width
}

type screenshotRegion struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

func (ScreenshotTool) Name() string { return "screenshot" }

func (ScreenshotTool) Description() string {
	return "Capture a screenshot of the entire screen, a specific display, a window (by title or app name), or a screen region. " +
		"Use action=\"list_displays\" to see available monitors, action=\"list_windows\" to see capturable windows. " +
		"The screenshot is returned as an image for visual analysis. " +
		"Supports cursor inclusion, delay, PNG/JPEG format, and auto-resize to control context window usage."
}

func (ScreenshotTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "action": {
      "type": "string",
      "enum": ["capture", "list_displays", "list_windows"],
      "description": "capture: take a screenshot (default). list_displays: list available monitors. list_windows: list capturable windows.",
      "default": "capture"
    },
    "window": {
      "type": "string",
      "description": "Capture this window. Matched by window title substring or application name (case-insensitive). Example: \"Safari\", \"VS Code\", \"terminal\". Use action=list_windows to find available windows."
    },
    "display": {
      "type": "integer",
      "description": "Monitor index (1-based). Default: primary display. Use action=list_displays to see available monitors.",
      "minimum": 1
    },
    "region": {
      "type": "object",
      "description": "Capture a specific screen region instead of full screen.",
      "properties": {
        "x": {"type": "integer", "minimum": 0},
        "y": {"type": "integer", "minimum": 0},
        "width": {"type": "integer", "minimum": 1},
        "height": {"type": "integer", "minimum": 1}
      },
      "required": ["x", "y", "width", "height"]
    },
    "cursor": {
      "type": "boolean",
      "description": "Include the mouse cursor in the capture. Default: false (cursor excluded).",
      "default": false
    },
    "delay_ms": {
      "type": "integer",
      "description": "Delay before capture in milliseconds. Useful for capturing UI states that change on hover or focus.",
      "minimum": 0,
      "default": 0
    },
    "format": {
      "type": "string",
      "enum": ["png", "jpeg"],
      "description": "Output image format. PNG is lossless (larger). JPEG is compressed (smaller, better for context efficiency). Default: png.",
      "default": "png"
    },
    "quality": {
      "type": "integer",
      "minimum": 1,
      "maximum": 100,
      "description": "JPEG quality (1-100). Only used when format=jpeg. Default: 85.",
      "default": 85
    },
    "output_path": {
      "type": "string",
      "description": "Save the screenshot to this file path. If omitted, the screenshot is only returned inline for analysis."
    },
    "max_width": {
      "type": "integer",
      "description": "Maximum width in pixels. If the screenshot is wider, it is auto-resized (maintaining aspect ratio). Default: 1920.",
      "default": 1920,
      "minimum": 100
    }
  }
}`)
}

func (t ScreenshotTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var params screenshotParams
	if err := json.Unmarshal(input, &params); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid parameters: %v", err)}, nil
	}

	action := params.Action
	if action == "" {
		action = "capture"
	}

	switch action {
	case "list_displays":
		return t.executeListDisplays()
	case "list_windows":
		return t.executeListWindows()
	case "capture":
		return t.executeCapture(params)
	default:
		return Result{IsError: true, Content: fmt.Sprintf("unknown action %q (use capture, list_displays, or list_windows)", action)}, nil
	}
}

func (t ScreenshotTool) executeCapture(params screenshotParams) (Result, error) {
	opts := image.ScreenshotOptions{
		Window:     params.Window,
		Display:    params.Display,
		Cursor:     params.Cursor,
		DelayMs:    params.DelayMs,
		Format:     params.Format,
		Quality:    params.Quality,
		OutputPath: params.OutputPath,
		MaxWidth:   params.MaxWidth,
	}
	if params.Region != nil {
		opts.Region = &image.ScreenshotRegion{
			X:      params.Region.X,
			Y:      params.Region.Y,
			Width:  params.Region.Width,
			Height: params.Region.Height,
		}
	}

	result, err := image.CaptureScreen(opts)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("screenshot failed: %v", err)}, nil
	}

	contentParts := []string{
		fmt.Sprintf("Screenshot captured: %dx%d %s",
			result.Image.Width, result.Image.Height, strings.ToUpper(result.Image.MIME)),
	}
	if params.Window != "" {
		contentParts = append(contentParts, fmt.Sprintf("window: %q", params.Window))
	}
	if params.Display > 0 {
		contentParts = append(contentParts, fmt.Sprintf("display: %d", params.Display))
	}
	if result.SavedPath != "" {
		contentParts = append(contentParts, fmt.Sprintf("saved: %s", result.SavedPath))
	}

	img := result.Image
	b64 := image.EncodeBase64(img)
	resultImages := []ResultImage{{
		MIME:       img.MIME,
		Base64:     b64,
		Width:      img.Width,
		Height:     img.Height,
		SourcePath: result.SavedPath,
	}}

	return Result{
		Content: strings.Join(contentParts, ", "),
		Images:  resultImages,
	}, nil
}

func (t ScreenshotTool) executeListDisplays() (Result, error) {
	displays, err := image.ListDisplays()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("listing displays failed: %v", err)}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d display(s):\n\n", len(displays)))
	for _, d := range displays {
		primary := ""
		if d.IsPrimary {
			primary = " (primary)"
		}
		sb.WriteString(fmt.Sprintf("  Display %d%s: %dx%d at (%d,%d)", d.Index, primary, d.Width, d.Height, d.X, d.Y))
		if d.Name != "" {
			sb.WriteString(fmt.Sprintf(" — %s", d.Name))
		}
		sb.WriteString("\n")
	}

	return Result{Content: sb.String()}, nil
}

func (t ScreenshotTool) executeListWindows() (Result, error) {
	windows, err := image.ListWindows()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("listing windows failed: %v", err)}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d capturable window(s):\n\n", len(windows)))
	for _, w := range windows {
		sb.WriteString(fmt.Sprintf("  [%d] %s — %s", w.ID, w.App, w.Title))
		if w.Width > 0 {
			sb.WriteString(fmt.Sprintf(" (%dx%d at %d,%d)", w.Width, w.Height, w.X, w.Y))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\nUse the window title or app name with the \"window\" parameter to capture it.")

	return Result{Content: sb.String()}, nil
}
