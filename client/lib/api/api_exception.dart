/// Errors the Server can return, in the shapes the UI must distinguish
/// (client-spec section 23).
library;

class ApiException implements Exception {
  const ApiException({
    required this.statusCode,
    required this.code,
    required this.message,
    this.details = const {},
  });

  final int statusCode;

  /// The Server's error identifier, for example `station_conflict`.
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

/// The Server could not be reached at all, which the UI shows differently from
/// a Server that answered with an error (client-spec section 22).
class NetworkException implements Exception {
  const NetworkException(this.message);

  final String message;

  @override
  String toString() => message;
}
