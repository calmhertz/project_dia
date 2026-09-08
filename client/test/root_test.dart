import 'package:aagasa_client/main.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support.dart';

/// An initialized station signed in as [role].
FakeServer configuredAs(String role) {
  final server = FakeServer();
  server.responses['api/auth/me'] = userJson(username: role, role: role);
  server.responses['api/setup'] = {
    'initialized': true,
    'initialized_at': '2026-08-01T00:00:00Z',
  };
  server.responses['api/satellites'] = {
    'satellites': [satelliteJson()],
  };
  server.responses['api/passes'] = {'passes': []};
  server.responses['api/workers'] = {
    'workers': [
      {
        'name': 'worker-1',
        'connection_state': 'online',
        'in_sync': true,
        'pending_uploads': 0,
        'pending_reports': 0,
      },
    ],
    'offline_after_seconds': 15,
  };
  server.responses['api/station'] = {
    'name': 'SJCIT Ground Station',
    'latitude_degrees': 13.394944,
    'longitude_degrees': 77.729444,
    'altitude_m': 915.0,
    'timezone': 'Asia/Kolkata',
    'active_rf_band': 'vhf',
    'configured_bands': ['vhf', 'uhf'],
    'antenna_descriptions': ['Turnstile', 'Yagi'],
  };
  server.responses['api/scheduling-config'] = {
    'minimum_lead_time_seconds': 1800,
    'pre_pass_buffer_seconds': 120,
    'post_pass_buffer_seconds': 120,
    'recording_pre_roll_seconds': 10,
    'recording_post_roll_seconds': 10,
    'minimum_elevation_degrees': 10.0,
    'effective_from': '2026-08-01T00:00:00Z',
  };
  server.responses['api/station/config'] = {
    'station_id': 'station-1',
    'name': 'SJCIT Ground Station',
    'latitude_degrees': 13.394944,
    'longitude_degrees': 77.729444,
    'altitude_m': 915.0,
    'timezone': 'Asia/Kolkata',
    'active_rf_band': 'vhf',
    'bands': [
      {
        'band': 'vhf',
        'antenna_description': 'Turnstile',
        'center_frequency_hz': 137100000,
        'sample_rate_hz': 2048000,
        'gain_db': 40.0,
        'ppm_correction': 0,
        'bias_tee_enabled': false,
      },
      {
        'band': 'uhf',
        'antenna_description': 'Yagi',
        'center_frequency_hz': 435000000,
        'sample_rate_hz': 2048000,
        'gain_db': 40.0,
        'ppm_correction': 0,
        'bias_tee_enabled': false,
      },
    ],
    'hardware': {
      'rotator_serial_port': '/dev/ttyUSB0',
      'rotator_baud_rate': 9600,
      'rotator_park_azimuth_degrees': 0,
      'rotator_park_elevation_degrees': 0,
      'sdr_device_identifier': 'rtlsdr',
    },
    'upcoming_passes': 0,
  };
  server.responses['api/users'] = {
    'users': [
      userJson(id: 'user-1', username: 'root', role: 'root'),
      userJson(id: 'user-2', username: 'operator'),
    ],
  };
  server.responses['api/audit'] = {'records': [], 'limit': 100};
  server.responses['api/tle-status'] = {
    'satellites': [],
    'stale_after_hours': 72,
    'missing_count': 0,
    'stale_count': 0,
  };
  return server;
}

Future<void> openAs(WidgetTester tester, FakeServer server) async {
  tester.view.physicalSize = const Size(2000, 2400);
  tester.view.devicePixelRatio = 1.0;
  addTearDown(tester.view.reset);

  final built = buildSession(server, existingToken: 'test-token');
  await tester.pumpWidget(AagasaApp(api: built.api, session: built.session));
  await tester.pumpAndSettle();
}

Future<void> openRootTab(
  WidgetTester tester,
  FakeServer server,
  String tab,
) async {
  await openAs(tester, server);
  await tester.tap(find.text('Administration').last);
  await tester.pumpAndSettle();
  await tester.tap(find.text(tab));
  await tester.pumpAndSettle();
}

