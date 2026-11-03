package mcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

type ResourceTemplate struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

type ListResourceTemplatesResult struct {
	Templates  []ResourceTemplate `json:"resourceTemplates"`
	NextCursor string             `json:"nextCursor,omitempty"`
	CacheableResult
}

func (c *Client) ListResourceTemplates(ctx context.Context) ([]ResourceTemplate, error) {
	if c.serverCaps.Resources == nil {
		return nil, nil
	}

	if v, ok := c.listingCache.get(cacheResourceTemplates, ""); ok {
		return cloneCachedSlice(v.([]ResourceTemplate)), nil
	}
	fetchEpoch := c.listingCache.epoch(cacheResourceTemplates)
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
			c.storeListingsCache(cacheResourceTemplates, "", all, pages, fetchEpoch)
			return all, nil
		}
		cursor = result.NextCursor
	}
	debug.Log("mcp-client", "server=%s resources/templates/list exceeded %d pages; stopping pagination", c.name, maxPaginationPages)
	return all, nil
}

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
		if reserved {

			b.WriteString(val)
		} else {

			b.WriteString(strings.ReplaceAll(url.QueryEscape(val), "+", "%20"))
		}
	}
	return b.String(), nil
}
