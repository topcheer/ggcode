import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:cryptography/cryptography.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:ggcode_mobile/core/models/protocol.dart' as proto;
import 'package:ggcode_mobile/core/providers/file_transfer_provider.dart';

void main() {
  group('FileTransferNotifier assembly', () {
    test('chunks assemble into the offered buffer and verify done', () async {
      final n = FileTransferCore();
      final raw = Uint8List.fromList(
          List<int>.generate(600 * 1024, (i) => i & 0xFF)); // 2 chunks
      final digest = await Sha256().hash(raw);
      final hex =
          digest.bytes.map((b) => b.toRadixString(16).padLeft(2, '0')).join();

      n.handleOffer(proto.FileOfferData(
        fileId: 'f1',
        filename: 'a.bin',
        mime: 'application/octet-stream',
        size: raw.length,
        sha256: hex,
        chunks: 2,
      ));
      expect(n.byId('f1')!.status, FileTransferStatus.transferring);

      // Per-chunk base64, exactly like the host frames it.
      final c0 = base64Encode(raw.sublist(0, 512 * 1024));
      final c1 = base64Encode(raw.sublist(512 * 1024));
      n.handleChunk(proto.FileChunkData(fileId: 'f1', index: 0, data: c0));
      n.handleChunk(proto.FileChunkData(fileId: 'f1', index: 1, data: c1));

      final entry = n.byId('f1')!;
      expect(entry.receivedChunks.length, 2);
      expect(entry.buffer, raw);
    });

    test('sha256 mismatch marks failed and never surfaces a file',
        () async {
      final n = FileTransferCore();
      final raw = Uint8List.fromList(List<int>.filled(1024, 7));
      n.handleOffer(proto.FileOfferData(
        fileId: 'f2',
        filename: 'b.bin',
        mime: 'application/octet-stream',
        size: raw.length,
        sha256: 'deadbeef', // wrong on purpose
        chunks: 1,
      ));
      n.handleChunk(proto.FileChunkData(
          fileId: 'f2', index: 0, data: base64Encode(raw)));
      // finalize is async (hash + write): pump until terminal.
      await Future<void>.delayed(Duration.zero);
      await Future<void>.delayed(Duration.zero);
      final entry = n.byId('f2')!;
      expect(entry.status, FileTransferStatus.failed);
      expect(entry.savedPath, isNull);
      expect(entry.error, contains('sha256'));
    });

    test('oversize offer rejected locally before allocation', () {
      final n = FileTransferCore();
      n.handleOffer(proto.FileOfferData(
        fileId: 'f3',
        filename: 'c.bin',
        mime: 'application/octet-stream',
        size: 50 * 1024 * 1024 + 1,
        sha256: '',
        chunks: 101,
      ));
      final entry = n.byId('f3')!;
      expect(entry.status, FileTransferStatus.failed);
      expect(entry.buffer, isNull); // never allocated
      expect(entry.error, contains('50 MiB'));
    });

    test('reset discards partial state (no resume in V1)', () {
      final n = FileTransferCore();
      n.handleOffer(proto.FileOfferData(
        fileId: 'f4',
        filename: 'd.bin',
        mime: 'text/plain',
        size: 1024,
        sha256: 'x',
        chunks: 1,
      ));
      expect(n.byId('f4'), isNotNull);
      n.reset();
      expect(n.byId('f4'), isNull);
    });
  });

  group('sanitizeFilename', () {
    test('strips separators and NUL, clamps, defaults', () {
      expect(FileTransferCore.sanitizeFilename('a/b\\c.txt'), 'abc.txt');
      expect(FileTransferCore.sanitizeFilename('  '), 'file');
      expect(FileTransferCore.sanitizeFilename('x' * 300).length, 255);
    });
  });

  group('protocol models', () {
    test('FileOfferData/FileChunkData/FileDoneData parse', () {
      final o = proto.FileOfferData.fromJson({
        'file_id': 'id1',
        'filename': 'x.png',
        'mime': 'image/png',
        'size': 12,
        'sha256': 'ab',
        'chunks': 1,
        'caption': 'cap',
      });
      expect(o.fileId, 'id1');
      expect(o.caption, 'cap');
      final c = proto.FileChunkData.fromJson(
          {'file_id': 'id1', 'index': 3, 'data': 'QQ=='});
      expect(c.index, 3);
      final d = proto.FileDoneData.fromJson({'file_id': 'id1'});
      expect(d.fileId, 'id1');
    });
  });

group('writeFileWithCollision', () {
  test('renames name (2).ext on collision', () async {
    final dir = await Directory.systemTemp.createTemp('ft');
    addTearDown(() => dir.delete(recursive: true));
    final p1 = await FileTransferCore.writeFileWithCollision(
        dir.path, 'report.pdf', Uint8List.fromList([1]));
    final p2 = await FileTransferCore.writeFileWithCollision(
        dir.path, 'report.pdf', Uint8List.fromList([2]));
    expect(p1.endsWith('report.pdf'), isTrue);
    expect(p2.endsWith('report (2).pdf'), isTrue);
    expect(await File(p1).readAsBytes(), [1]); // original untouched
    expect(await File(p2).readAsBytes(), [2]);
  });
});

}
