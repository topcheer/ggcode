package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type CurrentTimeTool struct{}

func NewCurrentTimeTool() *CurrentTimeTool {
	return &CurrentTimeTool{}
}

func (t *CurrentTimeTool) Name() string { return "current_time" }

func (t *CurrentTimeTool) Description() string {
	return "Get the current date and time. Use this whenever the actual wall-clock time matters (e.g. 'today', 'this week', deadline math, log timestamp interpretation), or when the session has been running long enough that the time noted in the system prompt may be stale. Accepts an optional IANA timezone name (default: system local timezone)."
}

func (t *CurrentTimeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "timezone": {
      "type": "string",
      "description": "Optional IANA timezone name (e.g. 'Asia/Shanghai', 'America/New_York'). Defaults to the system local timezone."
    }
  },
  "required": []
}`)
}

func (t *CurrentTimeTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	now := time.Now()
	loc := now.Location()
	tzName := strings.TrimSpace(string(input))

	requested := ""
	if tzName != "" && tzName != "null" && tzName != "{}" {
		requested = strings.Trim(tzName, `"`)
		if strings.HasPrefix(tzName, "{") {
			var req struct {
				Timezone string `json:"timezone"`
			}
			if err := json.Unmarshal(input, &req); err == nil && strings.TrimSpace(req.Timezone) != "" {
				requested = strings.TrimSpace(req.Timezone)
			} else {
				requested = ""
			}
		}
	}

	if requested != "" {
		parsed, err := time.LoadLocation(requested)
		if err != nil {
			return Result{
				Content: fmt.Sprintf("Unknown timezone %q: %v. Use an IANA timezone name such as 'Asia/Shanghai' or 'America/New_York'.", requested, err),
				IsError: true,
			}, nil
		}
		loc = parsed
	}
	local := now.In(loc)
	name, _ := local.Zone()
	utcOffset := local.Format("-07:00")

	var b strings.Builder
	fmt.Fprintf(&b, "Current time: %s (%s), %s %s (UTC offset %s)\n",
		local.Format("2006-01-02"), local.Format("Monday"),
		local.Format("15:04:05"), name, utcOffset)
	fmt.Fprintf(&b, "ISO 8601: %s\n", local.Format(time.RFC3339))
	fmt.Fprintf(&b, "Unix epoch: %d\n", local.Unix())
	if name == "Local" || name == "" {
		fmt.Fprintf(&b, "System local timezone could not be resolved to a name; offset %s applies.\n", utcOffset)
	}

	return Result{Content: strings.TrimRight(b.String(), "\n")}, nil
}
