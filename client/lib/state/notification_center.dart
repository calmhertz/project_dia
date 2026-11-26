import 'dart:async';
import 'dart:convert';
import 'dart:math' as math;

import 'package:flutter/foundation.dart';
import 'package:flutter/widgets.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

import '../api/api_client.dart';
import '../models/models.dart';
import '../services/background_notifications.dart';

typedef SocketConnector = WebSocketChannel Function(Uri uri);

/// The signed-in user's notifications while the app is open.
///
/// A WebSocket to the server says the moment something new exists, and the
/// center then fetches it over HTTP. When the socket is down it falls back to
/// polling every [interval], and it reconnects with backoff. Anything that
/// arrives is reported through [onArrived].
class NotificationCenter extends ChangeNotifier with WidgetsBindingObserver {
  NotificationCenter({
    required this.api,
    NotificationCursor? cursor,
    SocketConnector? connect,
    this.interval = const Duration(seconds: 30),
  }) : _cursor =
           cursor ??
           (BackgroundNotifications.isEnabled
               ? const SecureNotificationCursor()
               : InMemoryNotificationCursor()),
       _connect =
           connect ?? (liveUpdatesEnabled ? WebSocketChannel.connect : null);

  /// Set by `main`; widget tests leave it off so no real sockets are opened.
  static bool liveUpdatesEnabled = false;

  /// Even with a live socket, poll this often as a safety net.
  static const _safetyPoll = Duration(minutes: 5);
  static const _maxBackoff = Duration(seconds: 30);
  static const _keep = 50;

  final ApiClient api;
  final NotificationCursor _cursor;
  final SocketConnector? _connect;
  final Duration interval;

  /// Called with notifications that arrived after the first load, oldest
  /// first.
  void Function(List<AppNotification> arrived)? onArrived;

  List<AppNotification> _items = const [];
  int _unread = 0;
  int _newest = 0;
  bool _loaded = false;
  bool _refreshing = false;
  bool _refreshAgain = false;
  DateTime _lastRefresh = DateTime.fromMillisecondsSinceEpoch(0);
  Timer? _timer;
  bool _disposed = false;

  WebSocketChannel? _socket;
  StreamSubscription<dynamic>? _socketSub;
  Timer? _reconnectTimer;
  Duration _backoff = const Duration(seconds: 1);
  bool _live = false;
  bool _socketWanted = false;

  List<AppNotification> get items => _items;
  int get unreadCount => _unread;
  bool get loaded => _loaded;

  /// True while the WebSocket is connected and authenticated.
  bool get isLive => _live;

  void start() {
    WidgetsBinding.instance.addObserver(this);
    _timer = Timer.periodic(interval, (_) {
      if (!_live || DateTime.now().difference(_lastRefresh) > _safetyPoll) {
        refresh();
      }
    });
    refresh();
    _openSocket();
  }

