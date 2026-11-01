package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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

	// osvFailOpenWarnOnce makes the fail-open warning user-visible EXACTLY
	// once per process (#4860 review: debug.Log is invisible to users; the
	// issue requires a one-time visible warning so an offline OSV check is
	// never a silent skip).
	osvFailOpenWarnOnce sync.Once
)

// osvPackageFromCommand infers (ecosystem, package, version) from a
// package-runner command line. Only runner executables are considered;
// anything else returns empty strings and the gate is a no-op.
// Subcommand-aware (#4860 review follow-up): "pnpm dlx <pkg>" and
// "uv tool run <pkg>" resolve the package AFTER the subcommand words -
// taking the first positional would query the subcommand itself ("dlx",
// "run") and miss the real package. "uv run"/"pnpm install" operate on
// the local project environment, not remote package installs - no gate.
func osvPackageFromCommand(command string, args []string) (ecosystem, pkg, version string) {
	var positional []string
	for _, arg := range args {
		trimmed := strings.TrimSpace(arg)
		if trimmed == "" || strings.HasPrefix(trimmed, "-") {
			continue
		}
		positional = append(positional, trimmed)
	}
	switch baseCommandName(command) {
	case "npx", "bunx":
		if len(positional) > 0 {
			pkg, version = splitPackageVersion(positional[0])
			return "npm", pkg, version
		}
	case "pnpm":
		for i, p := range positional {
			if p == "dlx" && i+1 < len(positional) {
				pkg, version = splitPackageVersion(positional[i+1])
				return "npm", pkg, version
			}
		}
	case "uvx":
		if len(positional) > 0 {
			pkg, version = splitPackageVersion(positional[0])
			return "PyPI", pkg, version
		}
	case "uv":
		for i, p := range positional {
			if p == "tool" && i+2 < len(positional) && positional[i+1] == "run" {
				pkg, version = splitPackageVersion(positional[i+2])
				return "PyPI", pkg, version
			}
		}
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
		// One process-wide VISIBLE warning (not just debug.Log) so the user
		// knows launches are unscreened this session; then stay quiet.
		osvFailOpenWarnOnce.Do(func() {
			fmt.Fprintf(os.Stderr, "Warning: OSV malware check unavailable (%v); MCP package launches run WITHOUT malware screening until connectivity returns. Set mcp_servers.<name>.skip_osv_check: true to silence this permanently.\n", err)
		})
		debug.Log("mcp-osv", "OSV query for %s failed (allowing launch, fail-open): %v", ecosystem+"|"+pkg+"@"+version, err)
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
