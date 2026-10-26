library;

class ApiException implements Exception {
  const ApiException({
    required this.statusCode,
    required this.code,
    required this.message,
    this.details = const {},
  });

  final int statusCode;

  final String code;
  final String message;
  final Map<String, dynamic> details;

  bool get isUnauthenticated => statusCode == 401;
  bool get isForbidden => statusCode == 403;
  bool get isNotFound => statusCode == 404;
  bool get isConflict => statusCode == 409;
  bool get mustChangePassword => code == 'password_change_required';
  bool get isStationConflict => code == 'station_conflict';
  bool get isWorkerOffline => code == 'worker_offline';
  bool get isLeadTime => code == 'lead_time';

  @override
  String toString() => message;
}

class NetworkException implements Exception {
  const NetworkException(this.message);

  final String message;

  @override
  String toString() => message;
}
