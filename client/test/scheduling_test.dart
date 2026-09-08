import 'package:aagasa_client/main.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support.dart';

/// A signed-in app with a one-satellite catalogue.
FakeServer station() {
  final server = FakeServer();
  server.responses['api/auth/login'] = {
    'token': 'test-token',
    'must_change_password': false,
    'user': userJson(),
  };
  server.responses['api/auth/me'] = userJson();
  server.responses['api/satellites'] = {
    'satellites': [satelliteJson()],
  };
  server.responses['api/satellites/sat-1'] = {
    'satellite': satelliteJson(),
    'current_tle': {
      'epoch': '2026-08-24T12:00:00Z',
      'source': 'celestrak',
      'fetched_at': '2026-08-25T00:00:00Z',
    },
  };
  server.responses['api/satellites/sat-1/passes'] = {
    'passes': [predictedPassJson()],
    'station_active_band': 'vhf',
    'minimum_elevation_degrees': 10.0,
  };
  server.responses['api/passes'] = {'passes': []};
  return server;
}

Future<void> openApp(WidgetTester tester, FakeServer server) async {
  // Tall enough that a whole form or pass page is on screen, so a test taps
  // what a user would see rather than scrolling first.
  tester.view.physicalSize = const Size(1000, 2000);
  tester.view.devicePixelRatio = 1.0;
  addTearDown(tester.view.reset);

  final built = buildSession(server, existingToken: 'test-token');
  await tester.pumpWidget(AagasaApp(api: built.api, session: built.session));
  await tester.pumpAndSettle();
}

Future<void> openSatellite(WidgetTester tester, FakeServer server) async {
  await openApp(tester, server);
  await tester.tap(find.text('Satellites').last);
  await tester.pumpAndSettle();
  await tester.tap(find.text('ISS (ZARYA)'));
  await tester.pumpAndSettle();
}

Future<void> openRequestForm(WidgetTester tester, FakeServer server) async {
  await openSatellite(tester, server);
  await tester.tap(find.textContaining('max 31'));
  await tester.pumpAndSettle();
}

Future<void> openPass(WidgetTester tester, FakeServer server) async {
  await openApp(tester, server);
  await tester.tap(find.text('My passes').last);
  await tester.pumpAndSettle();
  await tester.tap(find.text('ISS (ZARYA)').first);
  await tester.pumpAndSettle();
}

