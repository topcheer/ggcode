package agent

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

type requiredSchemaCache struct {
	mu     sync.RWMutex
	byTool map[string]*requiredSchemaInfo
}

type requiredSchemaInfo struct {
	required []requiredField
}

type requiredField struct {
	Name string
	Desc string
}

var globalRequiredSchemaCache = &requiredSchemaCache{byTool: map[string]*requiredSchemaInfo{}}

func parseRequiredSchema(params json.RawMessage) *requiredSchemaInfo {
	if len(params) == 0 {
		return nil
	}
	var sch struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(params, &sch); err != nil {
		return nil
	}
	if len(sch.Required) == 0 {
		return nil
	}
	info := &requiredSchemaInfo{}
	for _, name := range sch.Required {
		f := requiredField{Name: name}
		if p, ok := sch.Properties[name]; ok {
			f.Desc = p.Description
		}
		info.required = append(info.required, f)
	}
	return info
}

func (c *requiredSchemaCache) get(t tool.Tool) *requiredSchemaInfo {
	name := t.Name()
	c.mu.RLock()
	info, ok := c.byTool[name]
	c.mu.RUnlock()
	if ok {
		return info
	}
	info = parseRequiredSchema(t.Parameters())
	c.mu.Lock()
	c.byTool[name] = info
	c.mu.Unlock()
	return info
}

func missingRequiredFields(info *requiredSchemaInfo, args json.RawMessage) []requiredField {
	if info == nil || len(info.required) == 0 {
		return nil
	}
	var call map[string]json.RawMessage
	if len(args) > 0 {
		if err := json.Unmarshal(args, &call); err != nil {

			return nil
		}
	}
	var missing []requiredField
	for _, f := range info.required {
		raw, present := call[f.Name]
		if present && !isNullJSONValue(raw) {
			continue
		}
		missing = append(missing, f)
	}
	return missing
}

func isNullJSONValue(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "" || trimmed == "null"
}

func formatMissingRequired(toolName string, missing []requiredField) string {
	var sb strings.Builder
	if len(missing) == 1 {
		sb.WriteString("missing required parameter: ")
	} else {
		sb.WriteString("missing required parameters: ")
	}
	for i, f := range missing {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(f.Name)
	}
	sb.WriteString("\n")
	for _, f := range missing {
		sb.WriteString("\n" + f.Name + ": ")
		if f.Desc != "" {
			sb.WriteString(f.Desc)
		} else {
			sb.WriteString("(required, see tool schema)")
		}
	}
	sb.WriteString("\n\nresend the call with these parameters included.")
	return sb.String()
}

func preflightRequiredCheck(t tool.Tool, args json.RawMessage) *tool.Result {
	info := globalRequiredSchemaCache.get(t)
	missing := missingRequiredFields(info, args)
	if len(missing) == 0 {
		return nil
	}
	debug.Log("agent", "preflight required-check rejected %s: missing %d param(s)", t.Name(), len(missing))
	r := tool.Result{IsError: true, Content: formatMissingRequired(t.Name(), missing)}
	return &r
}
