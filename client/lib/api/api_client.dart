import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:http/http.dart' as http;

import '../models/models.dart';
import 'api_exception.dart';

class ApiClient {
  ApiClient({String? baseUrl, http.Client? httpClient})
    : baseUrl = baseUrl ?? defaultBaseUrl,
      _http = httpClient ?? http.Client();

  static String get defaultBaseUrl {
    const configured = String.fromEnvironment('AAGASA_SERVER_URL');
    if (configured.isNotEmpty) return configured;
    if (kIsWeb) return Uri.base.origin;
    return 'http://127.0.0.1:8080';
  }

  static const Duration timeout = Duration(seconds: 20);

  final String baseUrl;
  final http.Client _http;

  String? token;

  void Function()? onSessionExpired;

  void dispose() => _http.close();


  Future<Map<String, dynamic>> _send(
    String method,
    String path, {
    Map<String, dynamic>? body,
    bool authenticated = true,
  }) async {
    final uri = Uri.parse('$baseUrl$path');
    final headers = <String, String>{'Accept': 'application/json'};
    if (body != null) headers['Content-Type'] = 'application/json';
    if (authenticated && token != null) {
      headers['Authorization'] = 'Bearer $token';
    }

    http.Response response;
    try {
      final request = http.Request(method, uri)..headers.addAll(headers);
      if (body != null) request.body = jsonEncode(body);
      final streamed = await _http.send(request).timeout(timeout);
      response = await http.Response.fromStream(streamed);
    } on TimeoutException {
      throw const NetworkException('The server did not respond in time.');
    } catch (_) {
      throw const NetworkException('Cannot reach the server.');
    }

    if (response.statusCode == 204 || response.body.isEmpty) {
      return const {};
    }

    Map<String, dynamic> decoded;
    try {
      decoded = jsonDecode(response.body) as Map<String, dynamic>;
    } catch (_) {
      if (response.statusCode >= 400) {
        throw ApiException(
          statusCode: response.statusCode,
          code: 'unexpected_response',
          message: 'The server returned an unexpected response.',
        );
      }
      return const {};
    }

    if (response.statusCode >= 400) {
      if (response.statusCode == 401) onSessionExpired?.call();
      throw ApiException(
        statusCode: response.statusCode,
        code: decoded['error'] as String? ?? 'error',
        message: decoded['message'] as String? ?? 'Something went wrong.',
        details: decoded,
      );
    }
    return decoded;
  }


  Future<({String token, AuthUser user})> login(
    String username,
    String password,
  ) async {
    final response = await _send(
      'POST',
      '/api/auth/login',
      authenticated: false,
      body: {'username': username, 'password': password},
    );
    return (
      token: response['token'] as String,
      user: AuthUser.fromJson(response['user'] as Map<String, dynamic>),
    );
  }

  Future<AuthUser> currentUser() async {
    return AuthUser.fromJson(await _send('GET', '/api/auth/me'));
  }

  Future<void> changePassword(String current, String next) async {
    await _send(
      'POST',
      '/api/auth/change-password',
      body: {'current_password': current, 'new_password': next},
    );
  }

  Future<void> logout() async {
    await _send('POST', '/api/auth/logout');
  }


  Future<List<Satellite>> satellites() async {
    final response = await _send('GET', '/api/satellites');
    return ((response['satellites'] as List<dynamic>?) ?? const [])
        .map((entry) => Satellite.fromJson(entry as Map<String, dynamic>))
        .toList();
  }

  Future<({Satellite satellite, TleSummary? tle})> satellite(String id) async {
    final response = await _send('GET', '/api/satellites/$id');
    final current = response['current_tle'] as Map<String, dynamic>?;
    return (
      satellite: Satellite.fromJson(
        response['satellite'] as Map<String, dynamic>,
      ),
      tle: current == null ? null : TleSummary.fromJson(current),
    );
  }

  Future<PassPrediction> predictPasses(
    String satelliteId, {
    int hours = 48,
  }) async {
    return PassPrediction.fromJson(
      await _send('GET', '/api/satellites/$satelliteId/passes?hours=$hours'),
    );
  }


  Future<List<Pass>> passes({String scope = 'mine', String? status}) async {
    final query = StringBuffer('?scope=$scope');
    if (status != null) query.write('&status=$status');
    final response = await _send('GET', '/api/passes$query');
    return ((response['passes'] as List<dynamic>?) ?? const [])
        .map((entry) => Pass.fromJson(entry as Map<String, dynamic>))
        .toList();
  }

