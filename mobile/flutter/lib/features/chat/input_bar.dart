import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart'; // Iteration 17: HapticFeedback
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:image_picker/image_picker.dart';

import '../../core/models/protocol.dart' as proto;
import '../../core/providers/session_provider.dart';
import '../../core/l10n/app_localizations.dart';
import '../../core/theme/app_theme.dart';

class InputBar extends ConsumerStatefulWidget {
  final TextEditingController controller;
  const InputBar({super.key, required this.controller});

  @override
  ConsumerState<InputBar> createState() => _InputBarState();
}

class _PendingImage {
  final String mime;
  final String base64Data; // raw base64 (no data: prefix)
  final String name;
  _PendingImage({required this.mime, required this.base64Data, this.name = ''});
}

class _InputBarState extends ConsumerState<InputBar>
    with SingleTickerProviderStateMixin {
  late final AnimationController _busyPulseController;
  final List<_PendingImage> _pendingImages = [];
  final ImagePicker _imagePicker = ImagePicker();

  // M9: pending thumbnails were base64Decode'd inside build on every rebuild
  // (every keystroke). Decode once when the image is picked.
  final Map<String, Uint8List> _decodedThumbnails = <String, Uint8List>{};

  Uint8List _thumbnailBytes(_PendingImage img) =>
      _decodedThumbnails[img.base64Data] ??= base64Decode(img.base64Data);

  bool _wasEmpty = true;

  @override
  void initState() {
    super.initState();
    _busyPulseController = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 1400),
    );
  }

  @override
  void dispose() {
    _busyPulseController.dispose();
    super.dispose();
  }

  Future<void> _pickImage(ImageSource source) async {
    try {
      final XFile? picked = await _imagePicker.pickImage(
        source: source,
        maxWidth: 1024,
        maxHeight: 1024,
        imageQuality: 70,
      );
      if (picked == null) return;
      // #2847: the picker and byte reads can outlive the page (user backs
      // out while the camera/gallery UI or a large image read is in
      // flight) — guard every await before touching State.
      if (!mounted) return;
      final bytes = await picked.readAsBytes();
      if (!mounted) return;
      final base64Data = base64Encode(bytes);
      // Detect mime from extension, default to jpeg
      var mime = 'image/jpeg';
      final ext = picked.name.split('.').last.toLowerCase();
      if (ext == 'png') mime = 'image/png';
      if (ext == 'gif') mime = 'image/gif';
      if (ext == 'webp') mime = 'image/webp';
      setState(() {
        _pendingImages.add(
          _PendingImage(mime: mime, base64Data: base64Data, name: picked.name),
        );
      });
    } catch (e) {
      // Silently ignore picker errors (user cancelled, permission denied, etc.)
    }
  }

  void _removeImage(int index) {
    setState(() {
      _pendingImages.removeAt(index);
    });
  }

  @override
  Widget build(BuildContext context) {
    watchAppTheme(
      ref,
    ); // re-run on theme switch: AppColors is static/non-reactive
    final status = ref.watch(displayedAgentStatusProvider);
    final isRunning = status == 'busy';
    final canSend = ref.watch(canSendMessagesProvider);
    final isHistorical = ref.watch(isHistoricalViewProvider);
    final connState = ref.watch(connectionProvider);
    final inputEnabled = canSend;
    if (isRunning) {
      if (!_busyPulseController.isAnimating) {
        if (!AppMotion.reduceMotion(context))
          _busyPulseController.repeat(reverse: true);
      }
    } else if (_busyPulseController.isAnimating) {
      _busyPulseController.stop();
      _busyPulseController.value = 0;
    }
    String hintText;
    if (isHistorical) {
      hintText = t('chat.placeholder.cached');
    } else if (connState.status != ConnectionStatus.connected) {
      hintText = t('chat.placeholder.disconnected');
    } else if (isRunning) {
      hintText = t('chat.placeholder.working');
    } else {
      hintText = t('chat.placeholder.idle');
    }

    final hasImages = _pendingImages.isNotEmpty;
    final canSubmit =
        inputEnabled && (widget.controller.text.trim().isNotEmpty || hasImages);
    _wasEmpty = widget.controller.text.trim().isEmpty;

    // While the agent is running the composer still accepts a message (the
    // placeholder promises it), so once there is a draft offer Send next to
    // Stop instead of Stop alone; an empty draft keeps Stop only.
    final stopButton = _ActionButton(
      key: const ValueKey('stop'),
      icon: Icons.stop_circle,
      color: AppColors.danger,
      onPressed: () {
        HapticFeedback.mediumImpact();
        ref.read(connectionProvider.notifier).send({
          'type': 'interrupt',
          'data': {},
        });
      },
      tooltip: t('chat.interrupt'),
    );
    final sendButton = _ActionButton(
      key: const ValueKey('send'),
      icon: Icons.send,
      color: canSubmit ? AppColors.accent : AppColors.textMuted,
      onPressed: canSubmit ? _send : null,
      tooltip: t('chat.send'),
    );

    return Container(
      padding: EdgeInsets.fromLTRB(12, 8, 12, 14),
      decoration: BoxDecoration(color: AppColors.background),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          // Image preview thumbnails
          if (hasImages)
            Container(
              margin: const EdgeInsets.only(bottom: 8),
              height: 64,
              child: ListView.separated(
                scrollDirection: Axis.horizontal,
                itemCount: _pendingImages.length,
                separatorBuilder: (_, _) => const SizedBox(width: 8),
                itemBuilder: (context, index) {
                  final img = _pendingImages[index];
                  return Stack(
                    children: [
                      ClipRRect(
                        borderRadius: BorderRadius.circular(8),
                        child: Image.memory(
                          _thumbnailBytes(img),
                          width: 64,
                          height: 64,
                          cacheWidth: 128,
                          gaplessPlayback: true,
                          fit: BoxFit.cover,
                        ),
                      ),
                      Positioned(
                        top: 2,
                        right: 2,
                        child: GestureDetector(
                          onTap: () => _removeImage(index),
                          behavior: HitTestBehavior.opaque,
                          child: Container(
                            width: 32,
                            height: 32,
                            alignment: Alignment.center,
                            decoration: const BoxDecoration(
                              color: Colors.black54,
                              shape: BoxShape.circle,
                            ),
                            child: const Icon(
                              Icons.close,
                              size: 16,
                              color: Colors.white,
                            ),
                          ),
                        ),
                      ),
                    ],
                  );
                },
              ),
            ),
          Row(
            crossAxisAlignment: CrossAxisAlignment.end,
            children: [
              // Image picker button
              _ActionButton(
                icon: Icons.add_photo_alternate,
                color: inputEnabled ? AppColors.accent : AppColors.textMuted,
                onPressed: inputEnabled ? () => _showImageSourceDialog() : null,
                tooltip: t('chat.attach_image'),
              ),
              SizedBox(width: 10),
              Expanded(
                child: AnimatedBuilder(
                  animation: _busyPulseController,
                  builder: (context, child) {
                    final pulse = _busyPulseController.value;
                    final borderColor = isRunning
                        ? AppColors.accent.withValues(
                            alpha: 0.60 + pulse * 0.20,
                          )
                        : AppColors.border;
                    final shadowColor = AppColors.accent.withValues(
                      alpha: isRunning ? 0.10 + pulse * 0.18 : 0,
                    );
                    return DecoratedBox(
                      decoration: BoxDecoration(
                        color: AppColors.surface,
                        borderRadius: BorderRadius.circular(AppRadii.lg),
                        border: Border.all(color: borderColor),
                        boxShadow: [
                          ...AppShadows.panel,
                          if (isRunning)
                            BoxShadow(
                              color: shadowColor,
                              blurRadius: 12 + pulse * 10,
                              spreadRadius: pulse * 1.5,
                            ),
                        ],
                      ),
                      child: child,
                    );
                  },
                  child: TextField(
                    controller: widget.controller,
                    style: TextStyle(
                      color: AppColors.textPrimary,
                      fontSize: 14,
                    ),
                    decoration: InputDecoration(
                      hintText: hintText,
                      hintStyle: TextStyle(color: AppColors.textMuted),
                      filled: true,
                      fillColor: AppColors.surface,
                      border: OutlineInputBorder(
                        borderRadius: BorderRadius.circular(AppRadii.lg),
                        borderSide: BorderSide.none,
                      ),
                      enabledBorder: OutlineInputBorder(
                        borderRadius: BorderRadius.circular(AppRadii.lg),
                        borderSide: BorderSide.none,
                      ),
                      focusedBorder: OutlineInputBorder(
                        borderRadius: BorderRadius.circular(AppRadii.lg),
                        borderSide: BorderSide.none,
                      ),
                      contentPadding: const EdgeInsets.fromLTRB(18, 14, 18, 14),
                    ),
                    enabled: inputEnabled,
                    // M7: multi-line composer (was single-line with
                    // onSubmitted: _send, so multi-line prompts were impossible).
                    minLines: 1,
                    maxLines: 6,
                    keyboardType: TextInputType.multiline,
                    textInputAction: TextInputAction.newline,
                    // M10: rebuild only when the empty/non-empty state flips,
                    // not on every keystroke.
                    onChanged: (v) {
                      final empty = v.trim().isEmpty;
                      if (empty != _wasEmpty) {
                        setState(() => _wasEmpty = empty);
                      }
                    },
                  ),
                ),
              ),
              SizedBox(width: 10),
              if (canSend)
                AnimatedSwitcher(
                  duration: AppMotion.duration(context, AppMotion.micro),
                  switchInCurve: AppMotion.standard,
                  switchOutCurve: AppMotion.accelerate,
                  transitionBuilder: (child, anim) => FadeTransition(
                    opacity: anim,
                    child: ScaleTransition(
                      scale: Tween(begin: 0.85, end: 1.0).animate(anim),
                      child: child,
                    ),
                  ),
                  child: !isRunning
                      ? sendButton
                      : _wasEmpty
                          ? stopButton
                          : Row(
                              key: const ValueKey('stop-send'),
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                stopButton,
                                SizedBox(width: 8),
                                sendButton,
                              ],
                            ),
                ),
            ],
          ),
        ],
      ),
    );
  }

  void _showImageSourceDialog() {
    showModalBottomSheet(
      context: context,
      builder: (context) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              leading: Icon(Icons.photo_library, color: AppColors.accent),
              title: Text(t('chat.gallery')),
              onTap: () {
                Navigator.pop(context);
                _pickImage(ImageSource.gallery);
              },
            ),
            ListTile(
              leading: Icon(Icons.camera_alt, color: AppColors.accent),
              title: Text(t('chat.camera')),
              onTap: () {
                Navigator.pop(context);
                _pickImage(ImageSource.camera);
              },
            ),
          ],
        ),
      ),
    );
  }

  void _send() {
    if (!ref.read(canSendMessagesProvider)) return;
    final text = widget.controller.text.trim();
    final hasImages = _pendingImages.isNotEmpty;
    if (text.isEmpty && !hasImages) return;
    HapticFeedback.lightImpact(); // Iteration 17: haptic feedback on send
    widget.controller.clear();

    // Convert pending images to proto format
    final images = _pendingImages
        .map(
          (img) => proto.MessageImage(
            mime: img.mime,
            data: img.base64Data,
            name: img.name,
          ),
        )
        .toList();
    // #2847: wrap the clear in setState — controller.clear() is a
    // programmatic change that does NOT fire TextField.onChanged, so
    // without this the thumbnail row only rebuilt when a watched provider
    // happened to change in the same frame, leaving stale images that could
    // be double-sent.
    setState(() {
      _pendingImages.clear();
    });

    ref.read(chatProvider.notifier).addUserMessage(text, images: images);
  }
}

class _ActionButton extends StatelessWidget {
  final IconData icon;
  final Color color;
  final VoidCallback? onPressed;
  final String tooltip;

  const _ActionButton({
    super.key,
    required this.icon,
    required this.color,
    required this.onPressed,
    required this.tooltip,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      width: 46,
      height: 46,
      decoration: BoxDecoration(
        color: AppColors.surface,
        borderRadius: BorderRadius.circular(16),
        border: Border.all(color: AppColors.border),
        boxShadow: AppShadows.panel,
      ),
      child: IconButton(
        icon: Icon(icon, color: color, size: 20),
        onPressed: onPressed,
        tooltip: tooltip,
      ),
    );
  }
}
