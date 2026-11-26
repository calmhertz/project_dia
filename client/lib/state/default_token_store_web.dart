import 'package:web/web.dart' as web;

import 'token_store.dart';

TokenStore defaultTokenStore() => const WebTokenStore();

class WebTokenStore implements TokenStore {
  const WebTokenStore();

  static const _key = 'aagasa.session.token';

  @override
  Future<String?> read() async => web.window.localStorage.getItem(_key);

  @override
  Future<void> write(String token) async =>
      web.window.localStorage.setItem(_key, token);

  @override
  Future<void> clear() async => web.window.localStorage.removeItem(_key);
}
