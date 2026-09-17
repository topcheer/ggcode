package mcp

// MCP 2026-07-28 error-code allocation policy (sa-70).
//
// Sources:
//   - https://modelcontextprotocol.io/specification/2026-07-28/changelog
//   - https://modelcontextprotocol.io/specification/2026-07-28/basic
//
// The 2026-07-28 revision partitions the JSON-RPC server-error range:
// -32000..-32019 stays implementation-defined (existing SDK usage is
// grandfathered); -32020..-32099 is reserved for the MCP specification,
// with codes defined exclusively by the spec and recorded in the schema.
// Codes introduced in the draft were renumbered accordingly:
// HeaderMismatch -32001 -> -32020, MissingRequiredClientCapability
// -32003 -> -32021, UnsupportedProtocolVersion -32004 -> -32022.
// The same revision changed the resource-not-found error code from the
// implementation-defined -32002 to -32602 (Invalid Params) to align with
// the JSON-RPC specification.
//
// GGCODE GAP THIS FILE CLOSES: the MCP client previously had no awareness
// of the reserved range or the renumbering. A server answering with
// -32020/-32021/-32022 surfaced as an opaque "JSON-RPC error -32020: ..."
// with no spec context; the initialize path did not recognize the spec's
// UnsupportedProtocolVersionError as a distinct, actionable condition; and
// resources/read failures did not normalize the legacy -32002 / new
// -32602 not-found reporting for the agent.

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// JSON-RPC standard code MCP servers overload for resource not found as of
// 2026-07-28 (see isResourceNotFoundCode).
const ErrCodeInvalidParams = -32602

// MCP 2026-07-28 spec-reserved server error codes (range -32020..-32099).
const (
	// ErrCodeHeaderMismatch: Streamable HTTP server rejected the required
	// standard MCP request headers (Mcp-Method / Mcp-Name, SEP-2243) -
	// typically an MCP-aware proxy or routing middleware stripped or
	// rewrote them.
	ErrCodeHeaderMismatch = -32020

	// ErrCodeMissingRequiredClientCapability: the request requires a
	// client capability the client did not advertise.
	ErrCodeMissingRequiredClientCapability = -32021

	// ErrCodeUnsupportedProtocolVersion (UnsupportedProtocolVersionError,
	// SEP-2575): the protocol version the request carries is not supported
	// by the server.
	ErrCodeUnsupportedProtocolVersion = -32022
)

// Pre-2026-07-28 draft numbering for the same conditions, published in the
// transport prose before the allocation policy renumbered them. Kept for
// interoperability with servers still using the grandfathered values; they
// sit in the implementation-defined -32000..-32019 zone and are NOT part
// of the reserved range.
const (
	legacyErrCodeHeaderMismatch                  = -32001
	legacyErrCodeResourceNotFound                = -32002
	legacyErrCodeMissingRequiredClientCapability = -32003
	legacyErrCodeUnsupportedProtocolVersion      = -32004
)

// isMCPReservedErrorCode reports whether code falls in the server-error
// sub-range the MCP 2026-07-28 specification reserves for itself
// (-32020..-32099). Codes outside it - including the grandfathered
// -32000..-32019 zone - remain implementation-defined.
func isMCPReservedErrorCode(code int) bool {
	return code >= -32099 && code <= -32020
}

// isUnsupportedProtocolVersionCode reports whether code is the spec's
// UnsupportedProtocolVersionError in either the renumbered (-32022) or the
// pre-policy draft (-32004) numbering.
func isUnsupportedProtocolVersionCode(code int) bool {
	return code == ErrCodeUnsupportedProtocolVersion ||
		code == legacyErrCodeUnsupportedProtocolVersion
}

// isResourceNotFoundCode reports whether code can carry a resource-not-found
// condition: the legacy implementation-defined -32002, or -32602, which the
// 2026-07-28 spec adopted for not-found ("Change resource not found error
// code from -32002 to -32602 (Invalid Params)"). -32602 is ambiguous on its
// own - callers combine it with method context.
func isResourceNotFoundCode(code int) bool {
	return code == legacyErrCodeResourceNotFound || code == ErrCodeInvalidParams
}

// specErrorCodeHint returns the spec-defined name plus an actionable hint
// for known spec error codes (reserved range, or the grandfathered legacy
// numbering of the same conditions). Unknown codes return empty strings.
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

// specAnnotatedError decorates a server JSON-RPC error whose code is
// spec-defined with the code's spec name and an actionable hint. It
// unwraps to the original *Error so errors.As keeps working for callers
// that classify by code.
type specAnnotatedError struct {
	err  *Error
	name string
	hint string
}

func (e *specAnnotatedError) Error() string {
	return fmt.Sprintf("%s [MCP spec: %s - %s]", e.err.Error(), e.name, e.hint)
}

func (e *specAnnotatedError) Unwrap() error { return e.err }

// decorateSpecError wraps a JSON-RPC error carrying a known spec-defined
// code (reserved range, or grandfathered legacy numbering) with spec
// context. Other errors pass through unchanged; the original *Error stays
// reachable via errors.As for programmatic classification.
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
		return err // idempotent
	}
	name, hint := specErrorCodeHint(je.Code)
	if name == "" {
		return err
	}
	return &specAnnotatedError{err: je, name: name, hint: hint}
}

// jsonRPCErrorOf extracts the JSON-RPC *Error from a wrapped error chain.
func jsonRPCErrorOf(err error) (*Error, bool) {
	var je *Error
	if errors.As(err, &je) {
		return je, true
	}
	return nil, false
}

// knownProtocolVersionList returns the sorted protocol versions this
// client accepts, for diagnostics.
func knownProtocolVersionList() []string {
	vs := make([]string, 0, len(knownMCPProtocolVersions))
	for v := range knownMCPProtocolVersions {
		vs = append(vs, v)
	}
	sort.Strings(vs)
	return vs
}

// unsupportedProtocolVersionError renders the initialize-time diagnostic
// for the spec's UnsupportedProtocolVersionError in either numbering.
// err must already carry the JSON-RPC error (it is included verbatim).
func unsupportedProtocolVersionError(server string, err error) error {
	return fmt.Errorf(
		"mcp[%s]: server rejected the protocol version (%s): it does not speak any version "+
			"this client supports (%s; latest %s). MCP 2026-07-28 servers advertise versions via "+
			"server/discover and are stateless; check whether a newer ggcode release supports "+
			"this server, or pin the server to a supported version.",
		server, err.Error(), strings.Join(knownProtocolVersionList(), ", "), latestMCPProtocolVersion)
}

// annotateResourceReadError normalizes resources/read failures for the
// 2026-07-28 resource-not-found change: the legacy -32002 code is
// unambiguous, while the new -32602 (Invalid Params) may be either a
// genuine parameter problem or a not-found, so the message says so.
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
