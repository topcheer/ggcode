// QR Scanner implementation for platforms with mobile_scanner (Android/iOS).

import 'package:flutter/material.dart';
import 'package:mobile_scanner/mobile_scanner.dart';

import 'platform_ohos.dart';
import 'scanner_stub.dart' as stub;

/// Builds a QR scanner widget that calls [onDetect] when a code is found.
///
/// #1871 case 2: Dart conditional imports have no ohos condition, so the
/// export chain (`if (dart.library.html)`) routes ohos HERE - where
/// mobile_scanner has no ohos implementation and throws
/// MissingPluginException at runtime. Dispatch to the manual-input stub
/// at RUNTIME instead (the stub compiles fine on every platform).
Widget buildQrScanner({
  required void Function(String code) onDetect,
  void Function()? onPermissionError,
}) {
  if (isOhos) {
    return stub.buildQrScanner(
      onDetect: onDetect,
      onPermissionError: onPermissionError,
    );
  }
  return MobileScanner(
    controller: MobileScannerController(
      detectionSpeed: DetectionSpeed.noDuplicates,
    ),
    onDetect: (capture) {
      final barcodes = capture.barcodes;
      if (barcodes.isNotEmpty) {
        final code = barcodes.first.rawValue;
        if (code != null && code.isNotEmpty) {
          onDetect(code);
        }
      }
    },
    errorBuilder: (context, error) {
      onPermissionError?.call();
      return Center(child: Text('Camera error: $error'));
    },
  );
}

/// Returns true if the current platform supports camera QR scanning.
bool get supportsQrScanner => !isOhos;
