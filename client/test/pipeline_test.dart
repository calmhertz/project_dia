import 'package:aagasa_client/main.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support.dart';

/// SatDump pipeline selection and upload (spec.md section 19.1,
/// client-spec sections 11 and 12).

FakeServer stationWithPipelines(
  String role, {
  List<Map<String, dynamic>>? pipelines,
}) {
  final server = FakeServer();
  server.responses['api/auth/me'] = userJson(username: role, role: role);
  server.responses['api/setup'] = {'initialized': true};
  server.responses['api/satellites'] = {
    'satellites': [satelliteJson()],
  };
  server.responses['api/satellites/sat-1'] = {'satellite': satelliteJson()};
  server.responses['api/satellites/sat-1/passes'] = {
    'passes': [predictedPassJson()],
    'station_active_band': 'vhf',
    'minimum_elevation_degrees': 10.0,
  };
  server.responses['api/passes'] = {'passes': []};
  server.responses['api/workers'] = {
    'workers': [],
    'offline_after_seconds': 15,
  };
  server.responses['api/pipelines'] = {
    'pipelines':
        pipelines ??
        [
          {'id': 'pipe-1', 'name': 'noaa_apt', 'is_custom': false},
          {'id': 'pipe-2', 'name': 'meteor_m2-x_lrpt', 'is_custom': false},
          {'id': 'pipe-3', 'name': 'My APT', 'is_custom': true},
        ],
    'reported_by_station': (pipelines ?? const [{}]).isNotEmpty,
  };
  return server;
}

Future<void> openRequestForm(WidgetTester tester, FakeServer server) async {
  tester.view.physicalSize = const Size(1400, 2400);
  tester.view.devicePixelRatio = 1.0;
  addTearDown(tester.view.reset);

  final built = buildSession(server, existingToken: 'test-token');
  await tester.pumpWidget(AagasaApp(api: built.api, session: built.session));
  await tester.pumpAndSettle();

  await tester.tap(find.text('Satellites').last);
  await tester.pumpAndSettle();
  await tester.tap(find.text('ISS (ZARYA)'));
  await tester.pumpAndSettle();
  await tester.tap(find.textContaining('max 31'));
  await tester.pumpAndSettle();
}

