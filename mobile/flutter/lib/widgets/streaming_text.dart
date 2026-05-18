import 'package:flutter/material.dart';

/// Animated text widget that shows a typing cursor for streaming text
class StreamingText extends StatefulWidget {
  final String text;
  final TextStyle? style;
  final bool showCursor;

  const StreamingText({
    super.key,
    required this.text,
    this.style,
    this.showCursor = true,
  });

  @override
  State<StreamingText> createState() => _StreamingTextState();
}

class _StreamingTextState extends State<StreamingText>
    with SingleTickerProviderStateMixin {
  late final AnimationController _cursorController;

  @override
  void initState() {
    super.initState();
    _cursorController = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 500),
    )..repeat(reverse: true);
  }

  @override
  void dispose() {
    _cursorController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.end,
      children: [
        Flexible(child: Text(widget.text, style: widget.style)),
        if (widget.showCursor)
          FadeTransition(
            opacity: _cursorController,
            child: Text(
              '|',
              style: widget.style?.copyWith(fontWeight: FontWeight.w100) ??
                  const TextStyle(fontWeight: FontWeight.w100),
            ),
          ),
      ],
    );
  }
}