void main() {
  // First-run setup --------------------------------------------------------

  // spec.md section 17.1: before setup the Server refuses almost everything.
  // Root is sent to do it; nobody else is left guessing.
  testWidgets('root is sent to first-run setup', (tester) async {
    final server = configuredAs('root');
    server.responses['api/setup'] = {'initialized': false};
    await openAs(tester, server);

    expect(find.text('Set up this station'), findsOneWidget);
    expect(find.text('Complete setup'), findsOneWidget);
  });

  testWidgets('a normal user is told who has to act', (tester) async {
    final server = configuredAs('user');
    server.responses['api/setup'] = {'initialized': false};
    await openAs(tester, server);

    expect(find.text('This station is not set up yet'), findsOneWidget);
    expect(find.text('Complete setup'), findsNothing);
  });

  testWidgets('an initialized station goes straight to the app', (
    tester,
  ) async {
    await openAs(tester, configuredAs('root'));

    expect(find.text('Set up this station'), findsNothing);
    expect(find.textContaining('Welcome back'), findsOneWidget);
  });

  testWidgets('setup posts the whole configuration in one request', (
    tester,
  ) async {
    final server = configuredAs('root');
    server.responses['api/setup'] = {'initialized': false};
    await openAs(tester, server);

    await tester.enterText(
      find.widgetWithText(TextFormField, 'Station name'),
      'Test Station',
    );
    await tester.enterText(
      find.widgetWithText(TextFormField, 'Latitude'),
      '13.4',
    );
    await tester.enterText(
      find.widgetWithText(TextFormField, 'Longitude'),
      '77.7',
    );

    // Setup then reports as done, which is what the Server would say.
    server.responses['api/setup'] = {
      'initialized': true,
      'initialized_at': '2026-08-25T10:00:00Z',
    };
    await tester.tap(find.text('Complete setup'));
    await tester.pumpAndSettle();

    expect(
      server.seen.any((entry) => entry.startsWith('POST api/setup')),
      isTrue,
    );
    expect(find.text('Set up this station'), findsNothing);
  });

  // The G-550's limits are real hardware limits (spec.md section 9).
  testWidgets('setup refuses an impossible park angle', (tester) async {
    final server = configuredAs('root');
    server.responses['api/setup'] = {'initialized': false};
    await openAs(tester, server);

    await tester.enterText(
      find.widgetWithText(TextFormField, 'Station name'),
      'Test Station',
    );
    await tester.enterText(
      find.widgetWithText(TextFormField, 'Latitude'),
      '13.4',
    );
    await tester.enterText(
      find.widgetWithText(TextFormField, 'Longitude'),
      '77.7',
    );
    await tester.enterText(
      find.widgetWithText(TextFormField, 'Park elevation (0-90)'),
      '120',
    );

    await tester.tap(find.text('Complete setup'));
    await tester.pumpAndSettle();

    expect(find.text('Between 0 and 90'), findsOneWidget);
    expect(
      server.seen.any((entry) => entry.startsWith('POST api/setup')),
      isFalse,
    );
  });

  testWidgets('a refused setup keeps the form so it can be corrected', (
    tester,
  ) async {
    final server = configuredAs('root');
    server.responses['api/setup'] = {'initialized': false};
    server.failures['POST api/setup'] = (
      409,
      {'error': 'already_initialized', 'message': 'setup has already run'},
    );
    await openAs(tester, server);

    await tester.enterText(
      find.widgetWithText(TextFormField, 'Station name'),
      'Test Station',
    );
    await tester.enterText(
      find.widgetWithText(TextFormField, 'Latitude'),
      '13.4',
    );
    await tester.enterText(
      find.widgetWithText(TextFormField, 'Longitude'),
      '77.7',
    );
    await tester.tap(find.text('Complete setup'));
    await tester.pumpAndSettle();

    expect(find.text('setup has already run'), findsOneWidget);
    expect(find.text('Complete setup'), findsOneWidget);
  });

  // Who sees the root surface ----------------------------------------------

  // client-spec section 30: an Admin must not see Root-only controls.
  testWidgets('an admin gets no configuration, accounts or audit tabs', (
    tester,
  ) async {
    await openAs(tester, configuredAs('admin'));
    await tester.tap(find.text('Administration').last);
    await tester.pumpAndSettle();

    expect(find.text('Approvals'), findsOneWidget);
    expect(find.text('Configuration'), findsNothing);
    expect(find.text('Accounts'), findsNothing);
    expect(find.text('Audit'), findsNothing);
  });

  testWidgets('root gets all of them', (tester) async {
    await openAs(tester, configuredAs('root'));
    await tester.tap(find.text('Administration').last);
    await tester.pumpAndSettle();

    expect(find.text('Configuration'), findsOneWidget);
    expect(find.text('Accounts'), findsOneWidget);
    expect(find.text('Audit'), findsOneWidget);
  });

  // Configuration ----------------------------------------------------------

  testWidgets('the configuration screen shows the whole station', (
    tester,
  ) async {
    await openRootTab(tester, configuredAs('root'), 'Configuration');

    expect(find.text('Station'), findsWidgets);
    expect(find.text('VHF antenna (active)'), findsOneWidget);
    expect(find.text('UHF antenna'), findsOneWidget);
    expect(find.text('Rotator and receiver'), findsOneWidget);
    expect(find.text('Scheduling rules'), findsOneWidget);
    // The hardware an Admin never sees is here, because Root owns it.
    expect(find.text('/dev/ttyUSB0'), findsOneWidget);
    expect(find.text('rtlsdr'), findsOneWidget);
  });

  // client-spec section 29: Root is warned when scheduled work exists.
  testWidgets('scheduled work is flagged before anything is changed', (
    tester,
  ) async {
    final server = configuredAs('root');
    final config = Map<String, dynamic>.from(
      server.responses['api/station/config']!,
    );
    config['upcoming_passes'] = 2;
    server.responses['api/station/config'] = config;

    await openRootTab(tester, server, 'Configuration');

    expect(
      find.textContaining('2 passes are already scheduled'),
      findsOneWidget,
    );
    expect(
      find.textContaining('keep the settings they were accepted under'),
      findsOneWidget,
    );
  });

  testWidgets('saving the station reports what it left alone', (tester) async {
    final server = configuredAs('root');
    server.responses['api/station'] = {
      ...server.responses['api/station']!,
      'upcoming_passes_unchanged': 3,
    };
    await openRootTab(tester, server, 'Configuration');

    await tester.enterText(
      find.widgetWithText(TextField, 'Name'),
      'Renamed Station',
    );
    await tester.tap(find.text('Save').first);
    await tester.pumpAndSettle();

    expect(
      server.seen.any((entry) => entry.startsWith('PATCH api/station')),
      isTrue,
    );
    expect(
      find.textContaining('3 scheduled passes kept the previous settings'),
      findsOneWidget,
    );
  });

  // An out-of-range park angle must never reach the Server, let alone the
  // rotator (spec.md section 9).
  testWidgets('an impossible park angle is refused locally', (tester) async {
    final server = configuredAs('root');
    await openRootTab(tester, server, 'Configuration');

    await tester.enterText(
      find.widgetWithText(TextField, 'Park azimuth (0-359)'),
      '400',
    );
    await tester.tap(find.widgetWithText(FilledButton, 'Save').at(3));
    await tester.pumpAndSettle();

    expect(
      find.text('Park azimuth must be between 0 and 359 degrees.'),
      findsOneWidget,
    );
    expect(
      server.seen.any((entry) => entry.contains('station/hardware')),
      isFalse,
    );
  });

  testWidgets('a new scheduling version reports when it takes effect', (
    tester,
  ) async {
    final server = configuredAs('root');
    server.responses['api/scheduling-config'] = {
      ...server.responses['api/scheduling-config']!,
    };
    await openRootTab(tester, server, 'Configuration');

    // The PUT answers with the effective time the Server chose.
    server.responses['api/scheduling-config'] = {
      ...server.responses['api/scheduling-config']!,
      'effective_from': '2026-08-25T10:10:00Z',
    };

    await tester.enterText(
      find.widgetWithText(TextField, 'Minimum lead time (seconds)'),
      '600',
    );
    await tester.tap(find.widgetWithText(FilledButton, 'Save').last);
    await tester.pumpAndSettle();

    expect(find.textContaining('In force from'), findsOneWidget);
  });

  // Accounts ---------------------------------------------------------------

  testWidgets('root can promote, and root itself is protected', (tester) async {
    final server = configuredAs('root');
    await openRootTab(tester, server, 'Accounts');

    // The signed-in root account and the root role are both untouchable.
    expect(find.text('Protected'), findsWidgets);

    await tester.tap(
      find.descendant(
        of: find.byType(Card),
        matching: find.byType(PopupMenuButton<String>),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('Promote to admin'), findsOneWidget);
    // Nothing offers promotion to root (spec.md section 17.1).
    expect(find.text('Promote to root'), findsNothing);

    server.responses['api/users/user-2'] = userJson(
      id: 'user-2',
      role: 'admin',
    );
    await tester.tap(find.text('Promote to admin'));
    await tester.pumpAndSettle();

    expect(
      server.seen.any((entry) => entry.startsWith('PATCH api/users/')),
      isTrue,
    );
  });

  testWidgets('a new account needs a long enough initial password', (
    tester,
  ) async {
    final server = configuredAs('root');
    await openRootTab(tester, server, 'Accounts');

    await tester.tap(find.text('New account'));
    await tester.pumpAndSettle();

    await tester.enterText(
      find.widgetWithText(TextFormField, 'Username'),
      'newoperator',
    );
    await tester.enterText(
      find.widgetWithText(TextFormField, 'Initial password'),
      'short',
    );
    await tester.tap(find.text('Create'));
    await tester.pumpAndSettle();

    expect(find.text('Use at least 12 characters'), findsOneWidget);
    expect(
      server.seen.any((entry) => entry.startsWith('POST api/users')),
      isFalse,
    );
  });

  testWidgets('deleting an account asks first', (tester) async {
    final server = configuredAs('root');
    await openRootTab(tester, server, 'Accounts');

    await tester.tap(
      find.descendant(
        of: find.byType(Card),
        matching: find.byType(PopupMenuButton<String>),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('Delete account'));
    await tester.pumpAndSettle();

    expect(
      find.textContaining('audit trail keeps what they did'),
      findsOneWidget,
    );

    await tester.tap(find.text('Keep'));
    await tester.pumpAndSettle();
    expect(server.seen.any((entry) => entry.startsWith('DELETE')), isFalse);
  });

  // Audit ------------------------------------------------------------------

  testWidgets('the audit trail reads in words, not wire names', (tester) async {
    final server = configuredAs('root');
    server.responses['api/audit'] = {
      'records': [
        {
          'id': 2,
          'action': 'pass.cancelled_by_root_override',
          'entity_type': 'pass',
          'entity_id': 'pass-1',
          'actor_user_id': 'user-1',
          'created_at': '2026-08-25T09:00:00Z',
        },
        {
          'id': 1,
          'action': 'auth.login_failed',
          'entity_type': 'user',
          'entity_id': 'user-1',
          'created_at': '2026-08-25T08:00:00Z',
        },
      ],
      'limit': 100,
    };
    await openRootTab(tester, server, 'Audit');

    expect(find.text('Pass cancelled by root override'), findsWidgets);
    expect(find.text('Failed sign-in'), findsOneWidget);
    // The actor is named where the account is known; user-1 is root here.
    expect(find.textContaining('root'), findsWidgets);
  });

  testWidgets('an audit filter narrows the request', (tester) async {
    final server = configuredAs('root');
    await openRootTab(tester, server, 'Audit');

    await tester.tap(find.widgetWithText(FilterChip, 'Root overrides'));
    await tester.pumpAndSettle();

    expect(
      server.seen.any(
        (entry) => entry.contains('action=pass.cancelled_by_root_override'),
      ),
      isTrue,
    );
  });

  testWidgets('an empty audit trail is a state, not an error', (tester) async {
    await openRootTab(tester, configuredAs('root'), 'Audit');

    expect(find.text('Nothing recorded'), findsOneWidget);
  });

  catalogueTests();
  frequencyTests();

  // An account with passes cannot be deleted, because the passes outlive it.
  testWidgets('a refused account deletion explains why', (tester) async {
    final server = configuredAs('root');
    server.failures['DELETE api/users/user-2'] = (
      409,
      {
        'error': 'in_use',
        'message':
            'other records still reference this, so it cannot be removed',
      },
    );
    await openRootTab(tester, server, 'Accounts');

    await tester.tap(
      find.descendant(
        of: find.byType(Card),
        matching: find.byType(PopupMenuButton<String>),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('Delete account'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, 'Delete'));
    await tester.pumpAndSettle();

    expect(find.textContaining('has passes on record'), findsOneWidget);
  });
}

// The centre frequency ------------------------------------------------------
//
// A band without one produces no pass plan, so approval fails long after
// setup looked successful. That is what a live station hit.

void frequencyTests() {
  testWidgets('setup will not complete without a centre frequency', (
    tester,
  ) async {
    final server = configuredAs('root');
    server.responses['api/setup'] = {'initialized': false};
    await openAs(tester, server);

    await tester.enterText(
      find.widgetWithText(TextFormField, 'Station name'),
      'Test Station',
    );
    await tester.enterText(
      find.widgetWithText(TextFormField, 'Latitude'),
      '13.4',
    );
    await tester.enterText(
      find.widgetWithText(TextFormField, 'Longitude'),
      '77.7',
    );
    await tester.enterText(
      find.widgetWithText(TextFormField, 'VHF centre frequency (Hz)'),
      '',
    );
    await tester.tap(find.text('Complete setup'));
    await tester.pumpAndSettle();

    expect(find.text('Enter the frequency in hertz'), findsOneWidget);
    expect(
      server.seen.any((entry) => entry.startsWith('POST api/setup')),
      isFalse,
    );
  });

  testWidgets('a band missing its frequency says why it matters', (
    tester,
  ) async {
    final server = configuredAs('root');
    final config = Map<String, dynamic>.from(
      server.responses['api/station/config']!,
    );
    config['bands'] = [
      {
        'band': 'vhf',
        'antenna_description': 'Turnstile',
        'sample_rate_hz': 2048000,
        'ppm_correction': 0,
        'bias_tee_enabled': false,
      },
    ];
    server.responses['api/station/config'] = config;
    await openRootTab(tester, server, 'Configuration');

    expect(
      find.textContaining('passes on it cannot be approved'),
      findsOneWidget,
    );
  });

  testWidgets('the frequency can be set from the configuration screen', (
    tester,
  ) async {
    final server = configuredAs('root');
    server.responses['PUT api/station/bands/vhf'] = {
      'band': {
        'band': 'vhf',
        'antenna_description': 'Turnstile',
        'center_frequency_hz': 137100000,
        'ppm_correction': 0,
        'bias_tee_enabled': false,
      },
    };
    await openRootTab(tester, server, 'Configuration');

    expect(
      find.widgetWithText(TextField, 'Centre frequency (Hz)'),
      findsNWidgets(2),
    );

    await tester.enterText(
      find.widgetWithText(TextField, 'Centre frequency (Hz)').first,
      '137620000',
    );
    await tester.tap(find.widgetWithText(FilledButton, 'Save').at(1));
    await tester.pumpAndSettle();

    expect(
      server.seen.any((entry) => entry.startsWith('PUT api/station/bands/vhf')),
      isTrue,
    );
  });
}

// Catalogue administration ---------------------------------------------------

void catalogueTests() {
  // spec.md section 17.1: changing the catalogue is Root's.
  testWidgets('only root is offered a way to add a satellite', (tester) async {
    for (final entry in {'user': false, 'admin': false, 'root': true}.entries) {
      // Unmount first: re-pumping AagasaApp alone would reuse the existing
      // provider and keep the previous role signed in.
      await tester.pumpWidget(const SizedBox.shrink());
      final server = configuredAs(entry.key);
      await openAs(tester, server);
      await tester.tap(find.text('Satellites').last);
      await tester.pumpAndSettle();

      expect(
        find.text('Add satellite'),
        entry.value ? findsOneWidget : findsNothing,
        reason: entry.key,
      );
    }
  });

  testWidgets('an empty catalogue invites root to fill it', (tester) async {
    final server = configuredAs('root');
    server.responses['api/satellites'] = {'satellites': []};
    await openAs(tester, server);
    await tester.tap(find.text('Satellites').last);
    await tester.pumpAndSettle();

    expect(
      find.textContaining('Add one by its NORAD catalog number'),
      findsOneWidget,
    );
  });

  testWidgets('adding a satellite posts the catalog number', (tester) async {
    final server = configuredAs('root');
    server.responses['POST api/satellites'] = satelliteJson(
      name: 'NOAA 19',
      noradId: 33591,
    );
    await openAs(tester, server);
    await tester.tap(find.text('Satellites').last);
    await tester.pumpAndSettle();

    await tester.tap(find.text('Add satellite'));
    await tester.pumpAndSettle();
    await tester.enterText(
      find.widgetWithText(TextFormField, 'NORAD catalog number'),
      '33591',
    );
    await tester.tap(find.widgetWithText(FilledButton, 'Add'));
    await tester.pumpAndSettle();

    expect(
      server.seen.any((entry) => entry.startsWith('POST api/satellites')),
      isTrue,
    );
  });

  testWidgets('a catalog number needs to be a number', (tester) async {
    final server = configuredAs('root');
    await openAs(tester, server);
    await tester.tap(find.text('Satellites').last);
    await tester.pumpAndSettle();

    await tester.tap(find.text('Add satellite'));
    await tester.pumpAndSettle();
    await tester.enterText(
      find.widgetWithText(TextFormField, 'NORAD catalog number'),
      'the space station',
    );
    await tester.tap(find.widgetWithText(FilledButton, 'Add'));
    await tester.pumpAndSettle();

    expect(find.text('Enter a catalog number'), findsOneWidget);
    expect(
      server.seen.any((entry) => entry.startsWith('POST api/satellites')),
      isFalse,
    );
  });

  // A satellite the providers do not know cannot be tracked, so the Server
  // stores nothing. The wording has to say which of the two it is.
  testWidgets('an unknown catalog number is explained', (tester) async {
    final server = configuredAs('root');
    server.failures['POST api/satellites'] = (
      502,
      {'error': 'no_orbital_data', 'message': 'no orbital data available'},
    );
    await openAs(tester, server);
    await tester.tap(find.text('Satellites').last);
    await tester.pumpAndSettle();

    await tester.tap(find.text('Add satellite'));
    await tester.pumpAndSettle();
    await tester.enterText(
      find.widgetWithText(TextFormField, 'NORAD catalog number'),
      '999999',
    );
    await tester.tap(find.widgetWithText(FilledButton, 'Add'));
    await tester.pumpAndSettle();

    expect(
      find.textContaining('No orbital data provider has this catalog number'),
      findsOneWidget,
    );
  });

  testWidgets('root can edit and close a satellite for scheduling', (
    tester,
  ) async {
    final server = configuredAs('root');
    server.responses['api/satellites/sat-1'] = {
      'satellite': satelliteJson(),
      'current_tle': {
        'epoch': '2026-08-24T12:00:00Z',
        'source': 'celestrak',
        'fetched_at': '2026-08-25T00:00:00Z',
      },
    };
    server.responses['api/satellites/sat-1/passes'] = {
      'passes': [],
      'station_active_band': 'vhf',
      'minimum_elevation_degrees': 10.0,
    };
    server.responses['PATCH api/satellites/sat-1'] = satelliteJson(
      schedulable: false,
    );
    await openAs(tester, server);
    await tester.tap(find.text('Satellites').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('ISS (ZARYA)'));
    await tester.pumpAndSettle();

    await tester.tap(find.byType(PopupMenuButton<String>).last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Edit'));
    await tester.pumpAndSettle();

    await tester.tap(find.byType(SwitchListTile));
    await tester.tap(find.widgetWithText(FilledButton, 'Save'));
    await tester.pumpAndSettle();

    expect(
      server.seen.any((entry) => entry.startsWith('PATCH api/satellites/')),
      isTrue,
    );
  });

  // spec.md section 13: history is never quietly dropped, so a satellite
  // with passes cannot be removed. The refusal has to suggest the way out.
  testWidgets('removing a satellite with passes suggests closing it instead', (
    tester,
  ) async {
    final server = configuredAs('root');
    server.responses['api/satellites/sat-1'] = {'satellite': satelliteJson()};
    server.responses['api/satellites/sat-1/passes'] = {
      'passes': [],
      'station_active_band': 'vhf',
      'minimum_elevation_degrees': 10.0,
    };
    server.failures['DELETE api/satellites/sat-1'] = (
      409,
      {'error': 'conflict', 'message': 'already exists'},
    );
    await openAs(tester, server);
    await tester.tap(find.text('Satellites').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('ISS (ZARYA)'));
    await tester.pumpAndSettle();

    await tester.tap(find.byType(PopupMenuButton<String>).last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Remove from catalogue'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, 'Remove'));
    await tester.pumpAndSettle();

    expect(
      find.textContaining('Close it for scheduling instead'),
      findsOneWidget,
    );
  });

  // There is no self-registration anywhere in the specs; the login screen
  // says so rather than leaving someone hunting for a sign-up link.
  testWidgets('the login screen says how accounts are made', (tester) async {
    final built = buildSession(FakeServer());
    await tester.pumpWidget(AagasaApp(api: built.api, session: built.session));
    await tester.pumpAndSettle();

    expect(
      find.text('Accounts are created by a station administrator.'),
      findsOneWidget,
    );
    expect(find.textContaining('Sign up'), findsNothing);
    expect(find.textContaining('Create account'), findsNothing);
  });
}
