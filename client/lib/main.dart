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

  final Session? session;

  @override
  Widget build(BuildContext context) {
    return ChangeNotifierProvider<Session>(
      create: (_) => (session ?? Session(api: api))..restore(),
      child: MaterialApp(
        title: 'Aagasa',
        debugShowCheckedModeBanner: false,
        themeMode: ThemeMode.dark,
        darkTheme: AppTheme.dark(),
        home: const _Root(),
      ),
    );
  }
}

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
      SessionStatus.passwordChangeRequired => const ChangePasswordScreen(),
      SessionStatus.signedIn => const FirstRunGate(),
    };
  }
}
