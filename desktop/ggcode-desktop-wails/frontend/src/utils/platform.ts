// Platform helpers shared by components that render shortcut labels or
// platform-specific chrome.
export const isMac: boolean = typeof navigator !== 'undefined' && (
  navigator.platform?.toLowerCase().includes('mac') || navigator.userAgent?.includes('Mac OS')
)

/** Modifier label for shortcut hints: "⌘" on macOS, "Ctrl+" elsewhere. */
export const modKey = isMac ? '⌘' : 'Ctrl+'
