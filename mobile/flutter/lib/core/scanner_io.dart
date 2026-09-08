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
///
/// #1871 case 4: the controller is owned by a StatefulWidget below (was
/// created inline per build call - callers invoke this from build(), so
/// every rebuild leaked a camera session until Android ran out of
/// camera handles).
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
  return _MobileScannerHost(
    onDetect: onDetect,
    onPermissionError: onPermissionError,
  );
}

class _MobileScannerHost extends StatefulWidget {
  const _MobileScannerHost({
    required this.onDetect,
    this.onPermissionError,
  });

  final void Function(String code) onDetect;
  final void Function()? onPermissionError;

  @override
  State<_MobileScannerHost> createState() => _MobileScannerHostState();
}

class _MobileScannerHostState extends State<_MobileScannerHost> {
  final MobileScannerController _controller = MobileScannerController(
    detectionSpeed: DetectionSpeed.noDuplicates,
  );

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return MobileScanner(
      controller: _controller,
      onDetect: (capture) {
        final barcodes = capture.barcodes;
        if (barcodes.isNotEmpty) {
          final code = barcodes.first.rawValue;
          if (code != null && code.isNotEmpty) {
            widget.onDetect(code);
          }
        }
      },
      errorBuilder: (context, error) {
        widget.onPermissionError?.call();
        return Center(child: Text('Camera error: $error'));
      },
    );
  }
}

/// Returns true if the current platform supports camera QR scanning.
bool get supportsQrScanner => !isOhos;