void main() {
  // Choosing a pass --------------------------------------------------------

  testWidgets('a satellite shows its upcoming passes', (tester) async {
    await openSatellite(tester, station());

    expect(find.textContaining('max 31'), findsOneWidget);
  });

  testWidgets('a window with no passes says so rather than showing nothing', (
    tester,
  ) async {
    final server = station();
    server.responses['api/satellites/sat-1/passes'] = {
      'passes': [],
      'station_active_band': 'vhf',
      'minimum_elevation_degrees': 10.0,
    };
    await openSatellite(tester, server);

    expect(find.text('No passes in the next 48 hours'), findsOneWidget);
  });

  // A non-schedulable satellite must not offer a request that would be
  // refused (client-spec section 12).
  testWidgets('a non-schedulable satellite offers no request', (tester) async {
    final server = station();
    server.responses['api/satellites'] = {
      'satellites': [satelliteJson(schedulable: false)],
    };
    server.responses['api/satellites/sat-1'] = {
      'satellite': satelliteJson(schedulable: false),
    };
    await openSatellite(tester, server);

    expect(find.text('Unavailable'), findsWidgets);

    await tester.tap(find.textContaining('max 31'));
    await tester.pumpAndSettle();
    expect(find.text('Request this pass'), findsNothing);
  });

  testWidgets('choosing a pass opens the request form', (tester) async {
    await openRequestForm(tester, station());

    expect(find.text('Request this pass'), findsOneWidget);
    expect(find.text('Raw recording'), findsOneWidget);
    expect(find.text('Share publicly'), findsOneWidget);
  });

  // Requesting -------------------------------------------------------------

  testWidgets('an accepted request returns to the satellite', (tester) async {
    final server = station();
    server.responses['api/passes'] = {
      'pass': passJson(status: 'pending_approval'),
      'warnings': [],
    };
    await openRequestForm(
      tester,
      station()..responses.addAll(server.responses),
    );

    await tester.tap(find.text('Request pass'));
    await tester.pumpAndSettle();

    expect(find.text('Request this pass'), findsNothing);
  });

  // client-spec section 15: a conflict says the slot is taken and nothing
  // about who holds it.
  testWidgets('a station conflict is explained without naming the holder', (
    tester,
  ) async {
    final server = station();
    server.failures['api/passes'] = (
      409,
      {
        'error': 'station_conflict',
        'message': 'the station is already reserved for this time',
        'details': {
          'occupied_from': '2026-08-26T09:58:00Z',
          'occupied_to': '2026-08-26T10:12:00Z',
        },
      },
    );
    await openRequestForm(tester, server);

    await tester.tap(find.text('Request pass'));
    await tester.pumpAndSettle();

    expect(find.text('This time is already occupied.'), findsOneWidget);
    expect(find.textContaining('user-1'), findsNothing);
    expect(find.textContaining('Requested by'), findsNothing);
  });

  testWidgets('an offline station is explained in its own terms', (
    tester,
  ) async {
    final server = station();
    server.failures['api/passes'] = (
      409,
      {'error': 'worker_offline', 'message': 'the ground station is offline'},
    );
    await openRequestForm(tester, server);

    await tester.tap(find.text('Request pass'));
    await tester.pumpAndSettle();

    expect(find.text('The ground station is offline.'), findsOneWidget);
  });

  testWidgets('a lead-time refusal keeps the server wording', (tester) async {
    final server = station();
    server.failures['api/passes'] = (
      409,
      {
        'error': 'lead_time',
        'message': 'this pass starts within the 15 minute lead time',
      },
    );
    await openRequestForm(tester, server);

    await tester.tap(find.text('Request pass'));
    await tester.pumpAndSettle();

    expect(find.text('This pass starts too soon.'), findsOneWidget);
    expect(
      find.textContaining('15 minute lead time'),
      findsOneWidget,
      reason: 'the Server states the actual lead time, not the client',
    );
  });

  // Pass detail ------------------------------------------------------------

  testWidgets('a pass shows its window and status', (tester) async {
    final server = station();
    server.responses['api/passes?scope=mine'] = {
      'passes': [passJson(status: 'approved')],
    };
    server.responses['api/passes/pass-1'] = {'pass': passJson()};
    server.responses['api/passes/pass-1/recordings'] = {'recordings': []};
    await openPass(tester, server);

    expect(find.text('Pass window'), findsOneWidget);
    expect(find.text('VHF'), findsOneWidget);
    expect(find.text('Approved'), findsOneWidget);
  });

  // spec.md section 15: an overridden pass tells its owner what happened.
  testWidgets('a root override is explained to the owner', (tester) async {
    final server = station();
    server.responses['api/passes?scope=mine'] = {
      'passes': [passJson(status: 'cancelled')],
    };
    server.responses['api/passes/pass-1'] = {
      'pass': {
        ...passJson(status: 'cancelled'),
        'cancellation_reason': 'cancelled_by_root_override',
      },
    };
    server.responses['api/passes/pass-1/recordings'] = {'recordings': []};
    await openPass(tester, server);

    expect(find.textContaining('higher-priority operation'), findsOneWidget);
    expect(find.text('Cancel this pass'), findsNothing);
  });

  testWidgets('cancelling asks first', (tester) async {
    final server = station();
    server.responses['api/passes?scope=mine'] = {
      'passes': [passJson(status: 'approved')],
    };
    server.responses['api/passes/pass-1'] = {'pass': passJson()};
    server.responses['api/passes/pass-1/recordings'] = {'recordings': []};
    server.responses['api/passes/pass-1/cancel'] = {
      'pass': passJson(status: 'cancelled'),
    };
    await openPass(tester, server);

    await tester.tap(find.text('Cancel this pass'));
    await tester.pumpAndSettle();
    expect(find.text('Cancel this pass?'), findsOneWidget);

    // Backing out must not cancel anything.
    await tester.tap(find.text('Keep it'));
    await tester.pumpAndSettle();
    expect(server.seen.any((entry) => entry.contains('/cancel')), isFalse);

    await tester.tap(find.text('Cancel this pass'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Cancel pass'));
    await tester.pumpAndSettle();

    expect(server.seen.any((entry) => entry.contains('/cancel')), isTrue);
  });

  // Recordings -------------------------------------------------------------

  testWidgets('recordings are listed and can be downloaded', (tester) async {
    final server = station();
    server.responses['api/passes?scope=mine'] = {
      'passes': [passJson(status: 'completed')],
    };
    server.responses['api/passes/pass-1'] = {
      'pass': passJson(status: 'completed'),
    };
    server.responses['api/passes/pass-1/recordings'] = {
      'recordings': [
        {
          'recording_id': 'rec-1',
          'relative_path': 'iss/raw.wav',
          'size_bytes': 2048,
          'download_url': '/api/recordings/rec-1/download',
          'received_at': '2026-08-26T10:15:00Z',
        },
      ],
    };
    server.responses['api/recordings/rec-1/download'] = {'bytes': 'x'};
    await openPass(tester, server);

    expect(find.text('iss/raw.wav'), findsOneWidget);
    expect(find.textContaining('2.0 KB'), findsOneWidget);

    await tester.tap(find.byIcon(Icons.download));
    await tester.pumpAndSettle();

    expect(find.textContaining('Downloaded'), findsOneWidget);
    expect(
      server.seen.any((entry) => entry.contains('recordings/rec-1/download')),
      isTrue,
    );
  });

  // A completed pass with nothing to show is a real outcome, not an error
  // (client-spec section 20).
  testWidgets('a completed pass with no recordings says so', (tester) async {
    final server = station();
    server.responses['api/passes?scope=mine'] = {
      'passes': [passJson(status: 'completed')],
    };
    server.responses['api/passes/pass-1'] = {
      'pass': passJson(status: 'completed'),
    };
    server.responses['api/passes/pass-1/recordings'] = {'recordings': []};
    await openPass(tester, server);

    expect(find.text('This pass produced no recordings.'), findsOneWidget);
  });

  testWidgets('a scheduled pass says recordings are still to come', (
    tester,
  ) async {
    final server = station();
    server.responses['api/passes?scope=mine'] = {
      'passes': [passJson(status: 'approved')],
    };
    server.responses['api/passes/pass-1'] = {'pass': passJson()};
    server.responses['api/passes/pass-1/recordings'] = {'recordings': []};
    await openPass(tester, server);

    expect(
      find.text('Recordings appear here once the pass has run.'),
      findsOneWidget,
    );
  });

  // A refused recording list must not be mistaken for an empty one.
  testWidgets('a refused recording list is shown as an error', (tester) async {
    final server = station();
    server.responses['api/passes?scope=mine'] = {
      'passes': [passJson(status: 'completed')],
    };
    server.responses['api/passes/pass-1'] = {
      'pass': passJson(status: 'completed'),
    };
    server.failures['api/passes/pass-1/recordings'] = (
      403,
      {'error': 'forbidden', 'message': 'not permitted'},
    );
    await openPass(tester, server);

    expect(find.text('Try again'), findsOneWidget);
    expect(find.text('This pass produced no recordings.'), findsNothing);
  });
}
