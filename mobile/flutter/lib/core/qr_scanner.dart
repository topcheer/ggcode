// QR Scanner wrapper using Dart conditional imports.
// On ohos, mobile_scanner is not available — replaced with manual URL input.

export 'scanner_io.dart' if (dart.library.html) 'scanner_stub.dart';
