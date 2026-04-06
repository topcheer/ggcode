package mcp

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/config"
)

var nonNamePattern = regexp.MustCompile(`[^a-z0-9]+`)

type installOptions struct {
	transport string
	env       map[string]string
	headers   map[string]string
}

func ParseInstallArgs(args []string) (config.MCPServerConfig, error) {
	if idx := installSeparatorIndex(args); idx >= 0 {
		prefix, opts, err := parseInstallOptions(args[:idx])
		if err != nil {
			return config.MCPServerConfig{}, err
		}
		return parseDelimitedInstallArgs(prefix, args[idx+1:], opts)
	}
	positionals, opts, err := parseInstallOptions(args)
	if err != nil {
		return config.MCPServerConfig{}, err
	}
	server, err := parseStandardInstallArgs(positionals, opts.transport)
	if err != nil {
		return config.MCPServerConfig{}, err
	}
	return applyInstallOptions(server, opts)
}

func parseStandardInstallArgs(args []string, transportOverride string) (config.MCPServerConfig, error) {
	if transportOverride != "" {
		return parseOptionTransportInstallArgs(args, transportOverride)
	}
	return parseLegacyInstallArgs(args)
}

func parseLegacyInstallArgs(args []string) (config.MCPServerConfig, error) {
	if len(args) < 2 {
		return config.MCPServerConfig{}, fmt.Errorf("usage: [name] <stdio|http|ws> <command...|url>")
	}

	transport := normalizeInstallTransport(args[0])
	start := 1
	name := ""
	if transport == "" {
		if len(args) >= 2 {
			if trailingTransport := normalizeInstallTransport(args[len(args)-1]); trailingTransport == "stdio" {
				return config.MCPServerConfig{
					Name:    normalizeServerName(inferCommandServerName(args[0], args[1:len(args)-1])),
					Type:    "stdio",
					Command: args[0],
					Args:    append([]string(nil), args[1:len(args)-1]...),
				}, nil
			}
		}
		if len(args) < 3 {
			return config.MCPServerConfig{}, fmt.Errorf("usage: [name] <stdio|http|ws> <command...|url>")
		}
		name = strings.TrimSpace(args[0])
		transport = normalizeInstallTransport(args[1])
		start = 2
	}
	if transport == "" {
		return config.MCPServerConfig{}, fmt.Errorf("expected transport to be one of stdio, http, ws")
	}

	rest := append([]string(nil), args[start:]...)
	if len(rest) == 0 {
		return config.MCPServerConfig{}, fmt.Errorf("missing install target for transport %s", transport)
	}

	server := config.MCPServerConfig{Type: transport}
	switch transport {
	case "stdio":
		server.Command = rest[0]
		if len(rest) > 1 {
			server.Args = rest[1:]
		}
		if name == "" {
			name = inferCommandServerName(server.Command, server.Args)
		}
	case "http", "ws":
		if len(rest) != 1 {
			return config.MCPServerConfig{}, fmt.Errorf("%s install expects a single URL", transport)
		}
		server.URL = strings.TrimSpace(rest[0])
		if name == "" {
			name = inferURLServerName(server.URL)
		}
	}

	name = normalizeServerName(name)
	if name == "" {
		return config.MCPServerConfig{}, fmt.Errorf("could not determine MCP server name")
	}
	server.Name = name
	return server, nil
}

func parseOptionTransportInstallArgs(args []string, transport string) (config.MCPServerConfig, error) {
	if len(args) == 0 {
		return config.MCPServerConfig{}, fmt.Errorf("missing install target for transport %s", transport)
	}
	name := ""
	target := args
	if len(args) > 1 {
		name = strings.TrimSpace(args[0])
		target = args[1:]
	}
	if len(target) == 0 {
		return config.MCPServerConfig{}, fmt.Errorf("missing install target for transport %s", transport)
	}

	server := config.MCPServerConfig{Type: transport}
	switch transport {
	case "stdio":
		server.Command = target[0]
		if len(target) > 1 {
			server.Args = append([]string(nil), target[1:]...)
		}
		if name == "" {
			name = inferCommandServerName(server.Command, server.Args)
		}
	case "http", "ws":
		if len(target) != 1 {
			return config.MCPServerConfig{}, fmt.Errorf("%s install expects a single URL", transport)
		}
		server.URL = strings.TrimSpace(target[0])
		if name == "" {
			name = inferURLServerName(server.URL)
		}
	}

	name = normalizeServerName(name)
	if name == "" {
		return config.MCPServerConfig{}, fmt.Errorf("could not determine MCP server name")
	}
	server.Name = name
	return server, nil
}

func parseDelimitedInstallArgs(prefix, target []string, opts installOptions) (config.MCPServerConfig, error) {
	if len(target) == 0 {
		return config.MCPServerConfig{}, fmt.Errorf("missing install target after --")
	}

	name := ""
	transport := opts.transport
	switch len(prefix) {
	case 0:
	case 1:
		if transport == "" {
			if normalized := normalizeInstallTransport(prefix[0]); normalized != "" {
				transport = normalized
			} else {
				name = strings.TrimSpace(prefix[0])
			}
		} else {
			name = strings.TrimSpace(prefix[0])
		}
	default:
		return config.MCPServerConfig{}, fmt.Errorf("usage: [name] [-t <stdio|http|ws>] [--env KEY=VALUE ...] [--header KEY:VALUE ...] -- <command...|url>")
	}

	if transport == "" {
		transport = "stdio"
	}

	server := config.MCPServerConfig{
		Type: transport,
	}
	switch transport {
	case "stdio":
		server.Command = target[0]
		if len(target) > 1 {
			server.Args = append([]string(nil), target[1:]...)
		}
		if name == "" {
			name = inferCommandServerName(server.Command, server.Args)
		}
	case "http", "ws":
		if len(target) != 1 {
			return config.MCPServerConfig{}, fmt.Errorf("%s install expects a single URL", transport)
		}
		server.URL = strings.TrimSpace(target[0])
		if name == "" {
			name = inferURLServerName(server.URL)
		}
	}

	name = normalizeServerName(name)
	if name == "" {
		return config.MCPServerConfig{}, fmt.Errorf("could not determine MCP server name")
	}
	server.Name = name
	return applyInstallOptions(server, opts)
}

