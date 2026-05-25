import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:ggcode_mobile/core/connection_service.dart';
import 'package:ggcode_mobile/core/crypto.dart';

void main() {
  test('relay recovery delay prefers retry_after_ms when present', () {
    expect(relayRecoveryDelay(), const Duration(seconds: 60));
    expect(relayRecoveryDelay(1500), const Duration(milliseconds: 1500));
    expect(
      relayRetryAfterMs(const {'retry_after_ms': 60000}),
      60000,
    );
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
      url: 'ws://${server.address.host}:${server.port}/ws?token=stale-token',
      crypto: TunnelCrypto('stale-token'),
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
