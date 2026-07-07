// QR Scanner implementation for platforms with mobile_scanner (Android/iOS).

import 'package:flutter/material.dart';
import 'package:mobile_scanner/mobile_scanner.dart';

/// Builds a QR scanner widget that calls [onDetect] when a code is found.
Widget buildQrScanner({
  required void Function(String code) onDetect,
  void Function()? onPermissionError,
}) {
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
    errorBuilder: (context, error, child) {
      onPermissionError?.call();
      return Center(child: Text('Camera error: $error'));
    },
  );
}

/// Returns true if the current platform supports camera QR scanning.
bool get supportsQrScanner => true;
