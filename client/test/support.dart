import 'dart:convert';

import 'package:aagasa_client/api/api_client.dart';
import 'package:aagasa_client/state/session.dart';
import 'package:aagasa_client/state/token_store.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

/// Routes requests by path so a test states only what it cares about.
class FakeServer {
  FakeServer({this.token = 'test-token'});

  final String token;

  final Map<String, Map<String, dynamic>> responses = {};
  final Map<String, (int, Map<String, dynamic>)> failures = {};
  final List<String> requested = [];

  /// Requests that arrived, so a test can assert what the UI actually asked
  /// the Server for.
  List<String> get seen => requested;

  http.Client client() => MockClient((request) async {
    // Routes are keyed without the leading slash so a test reads the way the
    // API does.
    final path =
        request.url.path.replaceFirst(RegExp(r'^/'), '') +
        (request.url.hasQuery ? '?${request.url.query}' : '');
    requested.add('${request.method} $path');
    // A key may name a method ("POST api/setup") when a test needs one verb
    // to behave differently from another on the same path.
    bool matches(String key) => key.contains(' ')
        ? '${request.method} $path'.startsWith(key)
        : path.startsWith(key);

    // Longest key first, so a route with a query string wins over the bare
    // path it starts with.
    int byLength(String a, String b) => b.length.compareTo(a.length);

    for (final entry
        in (failures.entries.toList()
          ..sort((a, b) => byLength(a.key, b.key)))) {
      if (matches(entry.key)) {
        return http.Response(
          jsonEncode(entry.value.$2),
          entry.value.$1,
          headers: {'content-type': 'application/json'},
        );
      }
    }
    for (final entry
        in (responses.entries.toList()
          ..sort((a, b) => byLength(a.key, b.key)))) {
      if (matches(entry.key)) {
        return http.Response(
          jsonEncode(entry.value),
          200,
          headers: {'content-type': 'application/json'},
        );
      }
    }
    return http.Response(
      jsonEncode({'error': 'not_found', 'message': 'not found'}),
      404,
      headers: {'content-type': 'application/json'},
    );
  });
}

({ApiClient api, Session session, InMemoryTokenStore storage}) buildSession(
  FakeServer server, {
  String? existingToken,
}) {
  final api = ApiClient(
    baseUrl: 'http://server.test',
    httpClient: server.client(),
  );
  final storage = InMemoryTokenStore();
  if (existingToken != null) {
    storage.write(existingToken);
  }
  return (
    api: api,
    session: Session(api: api, storage: storage),
    storage: storage,
  );
}

Map<String, dynamic> userJson({
  String id = 'user-1',
  String username = 'operator',
  String role = 'user',
  bool mustChangePassword = false,
}) => {
  'id': id,
  'username': username,
  'role': role,
  'must_change_password': mustChangePassword,
  'created_at': '2026-08-25T10:00:00Z',
};

Map<String, dynamic> satelliteJson({
  String id = 'sat-1',
  String name = 'ISS (ZARYA)',
  int noradId = 25544,
  bool schedulable = true,
  Map<String, dynamic> metadata = const {
    'status': 'alive',
    'countries': 'RU,US',
  },
}) => {
  'id': id,
  'norad_id': noradId,
  'name': name,
  'description': '',
  'is_schedulable': schedulable,
  'metadata': metadata,
  'created_at': '2026-08-25T10:00:00Z',
};

Map<String, dynamic> passJson({
  String id = 'pass-1',
  String status = 'approved',
  String visibility = 'private',
  String? requestedBy = 'user-1',
  String aos = '2026-08-26T10:00:00Z',
}) => {
  'id': id,
  'satellite_id': 'sat-1',
  'status': status,
  'visibility': visibility,
  'band': 'vhf',
  'aos': aos,
  'los': '2026-08-26T10:10:00Z',
  'max_elevation_degrees': 31.0,
  'reserved_from': aos,
  'reserved_to': '2026-08-26T10:12:00Z',
  'recording_mode': 'raw',
  'recording_pre_roll_seconds': 10,
  'recording_post_roll_seconds': 10,
  // Omitted entirely when null, which is how the Server withholds a pass
  // owner from unrelated users.
  'requested_by': ?requestedBy,
  'created_at': '2026-08-25T10:00:00Z',
};

Map<String, dynamic> predictedPassJson({String aos = '2026-08-26T10:00:00Z'}) =>
    {
      'aos': aos,
      'tca': '2026-08-26T10:05:00Z',
      'los': '2026-08-26T10:10:00Z',
      'aos_azimuth_degrees': 189.7,
      'tca_azimuth_degrees': 130.0,
      'los_azimuth_degrees': 60.9,
      'max_elevation_degrees': 31.0,
      'duration_seconds': 600.0,
    };
