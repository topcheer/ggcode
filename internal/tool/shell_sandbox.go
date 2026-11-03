package tool

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

type SandboxPolicy struct {
	Enabled         bool
	AllowNetwork    bool
	ExtraWritePaths []string
}

func NewSandboxPolicy(enabled bool, allowNetworkPtr *bool, extra []string) *SandboxPolicy {
	allowNetwork := true
	if allowNetworkPtr != nil {
		allowNetwork = *allowNetworkPtr
	}
	return &SandboxPolicy{Enabled: enabled, AllowNetwork: allowNetwork, ExtraWritePaths: extra}
}

var errSandboxUnavailable = errors.New("sandbox.enabled is set but this platform cannot enforce an OS-level sandbox for shell commands")

func wrapShellCommandOS(cmd *exec.Cmd, workspace string, p *SandboxPolicy) (bool, error) {
	if p == nil || !p.Enabled {
		return false, nil
	}
	wrapped, err := sandboxWrap(cmd, workspace, p)
	if err != nil {
		debug.Log("sandbox", "OS sandbox wrap failed: %v", err)
		return false, err
	}
	if wrapped {
		debug.Log("sandbox", "shell command wrapped in OS sandbox (workspace=%s network_allowed=%v)", workspace, p.AllowNetwork)
	}
	return wrapped, nil
}

const sandboxEPERMHint = "\n[hint] This failure looks like an OS sandbox write denial. The sandbox allows writes inside the workspace, temp directories and sandbox.extra_write_paths only; extend that config list (e.g. ~/go/pkg/mod, ~/.cache) or set sandbox.enabled=false if the write is legitimate."

var sandboxDeniedExtraMarkers []string

func sandboxDeniedOutput(output string) bool {
	lower := strings.ToLower(output)
	for _, marker := range []string{
		"operation not permitted",
		"eperm",
		"read-only file system",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	for _, marker := range sandboxDeniedExtraMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

const sandboxLaunchMarker = "__ggcode_sandbox_launch"

const SandboxLaunchMarker = sandboxLaunchMarker

var SandboxLaunchEntry func(payloadJSON string, target []string)

type sandboxLaunchPayload struct {
	WritePaths   []string `json:"write_paths"`
	AllowNetwork bool     `json:"allow_network"`
}

const sandboxLaunchPayloadMaxBytes = 32 * 1024

func encodeSandboxLaunchPayload(p sandboxLaunchPayload) (string, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("sandbox launch payload: %w", err)
	}
	if len(body) > sandboxLaunchPayloadMaxBytes {
		return "", fmt.Errorf("%w: sandbox launch payload is %d bytes, over the %d cap", errSandboxUnavailable, len(body), sandboxLaunchPayloadMaxBytes)
	}
	return string(body), nil
}

func decodeSandboxLaunchPayload(s string) (sandboxLaunchPayload, error) {
	var p sandboxLaunchPayload
	if len(s) == 0 || len(s) > sandboxLaunchPayloadMaxBytes {
		return p, fmt.Errorf("sandbox launch payload: invalid length %d", len(s))
	}
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return p, fmt.Errorf("sandbox launch payload: %w", err)
	}
	return p, nil
}

func sandboxWriteAllowPaths(workspace string, p *SandboxPolicy) []string {
	var out []string
	add := func(path string) {
		if path == "" {
			return
		}
		resolved, cleaned := resolveSandboxPath(path)
		for _, form := range []string{resolved, cleaned} {
			if form == "" {
				continue
			}
			dup := false
			for _, existing := range out {
				if existing == form {
					dup = true
					break
				}
			}
			if !dup {
				out = append(out, form)
			}
		}
	}
	add("/dev/null")
	add(os.TempDir())
	add("/tmp")
	add(workspace)
	if p != nil {
		for _, extra := range p.ExtraWritePaths {
			add(extra)
		}
	}
	return out
}

const (
	landlockAccessWriteFile  = 0x0002
	landlockAccessRemoveFile = 0x0020
	landlockAccessRemoveDir  = 0x0010
	landlockAccessMakeChar   = 0x0040
	landlockAccessMakeDir    = 0x0080
	landlockAccessMakeReg    = 0x0100
	landlockAccessMakeSock   = 0x0200
	landlockAccessMakeFifo   = 0x0400
	landlockAccessMakeBlock  = 0x0800
	landlockAccessMakeSym    = 0x1000
	landlockAccessRefer      = 0x2000
	landlockAccessTruncate   = 0x4000
)

