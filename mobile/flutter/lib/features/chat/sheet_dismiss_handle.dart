import 'package:flutter/material.dart';

import '../../core/theme/app_theme.dart';

/// Tappable drag handle for modal bottom sheets.
///
/// `showModalBottomSheet(showDragHandle: true)` paints a bar that is
/// decoration only: the accessibility tree exposes the modal barrier's
/// "Dismiss" label over the sheet's top edge (so screen readers and UI
/// automation announce a dismiss control there), yet tapping the bar does
/// nothing - only the scrim dismisses. This handle keeps the same look and
/// makes the promise true: a tap, a screen-reader tap or UI automation on
/// the "Dismiss" element dismisses the sheet. The hit area is a comfortable
/// 44pt tall strip spanning the sheet.
class SheetDismissHandle extends StatelessWidget {
  const SheetDismissHandle({super.key, required this.onDismiss});

  final VoidCallback onDismiss;

  @override
  Widget build(BuildContext context) {
    // Same label Flutter gives the barrier, so assistive tech sees one
    // consistent "Dismiss" control regardless of where it reads it.
    final label = MaterialLocalizations.of(context).modalBarrierDismissLabel;
    return InkWell(
      onTap: onDismiss,
      child: Semantics(
        button: true,
        label: label,
        child: SizedBox(
          height: 44,
          child: Center(
            child: Container(
              width: 40,
              height: 4,
              decoration: BoxDecoration(
                color: AppColors.textSecondary.withValues(alpha: 0.5),
                borderRadius: BorderRadius.circular(2),
              ),
            ),
          ),
        ),
      ),
    );
  }
}
