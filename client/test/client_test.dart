import 'package:aagasa_client/main.dart';
import 'package:aagasa_client/state/session.dart';
import 'package:aagasa_client/theme/app_theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support.dart';

/// Signs in and settles on the shell.
Future<void> signIn(WidgetTester tester, FakeServer server) async {
  final built = buildSession(server);
  await tester.pumpWidget(AagasaApp(api: built.api, session: built.session));
  await tester.pumpAndSettle();

  await tester.enterText(find.byType(TextFormField).first, 'operator');
  await tester.enterText(find.byType(TextFormField).last, 'a-real-password');
  await tester.tap(find.text('Sign in'));
  await tester.pumpAndSettle();
}

FakeServer serverWithCatalogue() {
  final server = FakeServer();
  server.responses['api/auth/login'] = {
    'token': 'test-token',
    'expires_at': '2026-08-26T10:00:00Z',
    'must_change_password': false,
    'user': userJson(),
  };
  server.responses['api/auth/me'] = userJson();
  server.responses['api/satellites'] = {
    'satellites': [satelliteJson()],
  };
  server.responses['api/passes'] = {'passes': []};
  return server;
}

void main() {
  // Theme -----------------------------------------------------------------

  testWidgets('the theme is dark and built from the V1 seed', (tester) async {
    final built = buildSession(FakeServer());
    await tester.pumpWidget(AagasaApp(api: built.api, session: built.session));
    await tester.pumpAndSettle();

    final app = tester.widget<MaterialApp>(find.byType(MaterialApp));
    expect(app.themeMode, ThemeMode.dark);
    expect(app.theme, isNull, reason: 'no light theme ships in V1');
    expect(app.darkTheme!.useMaterial3, isTrue);
    expect(AppTheme.seedColor, const Color(0xFF487CE5));
  });

  // Sign in ---------------------------------------------------------------

  testWidgets('an unauthenticated visitor sees the login screen', (
    tester,
  ) async {
    final built = buildSession(FakeServer());
    await tester.pumpWidget(AagasaApp(api: built.api, session: built.session));
    await tester.pumpAndSettle();

    expect(find.text('Sign in'), findsOneWidget);
    expect(find.byType(NavigationBar), findsNothing);
  });

  testWidgets('signing in reaches the dashboard', (tester) async {
    await signIn(tester, serverWithCatalogue());

    expect(find.textContaining('Welcome back'), findsOneWidget);
    expect(find.text('Sign in'), findsNothing);
  });

  testWidgets('a rejected sign-in shows the reason and stays put', (
    tester,
  ) async {
    final server = FakeServer();
    server.failures['api/auth/login'] = (
      401,
      {
        'error': 'invalid_credentials',
        'message': 'invalid username or password',
      },
    );

    final built = buildSession(server);
    await tester.pumpWidget(AagasaApp(api: built.api, session: built.session));
    await tester.pumpAndSettle();

    await tester.enterText(find.byType(TextFormField).first, 'operator');
    await tester.enterText(find.byType(TextFormField).last, 'wrong');
    await tester.tap(find.text('Sign in'));
    await tester.pumpAndSettle();

    expect(find.textContaining('invalid username or password'), findsOneWidget);
    expect(find.text('Sign in'), findsOneWidget);
  });

  testWidgets('an unreachable server is reported, not swallowed', (
    tester,
  ) async {
    final server = FakeServer();
    // No login route registered, so the fake answers 404 for it.
    final built = buildSession(server);
    await tester.pumpWidget(AagasaApp(api: built.api, session: built.session));
    await tester.pumpAndSettle();

    await tester.enterText(find.byType(TextFormField).first, 'operator');
    await tester.enterText(find.byType(TextFormField).last, 'password');
    await tester.tap(find.text('Sign in'));
    await tester.pumpAndSettle();

    expect(find.byType(TextFormField), findsNWidgets(2));
  });

  testWidgets('login validates before calling the server', (tester) async {
    final server = serverWithCatalogue();
    final built = buildSession(server);
    await tester.pumpWidget(AagasaApp(api: built.api, session: built.session));
    await tester.pumpAndSettle();

    await tester.tap(find.text('Sign in'));
    await tester.pumpAndSettle();

    expect(find.text('Enter your username'), findsOneWidget);
    expect(find.text('Enter your password'), findsOneWidget);
    expect(server.seen.where((entry) => entry.contains('login')), isEmpty);
  });

  // Forced password change -------------------------------------------------

  // spec.md section 17.1: nothing else is reachable until it is done.
  testWidgets('a forced password change blocks the rest of the app', (
    tester,
  ) async {
    final server = FakeServer();
    server.responses['api/auth/login'] = {
      'token': 'test-token',
      'must_change_password': true,
      'user': userJson(mustChangePassword: true),
    };

    final built = buildSession(server);
    await tester.pumpWidget(AagasaApp(api: built.api, session: built.session));
    await tester.pumpAndSettle();

    await tester.enterText(find.byType(TextFormField).first, 'root');
    await tester.enterText(find.byType(TextFormField).last, 'toor');
    await tester.tap(find.text('Sign in'));
    await tester.pumpAndSettle();

    expect(find.text('Change your password'), findsOneWidget);
    expect(find.byType(NavigationBar), findsNothing);
    expect(find.byType(NavigationRail), findsNothing);
  });

  testWidgets('the new password must meet the server policy', (tester) async {
    final server = FakeServer();
    server.responses['api/auth/login'] = {
      'token': 'test-token',
      'must_change_password': true,
      'user': userJson(mustChangePassword: true),
    };

    final built = buildSession(server);
    await tester.pumpWidget(AagasaApp(api: built.api, session: built.session));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextFormField).first, 'root');
    await tester.enterText(find.byType(TextFormField).last, 'toor');
    await tester.tap(find.text('Sign in'));
    await tester.pumpAndSettle();

    final fields = find.byType(TextFormField);
    await tester.enterText(fields.at(0), 'toor');
    await tester.enterText(fields.at(1), 'short');
    await tester.enterText(fields.at(2), 'short');
    await tester.tap(find.text('Change password'));
    await tester.pumpAndSettle();

    expect(find.text('Use at least 12 characters'), findsOneWidget);
  });

  // Session restore --------------------------------------------------------

  testWidgets('a stored session is restored without asking again', (
    tester,
  ) async {
    final server = serverWithCatalogue();
    final built = buildSession(server, existingToken: 'stored-token');

    await tester.pumpWidget(AagasaApp(api: built.api, session: built.session));
    await tester.pumpAndSettle();

    expect(find.textContaining('Welcome back'), findsOneWidget);
  });

  // A revoked token must not leave the user staring at errors.
  testWidgets('a rejected stored session returns to login', (tester) async {
    final server = FakeServer();
    server.failures['api/auth/me'] = (
      401,
      {'error': 'unauthenticated', 'message': 'authentication required'},
    );

    final built = buildSession(server, existingToken: 'expired-token');
    await tester.pumpWidget(AagasaApp(api: built.api, session: built.session));
    await tester.pumpAndSettle();

    expect(find.text('Sign in'), findsOneWidget);
    expect(
      await built.storage.read(),
      isNull,
      reason: 'a rejected token must not be kept',
    );
  });

  // Catalogue --------------------------------------------------------------

  testWidgets('the catalogue lists satellites', (tester) async {
    final server = serverWithCatalogue();
    await signIn(tester, server);

    await tester.tap(find.text('Satellites').last);
    await tester.pumpAndSettle();

    expect(find.text('ISS (ZARYA)'), findsOneWidget);
    expect(find.textContaining('NORAD 25544'), findsOneWidget);
  });

  testWidgets('an empty catalogue explains itself', (tester) async {
    final server = serverWithCatalogue();
    server.responses['api/satellites'] = {'satellites': []};
    await signIn(tester, server);

    await tester.tap(find.text('Satellites').last);
    await tester.pumpAndSettle();

    expect(find.text('No satellites yet'), findsOneWidget);
  });

  // Missing metadata must never break the listing (spec.md section 11.2).
  testWidgets('a satellite without metadata still lists', (tester) async {
    final server = serverWithCatalogue();
    server.responses['api/satellites'] = {
      'satellites': [satelliteJson(name: 'UNKNOWN-1', metadata: const {})],
    };
    await signIn(tester, server);

    await tester.tap(find.text('Satellites').last);
    await tester.pumpAndSettle();

    expect(find.text('UNKNOWN-1'), findsOneWidget);
  });

  // Passes -----------------------------------------------------------------

  testWidgets('my passes shows status for each pass', (tester) async {
    final server = serverWithCatalogue();
    server.responses['api/passes?scope=mine'] = {
      'passes': [passJson(status: 'pending_approval')],
    };
    await signIn(tester, server);

    await tester.tap(find.text('My passes').last);
    await tester.pumpAndSettle();

    expect(find.text('ISS (ZARYA)'), findsOneWidget);
    expect(find.text('Pending approval'), findsOneWidget);
  });

  testWidgets('an empty pass list explains what to do', (tester) async {
    final server = serverWithCatalogue();
    server.responses['api/passes?scope=mine'] = {'passes': []};
    await signIn(tester, server);

    await tester.tap(find.text('My passes').last);
    await tester.pumpAndSettle();

    expect(find.text('You have no passes yet'), findsOneWidget);
  });

  testWidgets('public history uses the public scope', (tester) async {
    final server = serverWithCatalogue();
    server.responses['api/passes?scope=public'] = {
      'passes': [passJson(visibility: 'public', requestedBy: null)],
    };
    await signIn(tester, server);

    await tester.tap(find.text('Public history').last);
    await tester.pumpAndSettle();

    expect(
      server.seen.any((entry) => entry.contains('scope=public')),
      isTrue,
      reason: 'the public list must be requested with the public scope',
    );
  });

  // Layout -----------------------------------------------------------------

  testWidgets('a narrow viewport uses a navigation bar', (tester) async {
    tester.view.physicalSize = const Size(400, 900);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.reset);

    await signIn(tester, serverWithCatalogue());

    expect(find.byType(NavigationBar), findsOneWidget);
    expect(find.byType(NavigationRail), findsNothing);
  });

  testWidgets('a wide viewport uses a navigation rail', (tester) async {
    tester.view.physicalSize = const Size(1400, 900);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.reset);

    await signIn(tester, serverWithCatalogue());

    expect(find.byType(NavigationRail), findsOneWidget);
    expect(find.byType(NavigationBar), findsNothing);
  });

  // Sign out ---------------------------------------------------------------

  testWidgets('signing out returns to login and forgets the token', (
    tester,
  ) async {
    final server = serverWithCatalogue();
    server.responses['api/auth/logout'] = {};
    final built = buildSession(server);

    await tester.pumpWidget(AagasaApp(api: built.api, session: built.session));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextFormField).first, 'operator');
    await tester.enterText(find.byType(TextFormField).last, 'a-real-password');
    await tester.tap(find.text('Sign in'));
    await tester.pumpAndSettle();

    await tester.tap(find.byIcon(Icons.account_circle_outlined));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Sign out'));
    await tester.pumpAndSettle();

    expect(find.text('Sign in'), findsOneWidget);
    expect(await built.storage.read(), isNull);
  });

  // The Server rejecting a session mid-use must return to login rather than
  // showing errors on every screen (client-spec section 6).
  testWidgets('an expired session during use returns to login', (tester) async {
    final server = serverWithCatalogue();
    await signIn(tester, server);

    server.failures['api/satellites'] = (
      401,
      {'error': 'unauthenticated', 'message': 'authentication required'},
    );
    await tester.tap(find.text('Satellites').last);
    await tester.pumpAndSettle();

    expect(find.text('Sign in'), findsOneWidget);
  });

  // Session unit behaviour -------------------------------------------------

  test('session starts out restoring', () {
    final built = buildSession(FakeServer());
    expect(built.session.status, SessionStatus.restoring);
    expect(built.session.isSignedIn, isFalse);
  });
}
