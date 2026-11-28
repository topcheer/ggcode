# Driving simulators with `mobile_device`

The `mobile_device` tool lets the agent control iOS simulators and Android
emulators/devices: list devices, boot, install and launch apps, read the
accessibility tree (`snapshot`), tap/type/swipe/press, read logs and take
screenshots. It is registered when `adb` or Xcode's `xcrun` is available.

## iOS: idb first, AppleScript as fallback

On macOS iOS control goes through [idb](https://github.com/facebook/idb)
(facebook/idb). idb talks to the simulator through its companion, so logical
point taps, text input and the accessibility tree all work **without a visible
Simulator window**. The older AppleScript path (`System Events` clicks on the
simulator window) needs Accessibility permission, a visible window and screen
coordinates that only match simulator points when the window sits at the
screen origin; it is used only when idb is unavailable.

### Automatic installation

When `idb` is missing the first time the tool runs on macOS, ggcode starts a
detached one-shot installer in the background (it never blocks the tool call):

1. `brew install idb-companion` — Homebrew 7+ refuses formulae from taps the
   user has not trusted; the installer trusts only the idb maintainers' tap
   (`meta/fb` / `facebook/fb`) and retries.
2. The `fb-idb` client is installed into a ggcode-owned virtualenv
   (`~/.ggcode/idb`), so no `pipx`, PEP 668 or `PATH` changes are needed.

The action that triggered the installation falls back to the AppleScript path
and the result says that the installation is in progress; the next call picks
idb up. The installer log is `~/.ggcode/logs/idb-install.log`. Set
`GGCODE_MOBILE_NO_INSTALL=1` to disable the auto-install.

### Coordinates

- `snapshot` returns the accessibility tree with `@eN` refs and `rect` values
  in **logical points**. Prefer `ref=`: the tool taps the element centre.
- Without `ref`, `x`/`y` are **screenshot pixels** (the full-resolution
  screenshot the tool takes). The tool divides by the device scale on iOS.

### Text input (`type`)

`idb ui text` sends HID keystrokes, which pass through the simulator
keyboard's input method: with a Chinese/Japanese IME active, punctuation and
long ASCII runs get recomposed into candidate words (a `wss://` connect URL
came out as `wss：//gateway。ggcode。dev…`). The tool therefore pastes instead:

1. the text is written to the simulator pasteboard (`pbcopy` / `simctl pbcopy`)
   and read back until the device sees it;
2. the tool waits until a text field reports keyboard focus (`IsEditing`);
3. on an **empty** field it long-presses the field and taps the edit menu's
   **Paste** item (a real user gesture, so iOS never raises the cross-app
   paste confirmation);
4. otherwise it sends Cmd+V and taps the **Paste** confirmation callout if one
   appears; if none appears it falls back to the edit menu, but never pastes
   over a selected word (it dismisses the menu instead).

The result says which path was used (`Paste menu item`, `Paste callout
confirmed`, `no Paste callout appeared`, `keystrokes via idb ui text`) so the
agent knows whether to verify the field content. Typing into a field that does
not have focus is refused (`no text field has keyboard focus - tap the field
first`).

### Caveats

- `@eN` refs are indices into the tree of the **last** `snapshot`. On a screen
  that keeps updating (a live chat projection) the tree changes between the
  snapshot and the tap and the ref drifts to a neighbouring element (observed:
  a composer ref landed on *Attach image*). Take a fresh `snapshot` right
  before tapping on such screens.
- `logs` returns the device system log on iOS (use `xcrun simctl spawn <udid>
  log show --predicate 'process == "Runner"'` for the app's own `debugPrint`
  output).
- Swipes follow the standard touch direction (finger up scrolls towards newer
  content), verified against iOS Settings; a live chat list may still jump
  because new events re-pin it to the bottom.
- On a shared Mac other agents may `shutdown`/`delete` simulators; idb then
  fails with `dtuhidd … invalidated`. Use a dedicated device for automation.

## Android

Android uses `adb` (`uiautomator` dump for the tree, `input` for taps/swipes,
`adb shell input text` for ASCII). Element refs work the same way as on iOS.

## Example

```
mobile_device action=snapshot platform=ios device=<UDID>
mobile_device action=tap ref=@e5 platform=ios device=<UDID>
mobile_device action=type ref=@e7 text="wss://..." platform=ios device=<UDID>
mobile_device action=screenshot platform=ios device=<UDID>
```
