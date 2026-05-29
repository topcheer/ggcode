import 'dart:io';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:ggcode_mobile/core/connection_service.dart';

void main() {
  test('relay recovery delay prefers retry_after_ms when present', () {
    expect(relayRecoveryDelay(), const Duration(seconds: 60));
    expect(relayRecoveryDelay(1500), const Duration(milliseconds: 1500));
    expect(
      relayRetryAfterMs(const {'retry_after_ms': 60000}),
      60000,
    );
  });

  test('ShareConnectionDescriptor keeps renew token out of public URL', () {
    final descriptor = ShareConnectionDescriptor.parse(
      'wss://relay.example/ws?proto=2&room_id=room-1&auth_ticket=auth-1&crypto_key=crypto-1',
    ).copyWith(renewToken: 'renew-1');

    expect(descriptor.isV2, isTrue);
    expect(descriptor.cryptoMaterial, 'crypto-1');
    expect(descriptor.publicUrl, isNot(contains('renew_token=')));
    expect(descriptor.publicUrl, contains('auth_ticket=auth-1'));
    expect(descriptor.runtimeUrl(), contains('renew_token=renew-1'));
  });

  test('ShareConnectionDescriptor keeps v3 crypto key out of public URL', () {
    final descriptor = ShareConnectionDescriptor.parse(
      'wss://relay.example/ws?proto=3&room_id=room-3&auth_ticket=auth-3&kx_pub=server-pub',
    ).copyWith(renewToken: 'renew-3');

    expect(descriptor.isV3, isTrue);
    expect(descriptor.serverPublicKey, 'server-pub');
    expect(descriptor.publicUrl, isNot(contains('crypto_key=')));
    expect(descriptor.publicUrl, contains('kx_pub=server-pub'));
    expect(descriptor.runtimeUrl(), contains('renew_token=renew-3'));
  });

  test('tunnel URL security allows local insecure URLs but rejects remote ones',
      () {
    expect(tunnelUrlSecurityError('ws://127.0.0.1:8080/ws?token=x'), isNull);
    expect(
        tunnelUrlSecurityError('ws://relay.example.com/ws?token=x'), isNotNull);
  });

  test('ConnectionService does not retry when relay returns room not found',
      () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() async {
      await server.close(force: true);
    });

    var requests = 0;
    server.listen((request) async {
      requests++;
      request.response.statusCode = HttpStatus.gone;
      request.response.write('room not found');
      await request.response.close();
    });

    final service = ConnectionService(
      descriptor: ShareConnectionDescriptor.parse(
        'ws://${server.address.host}:${server.port}/ws?token=stale-token',
      ),
    );
    addTearDown(service.dispose);

    final errors = <String>[];
    final statuses = <ConnectionStatus>[];
    final errorSub = service.errorStream.listen(errors.add);
    final statusSub = service.statusStream.listen(statuses.add);
    addTearDown(errorSub.cancel);
    addTearDown(statusSub.cancel);

    await service.connect();
    await Future<void>.delayed(const Duration(milliseconds: 2500));

    expect(requests, 1);
    expect(statuses, contains(ConnectionStatus.disconnected));
    expect(
      errors.where((error) => error.contains('Room not found')),
      isNotEmpty,
    );
  });

  test('ConnectionService rejects remote insecure relay URLs before dialing',
      () async {
    final service = ConnectionService(
      descriptor: ShareConnectionDescriptor.parse(
        'ws://relay.example.com/ws?token=remote-insecure',
      ),
    );
    addTearDown(service.dispose);

    final errors = <String>[];
    final statuses = <ConnectionStatus>[];
    final errorSub = service.errorStream.listen(errors.add);
    final statusSub = service.statusStream.listen(statuses.add);
    addTearDown(errorSub.cancel);
    addTearDown(statusSub.cancel);

    await service.connect();
    await Future<void>.delayed(const Duration(milliseconds: 100));

    expect(statuses, contains(ConnectionStatus.disconnected));
    expect(
      errors,
      contains(
        'Insecure relay URL is only allowed for localhost or private network hosts',
      ),
    );
  });

  test(
      'ConnectionService does not retry when relay sends room-not-found error frame after websocket upgrade',
      () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() async {
      await server.close(force: true);
    });

    var requests = 0;
    server.listen((request) async {
      requests++;
      final socket = await WebSocketTransformer.upgrade(request);
      socket.add(jsonEncode({
        'type': 'error',
        'reason': 'Room not found: stale or expired share token',
      }));
      await socket.close();
    });

    final service = ConnectionService(
      descriptor: ShareConnectionDescriptor.parse(
        'ws://${server.address.host}:${server.port}/ws?token=stale-token',
      ),
    );
    addTearDown(service.dispose);

    final errors = <String>[];
    final statuses = <ConnectionStatus>[];
    final errorSub = service.errorStream.listen(errors.add);
    final statusSub = service.statusStream.listen(statuses.add);
    addTearDown(errorSub.cancel);
    addTearDown(statusSub.cancel);

    await service.connect();
    await Future<void>.delayed(const Duration(milliseconds: 2500));

    expect(requests, 1);
    expect(statuses, contains(ConnectionStatus.disconnected));
    expect(
      errors.where((error) => error.contains('Room not found')),
      isNotEmpty,
    );
  });
}
