import 'dart:convert';
import 'dart:math';
import 'dart:typed_data';
import 'package:cryptography/cryptography.dart' as crypto;

/// AES-GCM encryption using the token as key.
class TunnelCrypto {
  final Uint8List _keyBytes;
  final crypto.AesGcm _aesGcm = crypto.AesGcm.with256bits();

  TunnelCrypto(String tokenHex)
      : _keyBytes = _normalizeKey(Uint8List.fromList(utf8.encode(tokenHex)));

  static Uint8List _normalizeKey(Uint8List key) {
    if (key.length == 32) return key;
    if (key.length > 32) return Uint8List.fromList(key.sublist(0, 32));
    if (key.length == 16 || key.length == 24) return key;
    // Pad to 32
    final padded = Uint8List(32);
    padded.setAll(0, key);
    return padded;
  }

  /// Encrypt plaintext, returns {nonce: base64, ciphertext: base64}.
  /// Wire format: ciphertext = cipherText + mac (same as Go's AES-GCM Seal output).
  Future<({String nonce, String ciphertext})> encryptData(List<int> plaintext) async {
    final nonce = _randomBytes(12);
    final secretKey = await _aesGcm.newSecretKeyFromBytes(_keyBytes);
    final secretBox = await _aesGcm.encrypt(
      plaintext,
      secretKey: secretKey,
      nonce: nonce,
    );
    // Manual concatenation: cipherText + mac (NOT nonce + cipherText + mac)
    final combined = Uint8List.fromList([
      ...secretBox.cipherText,
      ...secretBox.mac.bytes,
    ]);
    return (
      nonce: base64Encode(nonce),
      ciphertext: base64Encode(combined),
    );
  }

  /// Decrypt base64 nonce + ciphertext, returns plaintext bytes.
  /// Expects ciphertext = cipherText + mac (Go AES-GCM format).
  Future<List<int>> decryptData(String nonceB64, String ciphertextB64) async {
    final nonce = base64Decode(nonceB64);
    final combined = base64Decode(ciphertextB64);
    // Split: mac is last 16 bytes, rest is cipherText
    final macBytes = combined.sublist(combined.length - 16);
    final cipherBytes = combined.sublist(0, combined.length - 16);

    final secretKey = await _aesGcm.newSecretKeyFromBytes(_keyBytes);
    final secretBox = crypto.SecretBox(
      cipherBytes,
      nonce: nonce,
      mac: crypto.Mac(macBytes),
    );
    return await _aesGcm.decrypt(secretBox, secretKey: secretKey);
  }

  Uint8List _randomBytes(int length) {
    final rng = Random.secure();
    return Uint8List.fromList(List.generate(length, (_) => rng.nextInt(256)));
  }
}
