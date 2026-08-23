package notify

// bellPtr is a test helper for constructing NotificationConfig.Bell (*bool,
// #959): nil = default on, explicit false disables.
func bellPtr(b bool) *bool { return &b }
