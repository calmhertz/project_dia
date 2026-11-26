import 'dart:math' as math;

import 'package:flutter/foundation.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_local_notifications/flutter_local_notifications.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:workmanager/workmanager.dart';

import '../api/api_client.dart';
import '../models/models.dart';
import '../state/token_store.dart';

/// The id of the newest notification the user has already been shown, either
/// in the app or as a system notification. The app and the background job
/// share it so nothing is announced twice.
abstract class NotificationCursor {
  Future<int> read();
  Future<void> write(int id);
}

class SecureNotificationCursor implements NotificationCursor {
  const SecureNotificationCursor([
    this._storage = const FlutterSecureStorage(),
  ]);

  static const _key = 'aagasa.notifications.cursor';
  final FlutterSecureStorage _storage;

  @override
  Future<int> read() async {
    try {
      return int.tryParse(await _storage.read(key: _key) ?? '') ?? 0;
    } catch (_) {
      return 0;
    }
  }

  @override
  Future<void> write(int id) async {
    try {
      await _storage.write(key: _key, value: '$id');
    } catch (_) {}
  }
}

class InMemoryNotificationCursor implements NotificationCursor {
  int _id = 0;

  @override
  Future<int> read() async => _id;

  @override
  Future<void> write(int id) async => _id = id;
}

const _taskName = 'aagasa.check-notifications';
const _channelId = 'aagasa_events';
const _channelName = 'Station events';
const _maxIndividual = 3;

/// Android only: a WorkManager job that checks the server for new
/// notifications whenever the phone has a network connection (at most every
/// 15 minutes, the Android minimum), even when the app is closed, and shows
/// them as system notifications.
class BackgroundNotifications {
  BackgroundNotifications._();

  static bool _enabled = false;

  /// True once [initialize] ran on an Android device. Tests and the web never
  /// enable it.
  static bool get isEnabled => _enabled;

  static bool get _supported =>
      !kIsWeb && defaultTargetPlatform == TargetPlatform.android;

  static final _plugin = FlutterLocalNotificationsPlugin();

  /// Called once from `main`.
  static Future<void> initialize() async {
    if (!_supported) return;
    try {
      await Workmanager().initialize(notificationCallbackDispatcher);
      await _initPlugin();
      _enabled = true;
    } catch (error) {
      debugPrint('background notifications unavailable: $error');
    }
  }

  /// Starts the periodic check for the signed-in user and asks for the
  /// notification permission (Android 13+).
  static Future<void> start() async {
    if (!_enabled) return;
    try {
      await _plugin
          .resolvePlatformSpecificImplementation<
            AndroidFlutterLocalNotificationsPlugin
          >()
          ?.requestNotificationsPermission();
      await Workmanager().registerPeriodicTask(
        _taskName,
        _taskName,
        frequency: const Duration(minutes: 15),
        constraints: Constraints(networkType: NetworkType.connected),
        existingWorkPolicy: ExistingPeriodicWorkPolicy.keep,
      );
    } catch (error) {
      debugPrint('could not schedule notification checks: $error');
    }
  }

  /// Stops the periodic check, on sign-out.
  static Future<void> stop() async {
    if (!_enabled) return;
    try {
      await Workmanager().cancelByUniqueName(_taskName);
      await const SecureNotificationCursor().write(0);
      await _plugin.cancelAll();
    } catch (_) {}
  }

  static Future<void> _initPlugin() async {
    await _plugin.initialize(
      settings: const InitializationSettings(
        android: AndroidInitializationSettings('@mipmap/ic_launcher'),
      ),
    );
  }

  /// One background check: fetch what is new since the cursor and show it.
  static Future<void> checkNow() async {
    final token = await const SecureTokenStore().read();
    if (token == null || token.isEmpty) return;

    const cursor = SecureNotificationCursor();
    final last = await cursor.read();
    final api = ApiClient()..token = token;
    try {
      final page = await api.notifications(afterId: last, limit: 20);
      if (page.notifications.isEmpty) return;

      final newest = page.notifications.map((n) => n.id).reduce(math.max);
      await cursor.write(newest);

      // First check after sign-in: everything is already in the app's list.
      if (last == 0) return;

      final fresh = page.notifications.where((n) => !n.isRead).toList();
      if (fresh.isEmpty) return;
      await _initPlugin();
      await _show(fresh);
    } finally {
      api.dispose();
    }
  }

  static Future<void> _show(List<AppNotification> fresh) async {
    const details = NotificationDetails(
      android: AndroidNotificationDetails(
        _channelId,
        _channelName,
        channelDescription: 'Pass approvals, recordings and station alerts',
        importance: Importance.high,
        priority: Priority.high,
      ),
    );

    if (fresh.length > _maxIndividual) {
      await _plugin.show(
        id: 0,
        title: '${fresh.length} new Aagasa notifications',
        body: fresh.take(_maxIndividual).map((n) => n.title).join(' · '),
        notificationDetails: details,
      );
      return;
    }
    for (final n in fresh.reversed) {
      await _plugin.show(
        id: n.id & 0x7fffffff,
        title: n.title,
        body: n.body,
        notificationDetails: details,
        payload: n.passId,
      );
    }
  }
}

@pragma('vm:entry-point')
void notificationCallbackDispatcher() {
  Workmanager().executeTask((task, inputData) async {
    WidgetsFlutterBinding.ensureInitialized();
    try {
      await BackgroundNotifications.checkNow();
    } catch (error) {
      // Offline, server down or session expired: try again next period.
      debugPrint('notification check failed: $error');
    }
    return true;
  });
}
