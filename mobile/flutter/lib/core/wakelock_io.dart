// Wakelock implementation for platforms that have wakelock_plus
// (Android/iOS/macOS).
//
// #1872 case 1: Dart conditional imports have no ohos condition, so the
// export chain (`if (dart.library.html)`) routes ohos HERE - where
// wakelock_plus has no ohos implementation and the call throws
// MissingPluginException at runtime (or the native build fails).
// Dispatch at RUNTIME instead: on ohos the calls become no-ops, matching
// the stub's contract (the scanner got the same treatment in #1871).
// The futures are also not left dangling: a plugin failure (including
// the fire-and-forget path on half-implemented platforms) is swallowed
// after logging instead of surfacing as an unhandled zone error.

import 'package:wakelock_plus/wakelock_plus.dart';

import 'platform_ohos.dart';

void wakelockEnable() {
  if (isOhos) {
    return; // no wakelock_plus implementation on ohos; keep screen-lock-free
  }
  WakelockPlus.enable().catchError((Object e) {
    // Fire-and-forget: a failed wakelock must not crash the zone.
    // ignore: avoid_print
    print('wakelock enable failed: $e');
  });
}

void wakelockDisable() {
  if (isOhos) {
    return;
  }
  WakelockPlus.disable().catchError((Object e) {
    // ignore: avoid_print
    print('wakelock disable failed: $e');
  });
}
