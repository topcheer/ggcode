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
  Future<({String nonce, String ciphertext})> encryptData(
      List<int> plaintext) async {
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

String _hexEncode(List<int> bytes) =>
    bytes.map((byte) => byte.toRadixString(16).padLeft(2, '0')).join();

Uint8List _hexDecode(String value) {
  final normalized = value.trim();
  if (normalized.length.isOdd) {
    throw const FormatException('invalid hex length');
  }
  return Uint8List.fromList([
    for (var i = 0; i < normalized.length; i += 2)
      int.parse(normalized.substring(i, i + 2), radix: 16),
  ]);
}

class ShareKeyExchangeState {
  final crypto.X25519 _x25519 = crypto.X25519();
  final crypto.AesGcm _aesGcm = crypto.AesGcm.with256bits();
  final crypto.KeyPair _clientKeyPair;
  final String clientPublicKey;

  ShareKeyExchangeState._(this._clientKeyPair, this.clientPublicKey);

  static Future<ShareKeyExchangeState> create() async {
    final algorithm = crypto.X25519();
    final keyPair = await algorithm.newKeyPair();
    final publicKey = await keyPair.extractPublicKey();
    return ShareKeyExchangeState._(
      keyPair,
      _hexEncode(publicKey.bytes),
    );
  }

  Future<String> unwrapRoomKey({
    required String nonce,
    required String ciphertext,
    required String roomId,
    required String clientId,
    required String serverPublicKey,
  }) async {
    final remotePublicKey = crypto.SimplePublicKey(
      _hexDecode(serverPublicKey),
      type: crypto.KeyPairType.x25519,
    );
    final sharedSecret = await _x25519.sharedSecretKey(
      keyPair: _clientKeyPair,
      remotePublicKey: remotePublicKey,
    );
    final sharedBytes = await sharedSecret.extractBytes();
    final digest = await crypto.Sha256().hash(
      utf8.encode(
        'ggcode-share-v3\x00${_hexEncode(sharedBytes)}\x00$roomId\x00$clientId',
      ),
    );
    final secretKey = await _aesGcm.newSecretKeyFromBytes(digest.bytes);
    final combined = base64Decode(ciphertext);
    final cipherBytes = combined.sublist(0, combined.length - 16);
    final macBytes = combined.sublist(combined.length - 16);
    final secretBox = crypto.SecretBox(
      cipherBytes,
      nonce: base64Decode(nonce),
      mac: crypto.Mac(macBytes),
    );
    final plaintext = await _aesGcm.decrypt(
      secretBox,
      secretKey: secretKey,
    );
    return utf8.decode(plaintext);
  }
}
