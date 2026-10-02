import 'dart:convert';
import 'dart:io' show Directory, File;
import 'dart:typed_data';

import 'package:cryptography/cryptography.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:path_provider/path_provider.dart';

import '../models/protocol.dart' as proto;

/// Mobile file transfer V1 (agent → mobile, any format).
/// See docs/design/mobile-file-transfer.md.
///
/// Transfer state is keyed by file_id. The host enforces a 50 MiB cap and
/// chunk framing; this provider additionally re-validates the cap before
/// allocating (never trust the wire), verifies sha256 before any bytes are
/// written or surfaced, and discards ALL partial state on reset/reconnect
/// (V1 does not resume).
class FileTransferEntry {
  final String fileId;
  final String filename; // display metadata only — never resolved as a path
  final String mime;
  final int size;
  final String expectedSha256;
  final int totalChunks;
  final String caption;

  final Uint8List? buffer; // preallocated from validated size
  final Set<int> receivedChunks;
  final FileTransferStatus status;
  final String? savedPath; // set once verified + written
  final String? error;

  const FileTransferEntry({
    required this.fileId,
    required this.filename,
    required this.mime,
    required this.size,
    required this.expectedSha256,
    required this.totalChunks,
    this.caption = '',
    this.buffer,
    this.receivedChunks = const {},
    this.status = FileTransferStatus.transferring,
    this.savedPath,
    this.error,
  });

  int get receivedBytes =>
      receivedChunks.length * FileTransferProvider.chunkSize > size
          ? size
          : receivedChunks.length * FileTransferProvider.chunkSize;

  FileTransferEntry copyWith({
    Uint8List? buffer,
    Set<int>? receivedChunks,
    FileTransferStatus? status,
    String? savedPath,
    String? error,
  }) =>
      FileTransferEntry(
        fileId: fileId,
        filename: filename,
        mime: mime,
        size: size,
        expectedSha256: expectedSha256,
        totalChunks: totalChunks,
        caption: caption,
        buffer: buffer ?? this.buffer,
        receivedChunks: receivedChunks ?? this.receivedChunks,
        status: status ?? this.status,
        savedPath: savedPath ?? this.savedPath,
        error: error ?? this.error,
      );
}

enum FileTransferStatus { transferring, verifying, done, failed }

/// Pure assembly + verification core, testable without platform channels.
/// Exposed for unit tests via [FileTransferProvider.applyChunkPure].
class FileAssembly {
  final Uint8List buffer;
  final Set<int> received = {};

  FileAssembly(int size) : buffer = Uint8List(size);

  void write(int index, Uint8List bytes, int chunkSize) {
    final offset = index * chunkSize;
    final end = (offset + bytes.length) > buffer.length
        ? buffer.length
        : offset + bytes.length;
    buffer.setRange(offset, end, bytes);
    received.add(index);
  }

  bool get completeAllChunks =>
      received.length >= (buffer.isEmpty
          ? 1
          : (buffer.length + chunkSizePure - 1) ~/ chunkSizePure);

  static const int chunkSizePure = 512 * 1024;
}

/// Pure assembly/verification core, independent of Riverpod so tests can
/// instantiate it directly (V1: no resume, sha256-gated writes).
class FileTransferCore {
  final Map<String, FileTransferEntry> _entries = {};

  /// Set by the Riverpod shell (or tests) to observe state changes.
  void Function(Map<String, FileTransferEntry> entries)? onChanged;

  Map<String, FileTransferEntry> get entries => Map.unmodifiable(_entries);

  void _publish() {
    onChanged?.call(Map.unmodifiable(_entries));
  }

  FileTransferEntry? byId(String fileId) => _entries[fileId];

  void reset() {
    // V1 does not resume across reconnects: discard all partial state.
    _entries.clear();
    _publish();
  }

  /// Offer: create (or replace — re-offer is idempotent per file_id) the
  /// transfer entry. Re-validates the cap locally before allocating.
  void handleOffer(proto.FileOfferData offer) {
    if (offer.size <= 0 || offer.size > FileTransferProvider.maxFileSize) {
      _entries[offer.fileId] = FileTransferEntry(
        fileId: offer.fileId,
        filename: offer.filename,
        mime: offer.mime,
        size: offer.size,
        expectedSha256: offer.sha256,
        totalChunks: offer.chunks,
        caption: offer.caption,
        status: FileTransferStatus.failed,
        error: 'Rejected: size ${offer.size} exceeds the 50 MiB transfer cap',
      );
      _publish();
      return;
    }
    _entries[offer.fileId] = FileTransferEntry(
      fileId: offer.fileId,
      filename: offer.filename,
      mime: offer.mime,
      size: offer.size,
      expectedSha256: offer.sha256,
      totalChunks: offer.chunks,
      caption: offer.caption,
      buffer: Uint8List(offer.size), // prealloc after local cap validation
      receivedChunks: {},
    );
    _publish();
  }

