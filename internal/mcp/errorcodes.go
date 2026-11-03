package mcp

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

const ErrCodeInvalidParams = -32602

const (
	ErrCodeHeaderMismatch = -32020

	ErrCodeMissingRequiredClientCapability = -32021

	ErrCodeUnsupportedProtocolVersion = -32022
)

const (
	legacyErrCodeHeaderMismatch                  = -32001
	legacyErrCodeResourceNotFound                = -32002
	legacyErrCodeMissingRequiredClientCapability = -32003
	legacyErrCodeUnsupportedProtocolVersion      = -32004
)

func isMCPReservedErrorCode(code int) bool {
	return code >= -32099 && code <= -32020
}

func isUnsupportedProtocolVersionCode(code int) bool {
	return code == ErrCodeUnsupportedProtocolVersion ||
		code == legacyErrCodeUnsupportedProtocolVersion
}

func isResourceNotFoundCode(code int) bool {
	return code == legacyErrCodeResourceNotFound || code == ErrCodeInvalidParams
}

func specErrorCodeHint(code int) (name, hint string) {
	switch code {
	case ErrCodeHeaderMismatch, legacyErrCodeHeaderMismatch:
		return "HeaderMismatch",
			"the server rejected the required standard MCP request headers " +
				"(Mcp-Method / Mcp-Name); check MCP-aware proxies or routing middleware"
	case ErrCodeMissingRequiredClientCapability, legacyErrCodeMissingRequiredClientCapability:
		return "MissingRequiredClientCapability",
			"the request needs a client capability ggcode did not advertise; " +
				"enable the corresponding ggcode feature before retrying"
	case ErrCodeUnsupportedProtocolVersion, legacyErrCodeUnsupportedProtocolVersion:
		return "UnsupportedProtocolVersion",
			"the protocol version ggcode sent is not supported by this server"
	default:
		if isMCPReservedErrorCode(code) {
			return "ReservedErrorCode",
				"defined by the MCP specification (reserved range -32020..-32099)"
		}
		return "", ""
	}
}

type specAnnotatedError struct {
	err  *Error
	name string
	hint string
}

func (e *specAnnotatedError) Error() string {
	return fmt.Sprintf("%s [MCP spec: %s - %s]", e.err.Error(), e.name, e.hint)
}

func (e *specAnnotatedError) Unwrap() error { return e.err }

func decorateSpecError(err error) error {
	if err == nil {
		return nil
	}
	je, ok := jsonRPCErrorOf(err)
	if !ok {
		return err
	}
	var already *specAnnotatedError
	if errors.As(err, &already) {
		return err
	}
	name, hint := specErrorCodeHint(je.Code)
	if name == "" {
		return err
	}
	return &specAnnotatedError{err: je, name: name, hint: hint}
}

func jsonRPCErrorOf(err error) (*Error, bool) {
	var je *Error
	if errors.As(err, &je) {
		return je, true
	}
	return nil, false
}

func knownProtocolVersionList() []string {
	vs := make([]string, 0, len(knownMCPProtocolVersions))
	for v := range knownMCPProtocolVersions {
		vs = append(vs, v)
	}
	sort.Strings(vs)
	return vs
}

func unsupportedProtocolVersionError(server string, err error) error {
	return fmt.Errorf(
		"mcp[%s]: server rejected the protocol version (%s): it does not speak any version "+
			"this client supports (%s; latest %s). MCP 2026-07-28 servers advertise versions via "+
			"server/discover and are stateless; check whether a newer ggcode release supports "+
			"this server, or pin the server to a supported version.",
		server, err.Error(), strings.Join(knownProtocolVersionList(), ", "), latestMCPProtocolVersion)
}

func annotateResourceReadError(err error, uri string) error {
	je, ok := jsonRPCErrorOf(err)
	if !ok {
		return err
	}
	switch {
	case je.Code == legacyErrCodeResourceNotFound:
		return fmt.Errorf("resource not found: %s (legacy code %d; 2026-07-28 servers report this as -32602): %w",
			uri, je.Code, err)
	case je.Code == ErrCodeInvalidParams:
		return fmt.Errorf("resources/read rejected for %s with -32602 (Invalid Params); under MCP 2026-07-28 "+
			"this code also reports a missing resource, so verify the URI exists: %w", uri, err)
	default:
		return err
	}
}
