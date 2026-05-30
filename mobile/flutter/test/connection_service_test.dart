import 'dart:convert';
import 'dart:async';
import 'dart:io';

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

  test('ConnectionService surfaces upgrade required on relay 410 handshake',
      () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() async {
      await server.close(force: true);
    });

    server.listen((request) async {
      request.response.statusCode = HttpStatus.gone;
      request.response.write(
        'GGCode share v3 is required. Please upgrade GGCode TUI/GUI/Mobile to the latest version.',
      );
      await request.response.close();
    });

    final service = ConnectionService(
      descriptor: ShareConnectionDescriptor.parse(
        'ws://${server.address.host}:${server.port}/ws?proto=3&room_id=room-upgrade&auth_ticket=auth-upgrade',
      ),
    );
    addTearDown(service.dispose);

    final errors = <String>[];
    final errorSub = service.errorStream.listen(errors.add);
    addTearDown(errorSub.cancel);

    await service.connect();
    await Future<void>.delayed(const Duration(milliseconds: 200));

    expect(
      errors,
      contains(
        'Upgrade required: please update GGCode Mobile/Desktop to the latest version.',
      ),
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

  test(
      'ConnectionService surfaces upgrade required when relay sends error frame',
      () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() async {
      await server.close(force: true);
    });

    server.listen((request) async {
      final socket = await WebSocketTransformer.upgrade(request);
      socket.add(jsonEncode({
        'type': 'error',
        'reason':
            'GGCode share v3 is required. Please upgrade GGCode TUI/GUI/Mobile to the latest version.',
      }));
      await socket.close();
    });

    final service = ConnectionService(
      descriptor: ShareConnectionDescriptor.parse(
        'ws://${server.address.host}:${server.port}/ws?proto=3&room_id=room-upgrade&auth_ticket=auth-upgrade',
      ),
    );
    addTearDown(service.dispose);

    final errors = <String>[];
    final errorSub = service.errorStream.listen(errors.add);
    addTearDown(errorSub.cancel);

    await service.connect();
    await Future<void>.delayed(const Duration(milliseconds: 200));

    expect(
      errors,
      contains(
        'Upgrade required: please update GGCode Mobile/Desktop to the latest version.',
      ),
    );
  });

  test('ConnectionService tolerates non-string connected metadata fields',
      () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() async {
      await server.close(force: true);
    });

    server.listen((request) async {
      final socket = await WebSocketTransformer.upgrade(request);
      socket.add(jsonEncode({
        'type': 'connected',
        'data': {
          'protocol_version': 1,
          'share_mode': {'mode': 'legacy'},
          'room_id': 123,
          'connect_mode': true,
          'notice': ['hello'],
          'renew_token': {'token': 'renew'},
        },
      }));
    });

    final service = ConnectionService(
      descriptor: ShareConnectionDescriptor.parse(
        'ws://${server.address.host}:${server.port}/ws?token=test-token',
      ),
    );
    addTearDown(service.dispose);

    final statuses = <ConnectionStatus>[];
    final statusSub = service.statusStream.listen(statuses.add);
    addTearDown(statusSub.cancel);

    await service.connect();
    await Future<void>.delayed(const Duration(milliseconds: 100));

    expect(statuses, contains(ConnectionStatus.connected));
  });

  test('ConnectionService flushes armed resume hello after relay connected',
      () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() async {
      await server.close(force: true);
    });

    final received = Completer<Map<String, dynamic>>();
    server.listen((request) async {
      final socket = await WebSocketTransformer.upgrade(request);
      socket.add(jsonEncode({
        'type': 'connected',
        'data': {
          'protocol_version': 1,
          'share_mode': 'legacy',
          'room_id': 'room-1',
          'connect_mode': 'legacy',
          'notice': '',
          'renew_token': '',
        },
      }));
      socket.listen((message) {
        if (!received.isCompleted) {
          received
              .complete(jsonDecode(message as String) as Map<String, dynamic>);
        }
      });
    });

    final service = ConnectionService(
      descriptor: ShareConnectionDescriptor.parse(
        'ws://${server.address.host}:${server.port}/ws?token=test-token',
      ),
    );
    addTearDown(service.dispose);
    service.armResumeHello(clientId: 'client-1', sessionId: 'sess-1');

    await service.connect();
    final resume = await received.future.timeout(const Duration(seconds: 2));

    expect(resume['type'], 'resume_hello');
    expect(resume['client_id'], 'client-1');
    expect(resume['session_id'], 'sess-1');
  });
}
