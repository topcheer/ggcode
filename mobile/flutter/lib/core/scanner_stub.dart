// QR Scanner stub for platforms without mobile_scanner (ohos/HarmonyOS).
// Users paste connection URL manually instead of scanning.
// TODO: implement with @ohos.scankit when native plugin is available.

import 'package:flutter/material.dart';
import 'l10n/app_localizations.dart';

/// Builds a manual URL input instead of a QR scanner on ohos.
Widget buildQrScanner({
  required void Function(String code) onDetect,
  void Function()? onPermissionError,
}) {
  // Return a simple text input for manual URL entry.
  // This is used on HarmonyOS where camera scanning is not yet available.
  final controller = TextEditingController();
  return Padding(
    padding: const EdgeInsets.all(16),
    child: Column(
      mainAxisAlignment: MainAxisAlignment.center,
      children: [
        const Icon(Icons.link, size: 48, color: Colors.grey),
        const SizedBox(height: 16),
        TextField(
          controller: controller,
          decoration: const InputDecoration(
            labelText: 'Paste connection URL',
            hintText: 'ggcode://...',
            border: OutlineInputBorder(),
          ),
          onSubmitted: (value) {
            if (value.isNotEmpty) onDetect(value);
          },
        ),
        const SizedBox(height: 8),
        FilledButton(
          onPressed: () {
            final value = controller.text.trim();
            if (value.isNotEmpty) onDetect(value);
          },
          child: Text(t('connect.button_connect')),
        ),
      ],
    ),
  );
}

/// Returns false on ohos — camera QR scanning is not available.
bool get supportsQrScanner => false;