void main() {
  // Choosing a pipeline -----------------------------------------------------

  testWidgets('a raw recording asks for no pipeline', (tester) async {
    await openRequestForm(tester, stationWithPipelines('user'));

    expect(find.text('Pipeline'), findsNothing);
    // Raw is the default, so the request can be submitted straight away.
    final button = tester.widget<FilledButton>(
      find.widgetWithText(FilledButton, 'Request pass'),
    );
    expect(button.onPressed, isNotNull);
  });

  testWidgets('choosing a decode asks which pipeline', (tester) async {
    await openRequestForm(tester, stationWithPipelines('user'));

    await tester.tap(find.text('Processed result'));
    await tester.pumpAndSettle();

    expect(find.text('Pipeline'), findsOneWidget);
    // Nothing chosen yet, so the request cannot be submitted.
    final button = tester.widget<FilledButton>(
      find.widgetWithText(FilledButton, 'Request pass'),
    );
    expect(button.onPressed, isNull);
  });

  testWidgets('the station\'s own pipelines are offered, custom ones marked', (
    tester,
  ) async {
    await openRequestForm(tester, stationWithPipelines('user'));

    await tester.tap(find.text('Processed result'));
    await tester.pumpAndSettle();
    await tester.tap(find.byType(DropdownButtonFormField<String>));
    await tester.pumpAndSettle();

    expect(find.text('noaa_apt').last, findsOneWidget);
    expect(find.text('My APT (custom)').last, findsOneWidget);
  });

  testWidgets('a chosen pipeline is sent with the request', (tester) async {
    final server = stationWithPipelines('user');
    server.responses['POST api/passes'] = {
      'pass': passJson(status: 'pending_approval'),
      'warnings': [],
    };
    await openRequestForm(tester, server);

    await tester.tap(find.text('Processed result'));
    await tester.pumpAndSettle();
    await tester.tap(find.byType(DropdownButtonFormField<String>));
    await tester.pumpAndSettle();
    await tester.tap(find.text('noaa_apt').last);
    await tester.pumpAndSettle();

    await tester.tap(find.text('Request pass'));
    await tester.pumpAndSettle();

    expect(
      server.seen.any((entry) => entry.startsWith('POST api/passes')),
      isTrue,
    );
    // Back on the satellite page: the request was accepted.
    expect(find.text('Request this pass'), findsNothing);
  });

  // A station that has never connected has reported nothing, which is not the
  // same as a station with no pipelines.
  testWidgets('an unreported installation is explained', (tester) async {
    final server = stationWithPipelines('user', pipelines: []);
    server.responses['api/pipelines'] = {
      'pipelines': [],
      'reported_by_station': false,
    };
    await openRequestForm(tester, server);

    await tester.tap(find.text('Processed result'));
    await tester.pumpAndSettle();

    expect(
      find.textContaining('has not reported its SatDump installation'),
      findsOneWidget,
    );
    final button = tester.widget<FilledButton>(
      find.widgetWithText(FilledButton, 'Request pass'),
    );
    expect(button.onPressed, isNull);
  });

  // Uploading ---------------------------------------------------------------

  testWidgets('a normal user is not offered the pipelines tab', (tester) async {
    final server = stationWithPipelines('user');
    tester.view.physicalSize = const Size(1400, 2400);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.reset);

    final built = buildSession(server, existingToken: 'test-token');
    await tester.pumpWidget(AagasaApp(api: built.api, session: built.session));
    await tester.pumpAndSettle();

    expect(find.text('Administration'), findsNothing);
  });

  testWidgets('an admin can paste a custom definition', (tester) async {
    final server = stationWithPipelines('admin');
    server.responses['api/station'] = {
      'name': 'Test',
      'latitude_degrees': 0.0,
      'longitude_degrees': 0.0,
      'altitude_m': 0.0,
      'timezone': 'UTC',
      'active_rf_band': 'vhf',
      'configured_bands': ['vhf'],
      'antenna_descriptions': ['Turnstile'],
    };
    server.responses['POST api/pipelines'] = {
      'id': 'pipe-9',
      'name': 'My Custom',
      'is_custom': true,
      'checksum_sha256': 'a' * 64,
    };
    tester.view.physicalSize = const Size(1600, 2400);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.reset);

    final built = buildSession(server, existingToken: 'test-token');
    await tester.pumpWidget(AagasaApp(api: built.api, session: built.session));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Administration').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Pipelines'));
    await tester.pumpAndSettle();

    // Standard and uploaded are told apart: the heading, and the one custom
    // entry's own subtitle.
    expect(find.text('From the station'), findsOneWidget);
    expect(find.text('Uploaded'), findsNWidgets(2));
    expect(find.text('Standard'), findsNWidgets(2));

    await tester.tap(find.text('Add custom pipeline'));
    await tester.pumpAndSettle();
    await tester.enterText(find.widgetWithText(TextField, 'Name'), 'My Custom');
    await tester.enterText(
      find.widgetWithText(TextField, 'Pipeline JSON'),
      '{"my_custom": {"name": "My Custom"}}',
    );
    await tester.tap(find.widgetWithText(FilledButton, 'Upload'));
    await tester.pumpAndSettle();

    expect(
      server.seen.any((entry) => entry.startsWith('POST api/pipelines')),
      isTrue,
    );
  });

  // Malformed JSON is caught before a pointless round trip; the message says
  // what is wrong rather than waiting for the Server to say it.
  testWidgets('obvious nonsense is caught before sending', (tester) async {
    final server = stationWithPipelines('admin');
    server.responses['api/station'] = {
      'name': 'Test',
      'latitude_degrees': 0.0,
      'longitude_degrees': 0.0,
      'altitude_m': 0.0,
      'timezone': 'UTC',
      'active_rf_band': 'vhf',
      'configured_bands': ['vhf'],
      'antenna_descriptions': ['Turnstile'],
    };
    tester.view.physicalSize = const Size(1600, 2400);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.reset);

    final built = buildSession(server, existingToken: 'test-token');
    await tester.pumpWidget(AagasaApp(api: built.api, session: built.session));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Administration').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Pipelines'));
    await tester.pumpAndSettle();

    await tester.tap(find.text('Add custom pipeline'));
    await tester.pumpAndSettle();
    await tester.enterText(find.widgetWithText(TextField, 'Name'), 'Broken');
    await tester.enterText(
      find.widgetWithText(TextField, 'Pipeline JSON'),
      '{ not json',
    );
    await tester.tap(find.widgetWithText(FilledButton, 'Upload'));
    await tester.pumpAndSettle();

    expect(find.textContaining('That is not valid JSON'), findsOneWidget);
    // Nothing was sent: the client knew this could not succeed.
    expect(
      server.seen.any((entry) => entry.startsWith('POST api/pipelines')),
      isFalse,
    );
  });

  // Structure the client cannot judge is the Server's call, and its refusal
  // is shown as it stands.
  testWidgets('a refused definition shows the server reason', (tester) async {
    final server = stationWithPipelines('admin');
    server.responses['api/station'] = {
      'name': 'Test',
      'latitude_degrees': 0.0,
      'longitude_degrees': 0.0,
      'altitude_m': 0.0,
      'timezone': 'UTC',
      'active_rf_band': 'vhf',
      'configured_bands': ['vhf'],
      'antenna_descriptions': ['Turnstile'],
    };
    server.failures['POST api/pipelines'] = (
      400,
      {
        'error': 'invalid_pipeline',
        'message': 'the definition names no pipeline',
      },
    );
    tester.view.physicalSize = const Size(1600, 2400);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.reset);

    final built = buildSession(server, existingToken: 'test-token');
    await tester.pumpWidget(AagasaApp(api: built.api, session: built.session));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Administration').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Pipelines'));
    await tester.pumpAndSettle();

    await tester.tap(find.text('Add custom pipeline'));
    await tester.pumpAndSettle();
    await tester.enterText(
      find.widgetWithText(TextField, 'Name'),
      'Wrong shape',
    );
    // Valid JSON, but not a pipeline file: only the Server can say so.
    await tester.enterText(
      find.widgetWithText(TextField, 'Pipeline JSON'),
      '["noaa_apt"]',
    );
    await tester.tap(find.widgetWithText(FilledButton, 'Upload'));
    await tester.pumpAndSettle();

    expect(find.text('the definition names no pipeline'), findsOneWidget);
  });

  // client-spec section 12: uploading a custom JSON is offered wherever a
  // pipeline is chosen, not hidden in administration.
  testWidgets('the request form offers a custom upload', (tester) async {
    await openRequestForm(tester, stationWithPipelines('user'));

    await tester.tap(find.text('Processed result'));
    await tester.pumpAndSettle();

    expect(find.text('Upload pipeline JSON'), findsOneWidget);
  });

  testWidgets('a station with no pipelines still offers an upload', (
    tester,
  ) async {
    final server = stationWithPipelines('user', pipelines: []);
    server.responses['api/pipelines'] = {
      'pipelines': [],
      'reported_by_station': false,
    };
    await openRequestForm(tester, server);

    await tester.tap(find.text('Processed result'));
    await tester.pumpAndSettle();

    expect(find.text('Upload pipeline JSON'), findsOneWidget);
  });

  recordingsTests();
}

