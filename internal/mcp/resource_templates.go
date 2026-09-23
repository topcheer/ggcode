package mcp

// MCP 2025-06-18 resource templates (RFC 6570 URI Templates).
//
// A server exposes parameterized resources via resources/templates/list:
// `db://tables/{name}` style URI templates that a client expands into
// concrete URIs for resources/read. Template-based resources never appear
// in resources/list, so a client that ignores templates misses entire
// resource families (common with database / REST-bridge servers).
//
// The listing mirrors ListResources: cursor pagination (#562), SEP-2549
// freshness caching and graceful degradation — the method is older-server
// optional, so callers treat -32601 (method not found) as "no templates".

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// ResourceTemplate is one resources/templates/list entry.
type ResourceTemplate struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

// ListResourceTemplatesResult is a resources/templates/list page.
type ListResourceTemplatesResult struct {
	Templates  []ResourceTemplate `json:"resourceTemplates"`
	NextCursor string             `json:"nextCursor,omitempty"`
	CacheableResult
}

// ListResourceTemplates fetches the server's resource templates with cursor
// pagination and SEP-2549 freshness caching. Gated on the resources
// capability so a resource-less server receives no unsupported call.
func (c *Client) ListResourceTemplates(ctx context.Context) ([]ResourceTemplate, error) {
	if c.serverCaps.Resources == nil {
		return nil, nil
	}
	// SEP-2549 freshness cache (see ListResources).
	if v, ok := c.listingCache.get(cacheResourceTemplates, ""); ok {
		return cloneCachedSlice(v.([]ResourceTemplate)), nil
	}
	var all []ResourceTemplate
	var pages []CacheableResult
	cursor := ""
	for page := 0; page < maxPaginationPages; page++ {
		params := struct {
			Cursor string `json:"cursor,omitempty"`
		}{Cursor: cursor}
		var result ListResourceTemplatesResult
		if err := c.sendRequest(ctx, "resources/templates/list", params, &result); err != nil {
			if len(all) > 0 {
				debug.Log("mcp-client", "server=%s resources/templates/list page %d failed after %d templates: %v", c.name, page+1, len(all), err)
				return all, nil
			}
			return nil, fmt.Errorf("mcp[%s]: resources/templates/list: %w", c.name, err)
		}
		all = append(all, result.Templates...)
		pages = append(pages, result.CacheableResult)
		if result.NextCursor == "" {
			c.storeListingsCache(cacheResourceTemplates, "", all, pages)
			return all, nil
		}
		cursor = result.NextCursor
	}
	debug.Log("mcp-client", "server=%s resources/templates/list exceeded %d pages; stopping pagination", c.name, maxPaginationPages)
	return all, nil
}

// ExpandURITemplate expands an RFC 6570 URI Template against vars. Supported:
// simple {var} and reserved {+var} expansion with comma-separated
// multi-values (level 1-2, the form MCP servers use in practice). Unknown or
// empty variables are an error so a stale template expansion fails loudly
// instead of producing a half-substituted URI.
func ExpandURITemplate(tmpl string, vars map[string]string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(tmpl); {
		if tmpl[i] != '{' {
			b.WriteByte(tmpl[i])
			i++
			continue
		}
		end := strings.IndexByte(tmpl[i:], '}')
		if end < 0 {
			return "", fmt.Errorf("uri template %q: unclosed '{'", tmpl)
		}
		expr := tmpl[i+1 : i+end]
		i += end + 1
		if expr == "" {
			return "", fmt.Errorf("uri template %q: empty expression", tmpl)
		}
		reserved := false
		switch expr[0] {
		case '+':
			reserved = true
			expr = expr[1:]
		case '#', '.', '/', ';', '?', '&':
			return "", fmt.Errorf("uri template %q: operator %q not supported", tmpl, string(expr[0]))
		}
		if strings.ContainsAny(expr, ":*") {
			return "", fmt.Errorf("uri template %q: prefix/composite modifier on %q not supported", tmpl, expr)
		}
		val, ok := vars[expr]
		if !ok || strings.TrimSpace(val) == "" {
			return "", fmt.Errorf("uri template %q: missing value for variable %q", tmpl, expr)
		}
		for j, part := range strings.Split(val, ",") {
			if j > 0 {
				b.WriteByte(',')
			}
			if reserved {
				b.WriteString(part)
			} else {
				// pct-encode reserved characters; QueryEscape turns spaces
				// into '+' which is form-encoding, not URI — fix that up.
				b.WriteString(strings.ReplaceAll(url.QueryEscape(part), "+", "%20"))
			}
		}
	}
	return b.String(), nil
}
