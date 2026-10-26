import 'package:flutter/foundation.dart';

import '../api/api_client.dart';
import '../api/api_exception.dart';
import '../models/models.dart';
import 'token_store.dart';

/// Where the app is in the sign-in flow.
enum SessionStatus {
  /// Restoring a stored session at startup.
  restoring,
  signedOut,

  /// Signed in but the Server requires a password change before anything else
  /// (spec.md section 17.1).
  passwordChangeRequired,
  signedIn,
}

/// Holds the signed-in user and the session token.
///
/// Client-side role checks drive what is shown; the Server remains the
/// authority (client-spec section 4).
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

  /// Restore a stored session, if the Server still accepts it.
  Future<void> restore() async {
    String? stored;
    try {
      stored = await _storage.read();
    } catch (_) {
      // Storage unavailable on this platform or locked; sign in again.
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
      // Expired or revoked: discard it rather than showing errors later.
      await _clearToken();
      _set(SessionStatus.signedOut);
    } on NetworkException {
      // The Server is unreachable, which is not the same as being signed out.
      // Keep the token and let the user retry.
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

  /// Change the password. The Server revokes the session, so this signs out.
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
      // Signing out locally matters more than telling the Server.
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
      // A session that cannot be persisted still works for this run.
    }
  }

  Future<void> _clearToken() async {
    api.token = null;
    try {
      await _storage.clear();
    } catch (_) {
      // Nothing to clean up.
    }
  }

  void _set(SessionStatus status) {
    _status = status;
    notifyListeners();
  }
}