// The recordings destination (client-spec sections 5 and 20).

void recordingsTests() {
  FakeServer stationWithRecordings(String role) {
    final server = stationWithPipelines(role);
    server.responses['api/recordings?scope=mine'] = {
      'recordings': [
        {
          'recording_id': 'rec-1',
          'pass_id': 'pass-1',
          'satellite_id': 'sat-1',
          'relative_path': 'iss/raw.wav',
          'size_bytes': 2048,
          'download_url': '/api/recordings/rec-1/download',
          'received_at': '2026-08-26T10:15:00Z',
          'pass_aos': '2026-08-26T10:00:00Z',
          'visibility': 'private',
        },
      ],
    };
    server.responses['api/recordings?scope=public'] = {'recordings': []};
    return server;
  }

  Future<void> openRecordings(WidgetTester tester, FakeServer server) async {
    tester.view.physicalSize = const Size(1400, 2400);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.reset);

    final built = buildSession(server, existingToken: 'test-token');
    await tester.pumpWidget(AagasaApp(api: built.api, session: built.session));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Recordings').last);
    await tester.pumpAndSettle();
  }

  testWidgets('recordings are reachable from the navigation', (tester) async {
    await openRecordings(tester, stationWithRecordings('user'));

    expect(find.text('ISS (ZARYA)'), findsOneWidget);
    expect(find.textContaining('2.0 KB'), findsOneWidget);
    expect(find.textContaining('iss/raw.wav'), findsOneWidget);
  });

  testWidgets('an empty list says which scope is empty', (tester) async {
    final server = stationWithRecordings('user');
    server.responses['api/recordings?scope=mine'] = {'recordings': []};
    await openRecordings(tester, server);

    expect(find.text('Nothing recorded yet'), findsOneWidget);
    expect(find.textContaining('Recordings from your passes'), findsOneWidget);
  });

  testWidgets('the public scope is requested separately', (tester) async {
    final server = stationWithRecordings('user');
    await openRecordings(tester, server);

    await tester.tap(find.widgetWithText(ChoiceChip, 'Public'));
    await tester.pumpAndSettle();

    expect(
      server.seen.any((entry) => entry.contains('recordings?scope=public')),
      isTrue,
    );
    expect(find.textContaining('public passes'), findsOneWidget);
  });

  // Only Admin and Root may ask for everything; the Server refuses anyone
  // else, and the client does not offer it.
  testWidgets('a normal user is not offered every recording', (tester) async {
    await openRecordings(tester, stationWithRecordings('user'));

    expect(find.widgetWithText(ChoiceChip, 'All'), findsNothing);
  });

  testWidgets('an admin is offered every recording', (tester) async {
    final server = stationWithRecordings('admin');
    server.responses['api/recordings?scope=all'] = {'recordings': []};
    await openRecordings(tester, server);

    expect(find.widgetWithText(ChoiceChip, 'All'), findsOneWidget);
  });
}
