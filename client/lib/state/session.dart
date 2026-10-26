import 'package:flutter/foundation.dart';

import '../api/api_client.dart';
import '../api/api_exception.dart';
import '../models/models.dart';
import 'token_store.dart';

enum SessionStatus {
  restoring,
  signedOut,

  passwordChangeRequired,
  signedIn,
}

class Session extends ChangeNotifier {
  Session({required this.api, TokenStore? storage})
    : _storage = storage ?? const SecureTokenStore() {
    api.onSessionExpired = _handleExpiry;
  }

  final ApiClient api;
  final TokenStore _storage;

  SessionStatus _status = SessionStatus.restoring;
  AuthUser? _user;
  String? _error;

  SessionStatus get status => _status;
  AuthUser? get user => _user;
  String? get error => _error;

  bool get isSignedIn => _status == SessionStatus.signedIn;

  Future<void> restore() async {
    String? stored;
    try {
      stored = await _storage.read();
    } catch (_) {
      stored = null;
    }
    if (stored == null || stored.isEmpty) {
      _set(SessionStatus.signedOut);
      return;
    }

    api.token = stored;
    try {
      final user = await api.currentUser();
      _user = user;
      _set(
        user.mustChangePassword
            ? SessionStatus.passwordChangeRequired
            : SessionStatus.signedIn,
      );
    } on ApiException {
      await _clearToken();
      _set(SessionStatus.signedOut);
    } on NetworkException {
      await _clearToken();
      _set(SessionStatus.signedOut);
    }
  }

  Future<bool> signIn(String username, String password) async {
    _error = null;
    try {
      final result = await api.login(username, password);
      api.token = result.token;
      await _writeToken(result.token);
      _user = result.user;
      _set(
        result.user.mustChangePassword
            ? SessionStatus.passwordChangeRequired
            : SessionStatus.signedIn,
      );
      return true;
    } on ApiException catch (error) {
      _error = error.message;
    } on NetworkException catch (error) {
      _error = error.message;
    }
    notifyListeners();
    return false;
  }

  Future<bool> changePassword(String current, String next) async {
    _error = null;
    try {
      await api.changePassword(current, next);
      await signOut();
      return true;
    } on ApiException catch (error) {
      _error = error.message;
    } on NetworkException catch (error) {
      _error = error.message;
    }
    notifyListeners();
    return false;
  }

  Future<void> signOut() async {
    try {
      await api.logout();
    } catch (_) {
    }
    await _clearToken();
    _user = null;
    _set(SessionStatus.signedOut);
  }

  void _handleExpiry() {
    if (_status == SessionStatus.signedOut) return;
    _clearToken();
    _user = null;
    _error = 'Your session has expired. Please sign in again.';
    _set(SessionStatus.signedOut);
  }

  Future<void> _writeToken(String value) async {
    try {
      await _storage.write(value);
    } catch (_) {
    }
  }

  Future<void> _clearToken() async {
    api.token = null;
    try {
      await _storage.clear();
    } catch (_) {
    }
  }

  void _set(SessionStatus status) {
    _status = status;
    notifyListeners();
  }
}
