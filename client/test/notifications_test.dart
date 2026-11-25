import 'dart:convert';

import 'package:aagasa_client/api/api_client.dart';
import 'package:aagasa_client/models/models.dart';
import 'package:aagasa_client/screens/notifications_screen.dart';
import 'package:aagasa_client/state/notification_center.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:provider/provider.dart';
import 'package:web_socket/testing.dart';
import 'package:web_socket/web_socket.dart';
import 'package:web_socket_channel/adapter_web_socket_channel.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

import 'support.dart';

Map<String, dynamic> notificationJson({
  required int id,
  String kind = 'pass.approved',
  String severity = 'success',
  String title = 'Pass approved',
  String body = 'ISS (ZARYA) at Aug 26 10:00 UTC was approved.',
  String entityType = 'pass',
  String entityId = 'pass-1',
  bool read = false,
}) => {
  'id': id,
  'kind': kind,
  'severity': severity,
  'title': title,
  'body': body,
  'entity_type': entityType,
  'entity_id': entityId,
  'created_at': '2026-08-25T10:00:00Z',
  'read_at': read ? '2026-08-25T10:05:00Z' : null,
};

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  group('live stream', liveTests);

  ApiClient apiFor(FakeServer server) =>
      ApiClient(baseUrl: 'http://server.test', httpClient: server.client())
        ..token = server.token;

  test('the first load fills the list without announcing anything', () async {
    final server = FakeServer()
      ..responses['GET api/notifications'] = {
        'notifications': [notificationJson(id: 2), notificationJson(id: 1)],
        'unread_count': 2,
      };
    final center = NotificationCenter(
      api: apiFor(server),
      interval: const Duration(hours: 1),
    );
    final arrived = <AppNotification>[];
    center.onArrived = arrived.addAll;

    await center.refresh();

    expect(center.items.map((n) => n.id), [2, 1]);
    expect(center.unreadCount, 2);
    expect(arrived, isEmpty);
    center.dispose();
  });

  test('later polls ask only for newer items and announce them', () async {
    final server = FakeServer()
      ..responses['GET api/notifications'] = {
        'notifications': [notificationJson(id: 1)],
        'unread_count': 1,
      };
    final center = NotificationCenter(
      api: apiFor(server),
      interval: const Duration(hours: 1),
    );
    final arrived = <AppNotification>[];
    center.onArrived = arrived.addAll;
    await center.refresh();

    server.responses['GET api/notifications'] = {
      'notifications': [
        notificationJson(id: 3, title: 'Pass missed', severity: 'error'),
        notificationJson(id: 2, read: true),
      ],
      'unread_count': 2,
    };
    await center.refresh();

    expect(server.requested.last, 'GET api/notifications?after_id=1&limit=50');
    expect(center.items.map((n) => n.id), [3, 2, 1]);
    expect(arrived.map((n) => n.title), ['Pass missed']);
    expect(center.unreadCount, 2);
    center.dispose();
  });

  test('a failed poll keeps what is already shown', () async {
    final server = FakeServer()
      ..responses['GET api/notifications'] = {
        'notifications': [notificationJson(id: 1)],
        'unread_count': 1,
      };
    final center = NotificationCenter(
      api: apiFor(server),
      interval: const Duration(hours: 1),
    );
    await center.refresh();
    server.failures['GET api/notifications'] = (500, {'error': 'internal'});

    await center.refresh();

    expect(center.items, hasLength(1));
    center.dispose();
  });

  testWidgets('the bell shows the unread count and the list marks read', (
    tester,
  ) async {
    final server = FakeServer()
      ..responses['GET api/notifications'] = {
        'notifications': [
          notificationJson(
            id: 5,
            kind: 'worker.offline',
            severity: 'error',
            title: 'Worker offline',
            body: 'Worker worker-1 stopped sending heartbeats.',
            entityType: 'worker',
            entityId: 'worker-1',
          ),
        ],
        'unread_count': 1,
      }
      ..responses['POST api/notifications/5/read'] = {};
    final center = NotificationCenter(
      api: apiFor(server),
      interval: const Duration(hours: 1),
    );
    await tester.runAsync(center.refresh);

    await tester.pumpWidget(
      ChangeNotifierProvider.value(
        value: center,
        child: MaterialApp(
          home: Scaffold(appBar: AppBar(actions: const [NotificationBell()])),
        ),
      ),
    );
    expect(find.text('1'), findsOneWidget);

    await tester.tap(find.byTooltip('Notifications'));
    await tester.pumpAndSettle();
    expect(find.text('Worker offline'), findsOneWidget);
    expect(find.text('Mark all read'), findsOneWidget);

    await tester.tap(find.text('Worker offline'));
    await tester.runAsync(() => Future<void>.delayed(Duration.zero));
    await tester.pumpAndSettle();

    expect(server.requested, contains('POST api/notifications/5/read'));
    expect(center.unreadCount, 0);
    expect(find.text('Mark all read'), findsNothing);
    center.dispose();
  });
}