func parseInstallOptions(args []string) ([]string, installOptions, error) {
	var positionals []string
	var opts installOptions
	for i := 0; i < len(args); i++ {
		token := strings.TrimSpace(args[i])
		switch token {
		case "--env":
			if i+1 >= len(args) {
				return nil, installOptions{}, fmt.Errorf("missing KEY=VALUE after --env")
			}
			key, value, err := parseInstallMapValue(args[i+1], "--env")
			if err != nil {
				return nil, installOptions{}, err
			}
			if opts.env == nil {
				opts.env = map[string]string{}
			}
			opts.env[key] = value
			i++
			continue
		case "--header":
			if i+1 >= len(args) {
				return nil, installOptions{}, fmt.Errorf("missing KEY: VALUE after --header")
			}
			key, value, err := parseInstallMapValue(args[i+1], "--header")
			if err != nil {
				return nil, installOptions{}, err
			}
			if opts.headers == nil {
				opts.headers = map[string]string{}
			}
			opts.headers[key] = value
			i++
			continue
		case "-t", "--transport":
			if i+1 >= len(args) {
				return nil, installOptions{}, fmt.Errorf("missing transport after %s", token)
			}
			transport := normalizeInstallTransport(args[i+1])
			if transport == "" {
				return nil, installOptions{}, fmt.Errorf("expected transport to be one of stdio, http, ws")
			}
			opts.transport = transport
			i++
			continue
		}
		if token != "" {
			positionals = append(positionals, args[i])
		}
	}
	return positionals, opts, nil
}

func parseInstallMapValue(raw, flag string) (string, string, error) {
	trimmed := strings.TrimSpace(raw)
	if key, value, ok := strings.Cut(trimmed, "="); ok && strings.TrimSpace(key) != "" {
		return strings.TrimSpace(key), value, nil
	}
	if key, value, ok := strings.Cut(trimmed, ":"); ok && strings.TrimSpace(key) != "" {
		return strings.TrimSpace(key), strings.TrimSpace(value), nil
	}
	if flag == "--header" {
		return "", "", fmt.Errorf("expected KEY: VALUE after --header")
	}
	return "", "", fmt.Errorf("expected KEY=VALUE after --env")
}

func applyInstallOptions(server config.MCPServerConfig, opts installOptions) (config.MCPServerConfig, error) {
	if len(opts.env) > 0 {
		server.Env = cloneStringMap(opts.env)
	}
	if len(opts.headers) > 0 {
		if server.Type != "http" && server.Type != "ws" {
			return config.MCPServerConfig{}, fmt.Errorf("--header is only supported for http and ws MCP servers")
		}
		server.Headers = cloneStringMap(opts.headers)
	}
	return server, nil
}

func installSeparatorIndex(args []string) int {
	for i, arg := range args {
		if strings.TrimSpace(arg) == "--" {
			return i
		}
	}
	return -1
}

func normalizeInstallTransport(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "stdio":
		return "stdio"
	case "http":
		return "http"
	case "ws", "websocket":
		return "ws"
	default:
		return ""
	}
}

func inferCommandServerName(command string, args []string) string {
	base := filepath.Base(strings.TrimSpace(command))
	if base == "" {
		return ""
	}
	switch base {
	case "npx", "pnpm", "bunx", "uvx", "uv":
		for _, arg := range args {
			trimmed := strings.TrimSpace(arg)
			if trimmed == "" || strings.HasPrefix(trimmed, "-") {
				continue
			}
			return cleanupPackageServerName(trimmed)
		}
	case "yarn":
		for i := 0; i < len(args); i++ {
			trimmed := strings.TrimSpace(args[i])
			if trimmed == "" || strings.HasPrefix(trimmed, "-") {
				continue
			}
			if trimmed == "dlx" || trimmed == "exec" {
				continue
			}
			return cleanupPackageServerName(trimmed)
		}
	}
	return base
}

func cleanupPackageServerName(name string) string {
	trimmed := strings.TrimSpace(name)
	trimmed = strings.TrimPrefix(trimmed, "@")
	if idx := strings.Index(trimmed, "@"); idx > 0 {
		trimmed = trimmed[:idx]
	}
	trimmed = strings.TrimPrefix(trimmed, "modelcontextprotocol/")
	trimmed = strings.TrimSuffix(trimmed, "-server")
	return trimmed
}

func inferURLServerName(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	host := parsed.Hostname()
	if host == "" {
		return ""
	}
	parts := strings.Split(host, ".")
	if len(parts) > 0 {
		return parts[0]
	}
	return host
}

func normalizeServerName(name string) string {
	normalized := strings.ToLower(strings.TrimSpace(name))
	normalized = nonNamePattern.ReplaceAllString(normalized, "-")
	return strings.Trim(normalized, "-")
}