  // On Android the OS freezes a backgrounded app and drops its sockets, so
  // close it ourselves and reconnect on return; the background job covers
  // the gap. Browsers keep background tabs connected.
  bool get _closeWhenHidden =>
      !kIsWeb && defaultTargetPlatform == TargetPlatform.android;

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    switch (state) {
      case AppLifecycleState.resumed:
        refresh();
        _openSocket();
      case AppLifecycleState.paused || AppLifecycleState.hidden:
        if (_closeWhenHidden) _closeSocket();
      default:
        break;
    }
  }

  void _openSocket() {
    final connect = _connect;
    if (connect == null || _disposed || _socket != null) return;
    final token = api.token;
    if (token == null) return;
    _socketWanted = true;
    _reconnectTimer?.cancel();

    final WebSocketChannel channel;
    try {
      channel = connect(api.notificationStreamUri);
    } catch (_) {
      _scheduleReconnect();
      return;
    }
    _socket = channel;
    _socketSub = channel.stream.listen(
      _onSocketMessage,
      onDone: () => _onSocketClosed(channel),
      onError: (_) {},
      cancelOnError: false,
    );
    channel.ready
        .then((_) {
          if (_socket != channel) return;
          channel.sink.add(jsonEncode({'type': 'auth', 'token': token}));
        })
        .catchError((_) {
          // onDone follows and schedules the reconnect.
        });
  }

  void _onSocketMessage(dynamic data) {
    if (data is! String) return;
    Map<String, dynamic> message;
    try {
      message = jsonDecode(data) as Map<String, dynamic>;
    } catch (_) {
      return;
    }
    switch (message['type']) {
      case 'ready':
        _live = true;
        _backoff = const Duration(seconds: 1);
        refresh(); // Catch up on anything sent while disconnected.
        notifyListeners();
      case 'notification' || 'resync':
        refresh();
    }
  }

  void _onSocketClosed(WebSocketChannel channel) {
    if (_socket != channel) return;
    final code = channel.closeCode;
    _socketSub = null;
    _socket = null;
    final wasLive = _live;
    _live = false;
    if (wasLive && !_disposed) notifyListeners();

    // 4401: the session ended. An HTTP call surfaces that to the session,
    // which signs out; reconnecting would only fail again.
    if (code == 4401 || code == 4403) {
      refresh();
      return;
    }
    if (_socketWanted) _scheduleReconnect();
  }

  void _scheduleReconnect() {
    if (_disposed || !_socketWanted) return;
    _reconnectTimer?.cancel();
    _reconnectTimer = Timer(_backoff, _openSocket);
    _backoff = Duration(
      milliseconds: math.min(
        _backoff.inMilliseconds * 2,
        _maxBackoff.inMilliseconds,
      ),
    );
  }

  void _closeSocket() {
    _socketWanted = false;
    _reconnectTimer?.cancel();
    final socket = _socket;
    _socket = null;
    _socketSub?.cancel();
    _socketSub = null;
    _live = false;
    socket?.sink.close(1000).catchError((_) {});
  }

  Future<void> refresh() async {
    if (_disposed) return;
    if (_refreshing) {
      // A push arrived mid-fetch: fetch again once this one finishes.
      _refreshAgain = true;
      return;
    }
    _refreshing = true;
    try {
      do {
        _refreshAgain = false;
        await _fetch();
      } while (_refreshAgain && !_disposed);
    } finally {
      _refreshing = false;
    }
  }

  Future<void> _fetch() async {
    try {
      final page = await api.notifications(afterId: _newest);
      if (_disposed) return;
      _lastRefresh = DateTime.now();
      final incoming = page.notifications;
      final firstLoad = !_loaded;

      if (firstLoad) {
        _items = incoming;
      } else if (incoming.isNotEmpty) {
        _items = [...incoming, ..._items].take(_keep).toList();
      }
      _unread = page.unreadCount;
      _loaded = true;

      if (incoming.isNotEmpty) {
        _newest = math.max(_newest, incoming.map((n) => n.id).reduce(math.max));
        final shown = await _cursor.read();
        if (_newest > shown) await _cursor.write(_newest);
        if (!firstLoad) {
          final fresh = incoming
              .where((n) => !n.isRead && n.id > shown)
              .toList()
              .reversed
              .toList();
          if (fresh.isNotEmpty) onArrived?.call(fresh);
        }
      }
      if (!_disposed) notifyListeners();
    } catch (_) {
      // Offline or the server is restarting: the next poll tries again.
    }
  }

  Future<void> markRead(AppNotification notification) async {
    if (notification.isRead) return;
    _items = [
      for (final n in _items) n.id == notification.id ? n.markedRead() : n,
    ];
    _unread = math.max(0, _unread - 1);
    notifyListeners();
    try {
      await api.markNotificationRead(notification.id);
    } catch (_) {}
  }

  Future<void> markAllRead() async {
    _items = [for (final n in _items) n.markedRead()];
    _unread = 0;
    notifyListeners();
    try {
      await api.markAllNotificationsRead();
    } catch (_) {}
  }

  @override
  void dispose() {
    _disposed = true;
    _timer?.cancel();
    _closeSocket();
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }
}
