package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// OSV malware gate (#4858, pattern ported from the Hermes agent's
// tools/osv_check.py - port the pattern, not the code).
//
// Every npx/uvx-style MCP server launch is preceded by an OSV
// (api.osv.dev) query for MAL-* malware advisories. A hit blocks the
// launch with the advisory IDs; a network or API error FAILS OPEN (the
// server still starts - offline must not brick the MCP stack) with a
// debug log; verdicts are cached for 24h so restarts do not re-query.
// mcp_servers.<name>.skip_osv_check: true is the escape hatch for
// air-gapped or enterprise-mirror users.

const osvCacheTTL = 24 * time.Hour

// osvQueryEndpoint is a var so tests can point it at an httptest server.
var osvQueryEndpoint = "https://api.osv.dev/v1/query"

type osvVerdict struct {
	ids       []string // MAL-* advisory IDs (empty = clean or unknown)
	queriedAt time.Time
}

var (
	osvCacheMu sync.Mutex
	osvCache   = map[string]osvVerdict{}
)

// osvPackageFromCommand infers (ecosystem, package, version) from a
// package-runner command line. Only runner executables are considered;
// anything else returns empty strings and the gate is a no-op. The first
// non-flag argument is the package reference; a trailing @version is
// split off while a leading @scope is preserved.
func osvPackageFromCommand(command string, args []string) (ecosystem, pkg, version string) {
	switch baseCommandName(command) {
	case "npx", "bunx", "pnpm":
		ecosystem = "npm"
	case "uvx", "uv":
		ecosystem = "PyPI"
	default:
		return "", "", ""
	}
	for _, arg := range args {
		trimmed := strings.TrimSpace(arg)
		if trimmed == "" || strings.HasPrefix(trimmed, "-") {
			continue
		}
		pkg, version = splitPackageVersion(trimmed)
		return ecosystem, pkg, version
	}
	return "", "", ""
}

func baseCommandName(command string) string {
	base := strings.TrimSpace(command)
	// Windows shims arrive as full paths; filepath.Base needs the separator
	// normalized for both directions.
	if idx := strings.LastIndexAny(base, `/\`); idx >= 0 {
		base = base[idx+1:]
	}
	return strings.TrimSuffix(base, ".exe")
}

// splitPackageVersion splits "@scope/name@1.2.3" into ("@scope/name",
// "1.2.3") and "name" into ("name", ""). A lone "@scope" (no slash) is not
// a package reference we can query - return it as-is with no version.
func splitPackageVersion(ref string) (pkg, version string) {
	if !strings.Contains(ref[1:], "@") {
		return ref, ""
	}
	idx := strings.LastIndex(ref, "@")
	return ref[:idx], ref[idx+1:]
}

// osvGate is the pre-exec launch gate. nil = allow; non-nil = block with a
// user-actionable message. Never blocks on infrastructure errors.
func osvGate(ctx context.Context, serverName, command string, args []string, skip bool) error {
	if skip {
		return nil
	}
	ecosystem, pkg, version := osvPackageFromCommand(command, args)
	if pkg == "" {
		return nil
	}
	ids := osvQueryMAL(ctx, ecosystem, pkg, version)
	if len(ids) == 0 {
		return nil
	}
	return fmt.Errorf("mcp[%s]: refusing to launch %s %s: package is flagged by OSV malware advisories (%s). If you are certain this is a false positive (e.g. an internal mirror), set mcp_servers.%s.skip_osv_check: true to bypass",
		serverName, ecosystem, pkg, strings.Join(ids, ", "), serverName)
}

// osvQueryMAL returns the MAL-* advisory IDs for the package, or nil on
// clean/unknown/query failure (fail-open: the caller treats nil as allow).
func osvQueryMAL(ctx context.Context, ecosystem, pkg, version string) []string {
	cacheKey := ecosystem + "|" + pkg + "@" + version
	osvCacheMu.Lock()
	if entry, ok := osvCache[cacheKey]; ok && time.Since(entry.queriedAt) < osvCacheTTL {
		osvCacheMu.Unlock()
		return entry.ids
	}
	osvCacheMu.Unlock()

	ids, err := osvFetchMAL(ctx, ecosystem, pkg, version)
	if err != nil {
		// Fail open: offline or a degraded OSV must never brick MCP startup.
		debug.Log("mcp-osv", "OSV query for %s failed (allowing launch, fail-open): %v", cacheKey, err)
		return nil
	}
	osvCacheMu.Lock()
	osvCache[cacheKey] = osvVerdict{ids: ids, queriedAt: time.Now()}
	osvCacheMu.Unlock()
	return ids
}

func osvFetchMAL(ctx context.Context, ecosystem, pkg, version string) ([]string, error) {
	query := map[string]any{
		"package": map[string]string{"name": pkg, "ecosystem": ecosystem},
	}
	if version != "" {
		query["version"] = version
	}
	body, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, osvQueryEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("osv: HTTP %d", resp.StatusCode)
	}
	var result struct {
		Vulns []struct {
			ID string `json:"id"`
		} `json:"vulns"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	var mal []string
	for _, v := range result.Vulns {
		if strings.HasPrefix(v.ID, "MAL-") {
			mal = append(mal, v.ID)
		}
	}
	return mal, nil
}