  Future<Pass> pass(String id) async {
    final response = await _send('GET', '/api/passes/$id');
    return Pass.fromJson(response['pass'] as Map<String, dynamic>);
  }

  Future<ScheduleResult> requestPass({
    required String satelliteId,
    required DateTime aos,
    required String band,
    required String recordingMode,
    required bool isPublic,
    String? pipelineId,
    bool override = false,
  }) async {
    final response = await _send(
      'POST',
      '/api/passes',
      body: {
        'satellite_id': satelliteId,
        'aos': aos.toUtc().toIso8601String(),
        'band': band,
        'recording_mode': recordingMode,
        'visibility': isPublic ? 'public' : 'private',
        'pipeline_id': ?pipelineId,
        if (override) 'override': true,
      },
    );
    return ScheduleResult(
      pass: Pass.fromJson(response['pass'] as Map<String, dynamic>),
      warnings: ((response['warnings'] as List<dynamic>?) ?? const [])
          .map(
            (entry) =>
                SchedulingWarning.fromJson(entry as Map<String, dynamic>),
          )
          .toList(),
      overriddenPassIds:
          ((response['overridden_pass_ids'] as List<dynamic>?) ?? const [])
              .map((entry) => entry as String)
              .toList(),
    );
  }

  Future<Pass> cancelPass(String id) async {
    final response = await _send('POST', '/api/passes/$id/cancel');
    return Pass.fromJson(response['pass'] as Map<String, dynamic>);
  }


  Future<PipelineCatalogue> pipelines() async =>
      PipelineCatalogue.fromJson(await _send('GET', '/api/pipelines'));

  Future<SatDumpPipeline> uploadPipeline({
    required String name,
    required String definition,
  }) async {
    final response = await _send(
      'POST',
      '/api/pipelines',
      body: {'name': name, 'definition': definition},
    );
    return SatDumpPipeline.fromJson(response);
  }


  Future<List<Recording>> recordings({String scope = 'mine'}) async {
    final response = await _send('GET', '/api/recordings?scope=$scope');
    return ((response['recordings'] as List<dynamic>?) ?? const [])
        .map((entry) => Recording.fromJson(entry as Map<String, dynamic>))
        .toList();
  }

  Future<List<Recording>> recordingsForPass(String passId) async {
    final response = await _send('GET', '/api/passes/$passId/recordings');
    return ((response['recordings'] as List<dynamic>?) ?? const [])
        .map((entry) => Recording.fromJson(entry as Map<String, dynamic>))
        .toList();
  }

  Uri downloadUri(Recording recording) =>
      Uri.parse('$baseUrl${recording.downloadUrl}');

  Future<Uint8List> downloadRecording(Recording recording) async {
    final request = http.Request('GET', downloadUri(recording));
    if (token != null) request.headers['Authorization'] = 'Bearer $token';
    try {
      final streamed = await _http
          .send(request)
          .timeout(const Duration(minutes: 10));
      if (streamed.statusCode >= 400) {
        throw ApiException(
          statusCode: streamed.statusCode,
          code: 'download_failed',
          message: 'The recording could not be downloaded.',
        );
      }
      return await streamed.stream.toBytes();
    } on TimeoutException {
      throw const NetworkException('The download timed out.');
    }
  }


  Future<Pass> approvePass(String id) async {
    final response = await _send('POST', '/api/passes/$id/approve');
    return Pass.fromJson(response['pass'] as Map<String, dynamic>);
  }

  Future<Pass> rejectPass(String id) async {
    final response = await _send('POST', '/api/passes/$id/reject');
    return Pass.fromJson(response['pass'] as Map<String, dynamic>);
  }

  Future<List<AuthUser>> users() async {
    final response = await _send('GET', '/api/users');
    return ((response['users'] as List<dynamic>?) ?? const [])
        .map((entry) => AuthUser.fromJson(entry as Map<String, dynamic>))
        .toList();
  }

  Future<StationStatus> stationStatus() async =>
      StationStatus.fromJson(await _send('GET', '/api/station'));

  Future<SchedulingSettings> schedulingConfig() async =>
      SchedulingSettings.fromJson(await _send('GET', '/api/scheduling-config'));

