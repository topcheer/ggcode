// Platform-conditional exports for scanner and wakelock.
//
// Usage in app code:
//   import 'package:ggcode_mobile/core/platform_ohos.dart';
//
// On ohos: scanner/wakelock are stubbed out (text input for URL, no-op wakelock).
// On other platforms: real mobile_scanner and wakelock_plus are used.

import 'dart:io';

/// Whether we're running on HarmonyOS (ohos).
/// The ohos Flutter SDK sets Platform.operatingSystem to 'ohos'.
bool get isOhos => Platform.operatingSystem == 'ohos';