func landlockWriteBits(abi int) (uint64, error) {
	if abi < 1 {
		return 0, fmt.Errorf("Landlock ABI %d unsupported (kernel < 5.13 or landlock LSM disabled)", abi)
	}
	bits := uint64(landlockAccessWriteFile | landlockAccessRemoveFile |
		landlockAccessRemoveDir | landlockAccessMakeChar | landlockAccessMakeDir |
		landlockAccessMakeReg | landlockAccessMakeSock | landlockAccessMakeFifo |
		landlockAccessMakeBlock | landlockAccessMakeSym)
	if abi >= 2 {
		bits |= landlockAccessRefer
	}
	if abi >= 3 {
		bits |= landlockAccessTruncate
	}
	return bits, nil
}

type sbSockFilter struct {
	Code uint32
	Jt   uint32
	Jf   uint32
	K    uint32
}

const (
	sbBpfLdWAbs = 0x20
	sbBpfJeqK   = 0x15
	sbBpfRetK   = 0x06

	sbSeccompRetAllow  = 0x7fff0000
	sbSeccompErrnoBase = 0x00050000
)

const (
	sbSeccompDataNr    = 0
	sbSeccompDataArch  = 4
	sbSeccompDataArgs0 = 16

	sbAuditArchX8664   = 0xc000003e
	sbAuditArchAarch64 = 0xc00000b7

	sbSysSocket           = 41
	sbSysSocketArm        = 198
	sbSysSocketcall       = 102
	sbSocketcallSysSocket = 1

	sbAfInet   = 2
	sbAfInet6  = 10
	sbAfPacket = 17

	sbENosys = 38
	sbEperm  = 1
)

func sbSeccompNetworkDenyFilter(goarch string) ([]sbSockFilter, error) {
	errnoRet := func(errno uint32) uint32 { return sbSeccompErrnoBase | errno }
	retAllow := sbSockFilter{Code: sbBpfRetK, K: sbSeccompRetAllow}
	retEperm := sbSockFilter{Code: sbBpfRetK, K: errnoRet(sbEperm)}
	retEnosys := sbSockFilter{Code: sbBpfRetK, K: errnoRet(sbENosys)}
	ldArch := sbSockFilter{Code: sbBpfLdWAbs, K: sbSeccompDataArch}
	ldNr := sbSockFilter{Code: sbBpfLdWAbs, K: sbSeccompDataNr}
	ldArgs0 := sbSockFilter{Code: sbBpfLdWAbs, K: sbSeccompDataArgs0}

	switch goarch {
	case "amd64":

		return []sbSockFilter{
			ldArch,
			{Code: sbBpfJeqK, Jt: 0, Jf: 10, K: sbAuditArchX8664},
			ldNr,
			{Code: sbBpfJeqK, Jt: 2, Jf: 0, K: sbSysSocket},
			ldArgs0,
			{Code: sbBpfJeqK, Jt: 5, Jf: 4, K: sbSocketcallSysSocket},
			ldArgs0,
			{Code: sbBpfJeqK, Jt: 3, Jf: 0, K: sbAfInet},
			{Code: sbBpfJeqK, Jt: 2, Jf: 0, K: sbAfInet6},
			{Code: sbBpfJeqK, Jt: 1, Jf: 0, K: sbAfPacket},
			retAllow,
			retEperm,
			retEnosys,
		}, nil
	case "arm64":

		return []sbSockFilter{
			ldArch,
			{Code: sbBpfJeqK, Jt: 0, Jf: 8, K: sbAuditArchAarch64},
			ldNr,
			{Code: sbBpfJeqK, Jt: 0, Jf: 4, K: sbSysSocketArm},
			ldArgs0,
			{Code: sbBpfJeqK, Jt: 3, Jf: 0, K: sbAfInet},
			{Code: sbBpfJeqK, Jt: 2, Jf: 0, K: sbAfInet6},
			{Code: sbBpfJeqK, Jt: 1, Jf: 0, K: sbAfPacket},
			retAllow,
			retEperm,
			retEnosys,
		}, nil
	default:
		return nil, fmt.Errorf("seccomp network deny: unsupported arch %q (amd64/arm64 only)", goarch)
	}
}

func seatbeltEscaped(path string) bool {
	return !strings.ContainsAny(path, "\"\\")
}

func resolveSandboxPath(path string) (resolved, cleaned string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", ""
	}
	cleaned = filepath.Clean(abs)
	original := cleaned
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		return filepath.Clean(resolved), cleaned
	}

	dir := filepath.Dir(original)
	for dir != original {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			root := filepath.Clean(real)
			if rel, rerr := filepath.Rel(root, original); rerr == nil && rel != ".." && !strings.HasPrefix(rel, "..") {
				return filepath.Join(root, rel), original
			}
			return filepath.Join(root, filepath.Base(original)), original
		}
		dir = filepath.Dir(dir)
	}
	return original, original
}
