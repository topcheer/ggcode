import 'dart:convert';
import 'dart:math';
import 'dart:typed_data';
import 'package:cryptography/cryptography.dart';

/// AES-GCM encryption using the token as key.
class TunnelCrypto {
  final List<int> _keyBytes;
  final AesGcm _aesGcm = AesGcm.with256bits();

  TunnelCrypto(String tokenHex)
      : _keyBytes = _normalizeKey(utf8.encode(tokenHex));

  static List<int> _normalizeKey(List<int> key) {
    final kl = key.length;
    if (kl == 16 || kl == 24 || kl == 32) return key;
    if (kl < 16) return [...key, ...List.filled(16 - kl, 0)];
    if (kl > 32) return key.sublist(0, 32);
    // 17-31: pad to 32
    return [...key, ...List.filled(32 - kl, 0)];
  }

  /// Encrypt plaintext, returns {nonce: base64, ciphertext: base64}.
  Future<({String nonce, String ciphertext})> encryptData(List<int> plaintext) async {
    final nonce = _randomBytes(12);
    final secretKey = await _aesGcm.newSecretKeyFromBytes(_keyBytes);
    final secretBox = await _aesGcm.encrypt(
      plaintext,
      secretKey: secretKey,
      nonce: nonce,
    );
    return (
      nonce: base64Encode(nonce),
      ciphertext: base64Encode(secretBox.concatenation()),
    );
  }

  /// Decrypt base64 nonce + ciphertext, returns plaintext bytes.
  Future<List<int>> decryptData(String nonceB64, String ciphertextB64) async {
    final nonce = base64Decode(nonceB64);
    final combined = base64Decode(ciphertextB64);
    // combined = ciphertext + mac (16 bytes for AES-GCM)
    final macBytes = combined.sublist(combined.length - 16);
    final cipherBytes = combined.sublist(0, combined.length - 16);

    final secretKey = await _aesGcm.newSecretKeyFromBytes(_keyBytes);
    final secretBox = SecretBox(
      cipherBytes,
      nonce: nonce,
      mac: Mac(macBytes),
    );
    return await _aesGcm.decrypt(secretBox, secretKey: secretKey);
  }

  List<int> _randomBytes(int length) {
    final rng = Random.secure();
    return List.generate(length, (_) => rng.nextInt(256));
  }
}
