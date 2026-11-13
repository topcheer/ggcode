import 'package:flutter/material.dart';
import 'package:url_launcher/url_launcher.dart';

/// #5565 follow-up: shared scan-result router for web URLs.
///
/// Relay session QRs use the `ggcode://` custom scheme; plain http(s) URLs
/// (e.g. the et-tunnel WebUI QR printed by `/et on` in the TUI) are not relay
/// sessions. This helper offers to open them in the browser instead of
/// force-feeding them into the relay connect (which would blindly rewrite
/// https -> wss and never connect).
///
/// Returns true when [url] looks like a plain web URL and the dialog flow ran
/// (regardless of the user's choice) — callers should skip relay connect then.

/// True when [code] is a plain web URL that is NOT a relay session QR.
bool isPlainWebUrl(String code) {
  final trimmed = code.trim();
  return trimmed.startsWith('http://') || trimmed.startsWith('https://');
}

/// Offers to open [url] in the external browser. Returns true if opened.
Future<bool> offerOpenWebUrl(BuildContext context, String url) async {
  final open = await showDialog<bool>(
    context: context,
    builder: (ctx) => AlertDialog(
      title: const Text('Open Web UI?'),
      content: Text(
        'Scanned a web URL:\n$url\n\nOpen it in the browser?',
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(ctx).pop(false),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: () => Navigator.of(ctx).pop(true),
          child: const Text('Open'),
        ),
      ],
    ),
  );
  if (open != true) return false;
  final uri = Uri.tryParse(url);
  if (uri != null && await canLaunchUrl(uri)) {
    await launchUrl(uri, mode: LaunchMode.externalApplication);
    return true;
  }
  if (context.mounted) {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text('Could not open: $url')),
    );
  }
  return false;
}
