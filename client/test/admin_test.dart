import 'package:aagasa_client/main.dart';
import 'package:aagasa_client/widgets/formatting.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support.dart';

/// A signed-in app for a given role.
FakeServer stationAs(String role) {
  final server = FakeServer();
  server.responses['api/auth/login'] = {
    'token': 'test-token',
    'must_change_password': false,
    'user': userJson(username: role, role: role),
  };
  server.responses['api/auth/me'] = userJson(username: role, role: role);
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
        'last_seen_at': '2026-08-25T09:59:58Z',
        'seconds_since_seen': 2.4,
      },
    ],
    'heartbeat_interval_seconds': 5,
    'offline_after_seconds': 15,
  };
  server.responses['api/station'] = {
    'station_id': 'station-1',
    'name': 'SJCIT Ground Station',
    'latitude_degrees': 13.394944,
    'longitude_degrees': 77.729444,
    'altitude_m': 915.0,
    'timezone': 'Asia/Kolkata',
    'active_rf_band': 'vhf',
    'initialized_at': '2026-08-01T00:00:00Z',
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
    'created_at': '2026-08-01T00:00:00Z',
  };
  server.responses['api/tle-status'] = {
    'satellites': [
      {
        'satellite_id': 'sat-1',
        'norad_id': 25544,
        'name': 'ISS (ZARYA)',
        'has_orbital_data': true,
        'epoch': '2026-08-25T04:00:00Z',
        'source': 'celestrak',
        'fetched_at': '2026-08-25T05:00:00Z',
        'age_hours': 6.0,
        'stale': false,
      },
    ],
    'stale_after_hours': 72,
    'missing_count': 0,
    'stale_count': 0,
  };
  server.responses['api/users'] = {
    'users': [userJson(username: 'operator')],
  };
  return server;
}

Future<void> openAs(WidgetTester tester, FakeServer server) async {
  tester.view.physicalSize = const Size(1200, 2000);
  tester.view.devicePixelRatio = 1.0;
  addTearDown(tester.view.reset);

  final built = buildSession(server, existingToken: 'test-token');
  await tester.pumpWidget(AagasaApp(api: built.api, session: built.session));
  await tester.pumpAndSettle();
}

