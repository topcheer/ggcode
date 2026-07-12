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
      final bytes = await picked.readAsBytes();
      final base64Data = base64Encode(bytes);
      // Detect mime from extension, default to jpeg
      var mime = 'image/jpeg';
      final ext = picked.name.split('.').last.toLowerCase();
      if (ext == 'png') mime = 'image/png';
      if (ext == 'gif') mime = 'image/gif';
      if (ext == 'webp') mime = 'image/webp';
      setState(() {
        _pendingImages.add(_PendingImage(
          mime: mime,
          base64Data: base64Data,
          name: picked.name,
        ));
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
    final status = ref.watch(displayedAgentStatusProvider);
    final isRunning = status == 'busy';
    final canSend = ref.watch(canSendMessagesProvider);
    final isHistorical = ref.watch(isHistoricalViewProvider);
    final connState = ref.watch(connectionProvider);
    final inputEnabled = canSend;
    if (isRunning) {
      if (!_busyPulseController.isAnimating) {
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
    final canSubmit = inputEnabled && (widget.controller.text.trim().isNotEmpty || hasImages);

    return Container(
      padding: EdgeInsets.fromLTRB(12, 8, 12, 14),
      decoration: BoxDecoration(
        color: AppColors.background,
      ),
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
                separatorBuilder: (_, __) => const SizedBox(width: 8),
                itemBuilder: (context, index) {
                  final img = _pendingImages[index];
                  return Stack(
                    children: [
                      ClipRRect(
                        borderRadius: BorderRadius.circular(8),
                        child: Image.memory(
                          base64Decode(img.base64Data),
                          width: 64,
                          height: 64,
                          fit: BoxFit.cover,
                        ),
                      ),
                      Positioned(
                        top: 2,
                        right: 2,
                        child: GestureDetector(
                          onTap: () => _removeImage(index),
                          child: Container(
                            padding: const EdgeInsets.all(2),
                            decoration: BoxDecoration(
                              color: Colors.black.withValues(alpha: 0.6),
                              shape: BoxShape.circle,
                            ),
                            child: Icon(Icons.close, size: 14, color: Colors.white),
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
                tooltip: 'Attach Image',
              ),
              SizedBox(width: 10),
              Expanded(
                child: AnimatedBuilder(
                  animation: _busyPulseController,
                  builder: (context, child) {
                    final pulse = _busyPulseController.value;
                    final borderColor = isRunning
                        ? AppColors.accent.withValues(alpha: 0.60 + pulse * 0.20)
                        : AppColors.border;
                    final shadowColor = AppColors.accent
                        .withValues(alpha: isRunning ? 0.10 + pulse * 0.18 : 0);
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
                    style: TextStyle(color: AppColors.textPrimary, fontSize: 14),
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
                    onChanged: (_) => setState(() {}),
                    onSubmitted: (_) => _send(),
                  ),
                ),
              ),
              SizedBox(width: 10),
              if (isRunning && canSend)
                _ActionButton(
                  icon: Icons.stop_circle,
                  color: AppColors.danger,
                  onPressed: () {
                    ref.read(connectionProvider.notifier).send({
                      'type': 'interrupt',
                      'data': {},
                    });
                  },
                  tooltip: 'Interrupt',
                )
              else if (canSend)
                _ActionButton(
                  icon: Icons.send,
                  color: canSubmit ? AppColors.accent : AppColors.textMuted,
                  onPressed: canSubmit ? _send : null,
                  tooltip: 'Send',
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
        .map((img) => proto.MessageImage(
              mime: img.mime,
              data: img.base64Data,
              name: img.name,
            ))
        .toList();
    _pendingImages.clear();

    ref.read(chatProvider.notifier).addUserMessage(text, images: images);
  }
}

class _ActionButton extends StatelessWidget {
  final IconData icon;
  final Color color;
  final VoidCallback? onPressed;
  final String tooltip;

  const _ActionButton({
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