/// A server end of an in-memory WebSocket, plus how many times the center
/// tried to connect.
class FakeStream {
  final List<WebSocket> servers = [];
  final List<String> received = [];
  int connects = 0;

  WebSocketChannel connect(Uri uri) {
    connects++;
    final (client, server) = fakes();
    servers.add(server);
    server.events.listen((event) {
      if (event is TextDataReceived) received.add(event.text);
    });
    return AdapterWebSocketChannel(client);
  }

  WebSocket get current => servers.last;
}

Future<void> settle() => Future<void>.delayed(const Duration(milliseconds: 20));

void liveTests() {
  ApiClient apiFor(FakeServer server) =>
      ApiClient(baseUrl: 'http://server.test', httpClient: server.client())
        ..token = server.token;

  test('the stream URL follows the server URL', () {
    expect(
      ApiClient(baseUrl: 'https://station.example').notificationStreamUri
          .toString(),
      'wss://station.example/api/notifications/stream',
    );
    expect(
      ApiClient(baseUrl: 'http://dsl-pi:8080').notificationStreamUri.toString(),
      'ws://dsl-pi:8080/api/notifications/stream',
    );
  });

  test('a push makes the center fetch and announce at once', () async {
    final server = FakeServer()
      ..responses['GET api/notifications'] = {
        'notifications': [notificationJson(id: 1)],
        'unread_count': 1,
      };
    final stream = FakeStream();
    final center = NotificationCenter(
      api: apiFor(server),
      connect: stream.connect,
      interval: const Duration(hours: 1),
    );
    final arrived = <AppNotification>[];
    center.onArrived = arrived.addAll;
    center.start();
    await settle();

    expect(stream.received, [
      jsonEncode({'type': 'auth', 'token': 'test-token'}),
    ]);
    stream.current.sendText(jsonEncode({'type': 'ready'}));
    await settle();
    expect(center.isLive, isTrue);

    server.responses['GET api/notifications'] = {
      'notifications': [notificationJson(id: 2, title: 'Pass missed')],
      'unread_count': 2,
    };
    stream.current.sendText(jsonEncode({'type': 'notification', 'id': 2}));
    await settle();

    expect(arrived.map((n) => n.title), ['Pass missed']);
    expect(center.unreadCount, 2);
    center.dispose();
  });

  test('a dropped connection reconnects', () async {
    final server = FakeServer()
      ..responses['GET api/notifications'] = {
        'notifications': [],
        'unread_count': 0,
      };
    final stream = FakeStream();
    final center = NotificationCenter(
      api: apiFor(server),
      connect: stream.connect,
      interval: const Duration(hours: 1),
    );
    center.start();
    await settle();
    stream.current.sendText(jsonEncode({'type': 'ready'}));
    await settle();

    await stream.current.close(3000, 'restarting');
    await settle();
    expect(center.isLive, isFalse);

    await Future<void>.delayed(const Duration(milliseconds: 1200));
    expect(stream.connects, 2);
    center.dispose();
  });

  test('an ended session does not reconnect', () async {
    final server = FakeServer()
      ..responses['GET api/notifications'] = {
        'notifications': [],
        'unread_count': 0,
      };
    final stream = FakeStream();
    final center = NotificationCenter(
      api: apiFor(server),
      connect: stream.connect,
      interval: const Duration(hours: 1),
    );
    center.start();
    await settle();

    await stream.current.close(4401, 'session ended');
    await Future<void>.delayed(const Duration(milliseconds: 1200));

    expect(stream.connects, 1);
    center.dispose();
  });
}
