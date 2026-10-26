import 'package:flutter_secure_storage/flutter_secure_storage.dart';

/// Where the session token lives.
///
/// An interface the app owns, so tests do not depend on a platform plugin's
/// signatures and the storage mechanism can change without touching Session.
abstract class TokenStore {
  Future<String?> read();
  Future<void> write(String token);
  Future<void> clear();
}

/// Platform-backed storage: Keychain, Keystore, or WebCrypto-wrapped storage
/// depending on the target (client-spec section 6).
class SecureTokenStore implements TokenStore {
  const SecureTokenStore([this._storage = const FlutterSecureStorage()]);

  static const _key = 'aagasa.session.token';

  final FlutterSecureStorage _storage;

  @override
  Future<String?> read() => _storage.read(key: _key);

  @override
  Future<void> write(String token) => _storage.write(key: _key, value: token);

  @override
  Future<void> clear() => _storage.delete(key: _key);
}

/// Storage that survives only the current run. Used by tests, and as the
/// fallback when the platform store is unavailable.
class InMemoryTokenStore implements TokenStore {
  String? _token;

  @override
  Future<String?> read() async => _token;

  @override
  Future<void> write(String token) async => _token = token;

  @override
  Future<void> clear() async => _token = null;
}