  Future<({List<WorkerStatus> workers, int offlineAfterSeconds})>
  workers() async {
    final response = await _send('GET', '/api/workers');
    return (
      workers: ((response['workers'] as List<dynamic>?) ?? const [])
          .map((entry) => WorkerStatus.fromJson(entry as Map<String, dynamic>))
          .toList(),
      offlineAfterSeconds:
          (response['offline_after_seconds'] as num?)?.toInt() ?? 0,
    );
  }

  Future<TleStatus> tleStatus() async =>
      TleStatus.fromJson(await _send('GET', '/api/tle-status'));

  Future<TleRefreshResult> refreshTles() async => TleRefreshResult.fromJson(
    await _send('POST', '/api/satellites/refresh-tles'),
  );


  Future<SetupStatus> setupStatus() async =>
      SetupStatus.fromJson(await _send('GET', '/api/setup'));

  Future<void> runSetup(Map<String, dynamic> configuration) async {
    await _send('POST', '/api/setup', body: configuration);
  }

  Future<Satellite> addSatellite({required int noradId, String? name}) async {
    final response = await _send(
      'POST',
      '/api/satellites',
      body: {
        'norad_id': noradId,
        if (name != null && name.isNotEmpty) 'name': name,
      },
    );
    return Satellite.fromJson(response);
  }

  Future<Satellite> updateSatellite(
    String id, {
    String? name,
    String? description,
    bool? isSchedulable,
  }) async {
    final response = await _send(
      'PATCH',
      '/api/satellites/$id',
      body: {
        'name': ?name,
        'description': ?description,
        'is_schedulable': ?isSchedulable,
      },
    );
    return Satellite.fromJson(response);
  }

  Future<void> deleteSatellite(String id) async {
    await _send('DELETE', '/api/satellites/$id');
  }

  Future<void> refreshSatelliteMetadata(String id) async {
    await _send('POST', '/api/satellites/$id/refresh-metadata');
  }

  Future<StationConfig> stationConfig() async =>
      StationConfig.fromJson(await _send('GET', '/api/station/config'));

  Future<int> updateStation(Map<String, dynamic> changes) async {
    final response = await _send('PATCH', '/api/station', body: changes);
    return (response['upcoming_passes_unchanged'] as num?)?.toInt() ?? 0;
  }

  Future<BandConfig> updateBand(
    String band,
    Map<String, dynamic> changes,
  ) async {
    final response = await _send(
      'PUT',
      '/api/station/bands/$band',
      body: changes,
    );
    return BandConfig.fromJson(response['band'] as Map<String, dynamic>);
  }

  Future<int> updateHardware(Map<String, dynamic> changes) async {
    final response = await _send('PUT', '/api/station/hardware', body: changes);
    return (response['upcoming_passes_unchanged'] as num?)?.toInt() ?? 0;
  }

  Future<DateTime> updateSchedulingConfig(Map<String, dynamic> settings) async {
    final response = await _send(
      'PUT',
      '/api/scheduling-config',
      body: settings,
    );
    return DateTime.parse(response['effective_from'] as String).toUtc();
  }

  Future<AuthUser> createUser({
    required String username,
    required String password,
    required String role,
  }) async {
    final response = await _send(
      'POST',
      '/api/users',
      body: {'username': username, 'password': password, 'role': role},
    );
    return AuthUser.fromJson(response);
  }

  Future<AuthUser> setUserRole(String id, String role) async {
    final response = await _send(
      'PATCH',
      '/api/users/$id',
      body: {'role': role},
    );
    return AuthUser.fromJson(response);
  }

  Future<void> deleteUser(String id) async {
    await _send('DELETE', '/api/users/$id');
  }

  Future<List<AuditRecord>> auditTrail({
    String? action,
    int limit = 100,
  }) async {
    final query = StringBuffer('?limit=$limit');
    if (action != null) query.write('&action=$action');
    final response = await _send('GET', '/api/audit$query');
    return ((response['records'] as List<dynamic>?) ?? const [])
        .map((entry) => AuditRecord.fromJson(entry as Map<String, dynamic>))
        .toList();
  }


  Future<bool> serverIsReachable() async {
    try {
      await _send('GET', '/health', authenticated: false);
      return true;
    } catch (_) {
      return false;
    }
  }
}
