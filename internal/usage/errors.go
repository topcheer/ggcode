package usage

import "errors"

// ErrUnsupported is returned when the active vendor has no probe
// registered (the caller renders nothing rather than an error).
var ErrUnsupported = errors.New("usage: no probe for this vendor")
