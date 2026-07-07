// Wakelock interface wrapper using Dart conditional imports.
// On ohos, wakelock_plus is not available — calls become no-ops.

export 'wakelock_io.dart' if (dart.library.html) 'wakelock_stub.dart';