Future<void> openAdminTab(
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
  // Who sees administration ------------------------------------------------

  // client-spec section 4: the client hides what a role cannot use, and the
  // Server refuses it regardless.
  testWidgets('a normal user is not offered administration', (tester) async {
    await openAs(tester, stationAs('user'));

    expect(find.text('Administration'), findsNothing);
    expect(find.text('Dashboard'), findsWidgets);
  });

  testWidgets('an admin is offered administration', (tester) async {
    await openAs(tester, stationAs('admin'));

    expect(find.text('Administration'), findsWidgets);
  });

  testWidgets('root is offered administration too', (tester) async {
    await openAs(tester, stationAs('root'));

    expect(find.text('Administration'), findsWidgets);
  });

  // A normal user's dashboard must not turn into an operations console.
  testWidgets('a normal user sees no operational summary', (tester) async {
    await openAs(tester, stationAs('user'));

    expect(find.text('Ground station online'), findsNothing);
    expect(
      stationAs('user').seen.any((entry) => entry.contains('workers')),
      isFalse,
    );
  });

  testWidgets('an admin dashboard reports the station and the queue', (
    tester,
  ) async {
    final server = stationAs('admin');
    server.responses['api/passes?scope=all&status=pending_approval'] = {
      'passes': [passJson(status: 'pending_approval')],
    };
    await openAs(tester, server);

    expect(find.text('Ground station online'), findsOneWidget);
    expect(find.text('1 request waiting for approval'), findsOneWidget);
  });

  testWidgets('an offline station is stated plainly on the dashboard', (
    tester,
  ) async {
    final server = stationAs('admin');
    server.responses['api/workers'] = {
      'workers': [
        {
          'name': 'worker-1',
          'connection_state': 'offline',
          'in_sync': false,
          'pending_uploads': 2,
          'pending_reports': 1,
        },
      ],
      'offline_after_seconds': 15,
    };
    server.responses['api/passes?scope=all&status=pending_approval'] = {
      'passes': [],
    };
    await openAs(tester, server);

    expect(find.text('Ground station offline'), findsOneWidget);
    expect(find.text('Nothing waiting for approval'), findsOneWidget);
  });

  // Approval queue ---------------------------------------------------------

  testWidgets('the queue shows what a decision needs', (tester) async {
    final server = stationAs('admin');
    server.responses['api/passes?scope=all&status=pending_approval'] = {
      'passes': [passJson(status: 'pending_approval', visibility: 'public')],
    };
    await openAdminTab(tester, server, 'Approvals');

    expect(find.text('ISS (ZARYA)'), findsOneWidget);
    expect(find.text('VHF'), findsOneWidget);
    expect(find.text('Raw recording'), findsOneWidget);
    expect(find.text('Public'), findsOneWidget);
    // The requester is named from the account list, not left as an id.
    expect(find.text('operator'), findsOneWidget);
    expect(find.text('Approve'), findsOneWidget);
    expect(find.text('Reject'), findsOneWidget);
  });

  testWidgets('an empty queue says so', (tester) async {
    final server = stationAs('admin');
    server.responses['api/passes?scope=all&status=pending_approval'] = {
      'passes': [],
    };
    await openAdminTab(tester, server, 'Approvals');

    expect(find.text('Nothing waiting'), findsOneWidget);
  });

  testWidgets('approving calls the server and refreshes the queue', (
    tester,
  ) async {
    final server = stationAs('admin');
    server.responses['api/passes?scope=all&status=pending_approval'] = {
      'passes': [passJson(status: 'pending_approval')],
    };
    server.responses['api/passes/pass-1/approve'] = {
      'pass': passJson(status: 'approved'),
    };
    await openAdminTab(tester, server, 'Approvals');

    await tester.tap(find.text('Approve'));
    await tester.pumpAndSettle();

    expect(
      server.seen.any((entry) => entry.contains('pass-1/approve')),
      isTrue,
    );
  });

  testWidgets('rejecting calls the reject endpoint, not approve', (
    tester,
  ) async {
    final server = stationAs('admin');
    server.responses['api/passes?scope=all&status=pending_approval'] = {
      'passes': [passJson(status: 'pending_approval')],
    };
    server.responses['api/passes/pass-1/reject'] = {
      'pass': passJson(status: 'rejected'),
    };
    await openAdminTab(tester, server, 'Approvals');

    await tester.tap(find.text('Reject'));
    await tester.pumpAndSettle();

    expect(server.seen.any((entry) => entry.contains('pass-1/reject')), isTrue);
    expect(
      server.seen.any((entry) => entry.contains('pass-1/approve')),
      isFalse,
    );
  });

  // A decision the Server refuses must not look like it succeeded.
  testWidgets('a refused decision is reported', (tester) async {
    final server = stationAs('admin');
    server.responses['api/passes?scope=all&status=pending_approval'] = {
      'passes': [passJson(status: 'pending_approval')],
    };
    server.failures['api/passes/pass-1/approve'] = (
      409,
      {'error': 'conflict', 'message': 'this pass is no longer pending'},
    );
    await openAdminTab(tester, server, 'Approvals');

    await tester.tap(find.text('Approve'));
    await tester.pumpAndSettle();

    expect(find.text('this pass is no longer pending'), findsOneWidget);
  });

  // Station and rules ------------------------------------------------------

  testWidgets('the station tab reports operation, not configuration', (
    tester,
  ) async {
    await openAdminTab(tester, stationAs('admin'), 'Station');

    expect(find.text('SJCIT Ground Station'), findsOneWidget);
    expect(find.text('VHF active'), findsOneWidget);
    expect(find.text('Online'), findsOneWidget);
    expect(find.text('Up to date'), findsOneWidget);

    // Hardware settings belong to the Root surface in V15.
    expect(find.textContaining('/dev/tty'), findsNothing);
    expect(find.textContaining('rtlsdr'), findsNothing);
    // No internal infrastructure is named (client-spec section 4).
    for (final word in ['gRPC', 'Redis', 'Mongo', 'Postgres', 'container']) {
      expect(find.textContaining(word), findsNothing, reason: word);
    }
  });

  testWidgets('an admin sees the scheduling rules but no way to change them', (
    tester,
  ) async {
    await openAdminTab(tester, stationAs('admin'), 'Station');

    expect(find.text('Minimum lead time'), findsOneWidget);
    expect(find.text('Only Root can change these.'), findsOneWidget);
    expect(find.text('Save'), findsNothing);
  });

  testWidgets('a backlog is shown while the station is away', (tester) async {
    final server = stationAs('admin');
    server.responses['api/workers'] = {
      'workers': [
        {
          'name': 'worker-1',
          'connection_state': 'offline',
          'in_sync': false,
          'pending_uploads': 3,
          'pending_reports': 2,
        },
      ],
      'offline_after_seconds': 15,
    };
    await openAdminTab(tester, server, 'Station');

    expect(find.text('Offline'), findsOneWidget);
    expect(find.text('3 recordings, 2 results'), findsOneWidget);
    expect(find.textContaining('cannot be scheduled'), findsOneWidget);
  });

  // Orbital data -----------------------------------------------------------

  testWidgets('current orbital data is summarized', (tester) async {
    await openAdminTab(tester, stationAs('admin'), 'Orbital data');

    expect(
      find.text('All 1 satellites have current orbital data.'),
      findsOneWidget,
    );
    expect(find.text('Refresh all'), findsOneWidget);
  });

  // Stale data and absent data are different problems and must read
  // differently.
  testWidgets('stale and missing orbital data are distinguished', (
    tester,
  ) async {
    final server = stationAs('admin');
    server.responses['api/tle-status'] = {
      'satellites': [
        {
          'satellite_id': 'sat-1',
          'norad_id': 25544,
          'name': 'ISS (ZARYA)',
          'has_orbital_data': true,
          'epoch': '2026-08-01T00:00:00Z',
          'source': 'celestrak',
          'age_hours': 600.0,
          'stale': true,
        },
        {
          'satellite_id': 'sat-2',
          'norad_id': 99999,
          'name': 'NEW-SAT',
          'has_orbital_data': false,
          'stale': false,
        },
      ],
      'stale_after_hours': 72,
      'missing_count': 1,
      'stale_count': 1,
    };
    await openAdminTab(tester, server, 'Orbital data');

    expect(find.textContaining('older than 72 hours'), findsWidgets);
    expect(find.textContaining('No orbital data yet'), findsOneWidget);
    expect(
      find.text(
        'Of 2 satellites: 1 older than 72 hours, 1 with no data at all.',
      ),
      findsOneWidget,
    );
  });

  // A provider outage keeps the previous data, and the wording has to say so
  // rather than reporting a plain failure (spec.md section 11.1).
  testWidgets('a refresh reports what it kept', (tester) async {
    final server = stationAs('admin');
    server.responses['api/satellites/refresh-tles'] = {
      'updated': 2,
      'kept_last_known_good': 1,
      'failed': 1,
      'results': [],
    };
    await openAdminTab(tester, server, 'Orbital data');

    await tester.tap(find.text('Refresh all'));
    await tester.pumpAndSettle();

    expect(find.textContaining('1 kept their last known good'), findsOneWidget);
  });

  // History ----------------------------------------------------------------

  testWidgets('history lists every pass on the station', (tester) async {
    final server = stationAs('admin');
    server.responses['api/passes?scope=all'] = {
      'passes': [
        passJson(status: 'completed'),
        passJson(id: 'pass-2', status: 'failed'),
      ],
    };
    await openAdminTab(tester, server, 'History');

    // Two rows, each carrying its own outcome. The same words also appear as
    // filter chips, so the rows are counted rather than the words.
    expect(find.widgetWithText(Card, 'ISS (ZARYA)'), findsNWidgets(2));
    expect(find.byType(StatusChip), findsNWidgets(2));
  });

  testWidgets('a history filter narrows the request', (tester) async {
    final server = stationAs('admin');
    server.responses['api/passes?scope=all'] = {'passes': []};
    server.responses['api/passes?scope=all&status=failed'] = {
      'passes': [passJson(status: 'failed')],
    };
    await openAdminTab(tester, server, 'History');

    await tester.tap(find.widgetWithText(FilterChip, 'Failed'));
    await tester.pumpAndSettle();

    expect(
      server.seen.any((entry) => entry.contains('scope=all&status=failed')),
      isTrue,
    );
    expect(find.byType(StatusChip), findsOneWidget);
  });

  // Pass review ------------------------------------------------------------

  testWidgets('an admin can decide from the pass page', (tester) async {
    final server = stationAs('admin');
    server.responses['api/passes?scope=all'] = {
      'passes': [passJson(status: 'pending_approval')],
    };
    server.responses['api/passes/pass-1'] = {
      'pass': passJson(status: 'pending_approval'),
    };
    server.responses['api/passes/pass-1/recordings'] = {'recordings': []};
    server.responses['api/passes/pass-1/approve'] = {
      'pass': passJson(status: 'approved'),
    };
    await openAdminTab(tester, server, 'History');

    await tester.tap(find.text('ISS (ZARYA)').first);
    await tester.pumpAndSettle();

    expect(find.text('Decision'), findsOneWidget);
    expect(find.text('Requested by'), findsOneWidget);

    await tester.tap(find.widgetWithText(FilledButton, 'Approve'));
    await tester.pumpAndSettle();

    expect(
      server.seen.any((entry) => entry.contains('pass-1/approve')),
      isTrue,
    );
  });

  // The same page for a normal user must offer no decision at all.
  testWidgets('a normal user gets no decision controls', (tester) async {
    final server = stationAs('user');
    server.responses['api/passes?scope=mine'] = {
      'passes': [passJson(status: 'pending_approval')],
    };
    server.responses['api/passes/pass-1'] = {
      'pass': passJson(status: 'pending_approval'),
    };
    server.responses['api/passes/pass-1/recordings'] = {'recordings': []};
    await openAs(tester, server);

    await tester.tap(find.text('My passes').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('ISS (ZARYA)').first);
    await tester.pumpAndSettle();

    expect(find.text('Decision'), findsNothing);
    expect(find.text('Approve'), findsNothing);
    expect(find.text('Cancel this pass'), findsOneWidget);
  });
}
