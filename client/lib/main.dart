import 'dart:ui';

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import 'api/api_client.dart';
import 'app/first_run_gate.dart';
import 'screens/change_password_screen.dart';
import 'screens/login_screen.dart';
import 'state/session.dart';
import 'theme/app_theme.dart';
import 'widgets/states.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  _dropUnmappableKeyEvents();
  runApp(AagasaApp(api: ApiClient()));
}

/// Discards key events the engine could not map to both a physical and a
/// logical key.
///
/// On the web a browser sometimes reports a key with only one of the two set,
/// most visibly when a password manager fills the sign-in form. Flutter's
/// `KeyEventManager` asserts that both are present and the app dies on the
/// assertion in a debug build. The event carries no usable key either way, so
/// dropping it loses nothing; everything else is passed straight through.
void _dropUnmappableKeyEvents() {
  final dispatcher = PlatformDispatcher.instance;
  final delegate = dispatcher.onKeyData;
  if (delegate == null) return;

  dispatcher.onKeyData = (KeyData data) {
    if (data.physical == 0 || data.logical == 0) return false;
    return delegate(data);
  };
}

class AagasaApp extends StatelessWidget {
  const AagasaApp({super.key, required this.api, this.session});

  final ApiClient api;

  /// Supplied by tests; built from [api] otherwise.
  final Session? session;

  @override
  Widget build(BuildContext context) {
    return ChangeNotifierProvider<Session>(
      create: (_) => (session ?? Session(api: api))..restore(),
      child: MaterialApp(
        title: 'Aagasa',
        debugShowCheckedModeBanner: false,
        // Dark only in V1; no light theme is supplied.
        themeMode: ThemeMode.dark,
        darkTheme: AppTheme.dark(),
        home: const _Root(),
      ),
    );
  }
}

/// Chooses the screen for the current session state.
class _Root extends StatelessWidget {
  const _Root();

  @override
  Widget build(BuildContext context) {
    final status = context.select<Session, SessionStatus>((s) => s.status);

    return switch (status) {
      SessionStatus.restoring => const Scaffold(
        body: LoadingState(message: 'Starting Aagasa'),
      ),
      SessionStatus.signedOut => const LoginScreen(),
      // The Server refuses everything else until this is done, so the app
      // shows nothing else either (spec.md section 17.1).
      SessionStatus.passwordChangeRequired => const ChangePasswordScreen(),
      SessionStatus.signedIn => const FirstRunGate(),
    };
  }
}
