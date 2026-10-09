package a2a

import "errors"

// ErrConfig marks A2A startup failures caused by invalid configuration -
// unknown auth provider, missing client_id/issuer, unfilled preset
// placeholders, bad mTLS material - as opposed to runtime conditions such
// as a port already in use. Callers use errors.Is(err, ErrConfig) to
// decide between fail-fast abort (configuration: retrying cannot help
// until the user edits ggcode.yaml) and warn-and-continue (#3677: the TUI
// path used to downgrade both classes to an invisible debug.Log, so a
// misconfigured server failed silently on every request).
var ErrConfig = errors.New("a2a: configuration error")