  void handleChunk(proto.FileChunkData chunk) {
    final entry = _entries[chunk.fileId];
    if (entry == null || entry.buffer == null) return;
    if (entry.status != FileTransferStatus.transferring) return;
    if (chunk.index < 0 || chunk.index >= entry.totalChunks) return;

    Uint8List bytes;
    try {
      bytes = base64Decode(chunk.data);
    } catch (_) {
      return; // malformed chunk: ignore; sha256 gate catches loss
    }
    final updated = Set<int>.from(entry.receivedChunks);
    _writeInto(entry.buffer!, chunk.index, bytes);
    updated.add(chunk.index);
    _entries[chunk.fileId] = entry.copyWith(receivedChunks: updated);
    _publish();

    if (updated.length >= entry.totalChunks) {
      _finalize(chunk.fileId);
    }
  }

  Uint8List _writeInto(Uint8List buffer, int index, Uint8List bytes) {
    final offset = index * FileTransferProvider.chunkSize;
    final end = (offset + bytes.length) > buffer.length
        ? buffer.length
        : offset + bytes.length;
    buffer.setRange(offset, end, bytes);
    return buffer;
  }

  void handleDone(proto.FileDoneData done) => _finalize(done.fileId);

  Future<void> _finalize(String fileId) async {
    final entry = _entries[fileId];
    if (entry == null ||
        entry.status == FileTransferStatus.done ||
        entry.status == FileTransferStatus.verifying) {
      return;
    }
    if (entry.buffer == null ||
        entry.receivedChunks.length < entry.totalChunks) {
      return; // not all chunks arrived; wait (or a reset discards us)
    }
    _entries[fileId] = entry.copyWith(status: FileTransferStatus.verifying);
    _publish();

    // sha256 gate: unverified files are NEVER written or surfaced.
    final digest = await Sha256().hash(entry.buffer!);
    final hex = digest.bytes
        .map((b) => b.toRadixString(16).padLeft(2, '0'))
        .join();
    if (_hexFold(hex) != _hexFold(entry.expectedSha256)) {
      _entries[fileId] = entry.copyWith(
        status: FileTransferStatus.failed,
        error: 'sha256 mismatch — transfer discarded',
      );
      _publish();
      return;
    }

    // Write to <docs>/received/<sanitized-filename>, collision → (2), (3)…
    try {
      final path = await _writeReceived(entry);
      _entries[fileId] = entry.copyWith(
        status: FileTransferStatus.done,
        savedPath: path,
      );
    } catch (e) {
      _entries[fileId] = entry.copyWith(
        status: FileTransferStatus.failed,
        error: 'save failed: $e',
      );
    }
    _publish();
  }

  static String _hexFold(String s) => s.toLowerCase().trim();

  Future<String> _writeReceived(FileTransferEntry entry) async {
    final docs = await getApplicationDocumentsDirectory();
    final dir =
        await Directory('${docs.path}/received').create(recursive: true);
    return writeFileWithCollision(
        dir.path, sanitizeFilename(entry.filename), entry.buffer!);
  }

  /// Writes `name` into `dirPath`, renaming to `name (2).ext`, `(3)`… on
  /// collision. Public+static so tests can drive it against a temp dir.
  static Future<String> writeFileWithCollision(
      String dirPath, String name, Uint8List bytes) async {
    var target = '$dirPath/$name';
    if (!await File(target).exists()) {
      await File(target).writeAsBytes(bytes, flush: true);
      return target;
    }
    final dot = name.lastIndexOf('.');
    final stem = dot > 0 ? name.substring(0, dot) : name;
    final ext = dot > 0 ? name.substring(dot) : '';
    for (var i = 2; i < 1000; i++) {
      target = '$dirPath/$stem ($i)$ext';
      if (!await File(target).exists()) {
        await File(target).writeAsBytes(bytes, flush: true);
        return target;
      }
    }
    throw StateError('could not find a free filename for $name');
  }

  /// Client-side filename sanitization (defense in depth; the host already
  /// strips separators — the client never resolves paths from it anyway).
  static String sanitizeFilename(String name) {
    var n = name.replaceAll(RegExp(r'[/\\]'), '').replaceAll('\x00', '').trim();
    if (n.isEmpty) n = 'file';
    if (n.length > 255) n = n.substring(0, 255);
    return n;
  }
}

class FileTransferProvider {
  static const int chunkSize = 512 * 1024;
  static const int maxFileSize = 50 * 1024 * 1024;
}

/// Riverpod shell: exposes the core's map as provider state.
class FileTransferNotifier extends Notifier<Map<String, FileTransferEntry>> {
  FileTransferCore? _core;

  @override
  Map<String, FileTransferEntry> build() {
    final core = FileTransferCore();
    core.onChanged = (entries) {
      if (ref.mounted) state = entries;
    };
    _core = core;
    return core.entries;
  }

  FileTransferCore get core => _core!;
}

final fileTransferProvider =
    NotifierProvider<FileTransferNotifier, Map<String, FileTransferEntry>>(
        FileTransferNotifier.new);
