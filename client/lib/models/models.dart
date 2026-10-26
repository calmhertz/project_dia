/// Domain types as the Client sees them.
///
/// These mirror the Server's JSON. Fields the Server may omit for
/// authorization reasons, such as a pass owner, are nullable here rather than
/// defaulted, so the UI can tell "hidden" from "empty".
library;

class AuthUser {
  const AuthUser({
    required this.id,
    required this.username,
    required this.role,
    required this.mustChangePassword,
  });

  final String id;
  final String username;
  final String role;
  final bool mustChangePassword;

  bool get isAdmin => role == 'admin' || role == 'root';
  bool get isRoot => role == 'root';

  factory AuthUser.fromJson(Map<String, dynamic> json) => AuthUser(
    id: json['id'] as String? ?? '',
    username: json['username'] as String? ?? '',
    role: json['role'] as String? ?? 'user',
    mustChangePassword: json['must_change_password'] as bool? ?? false,
  );
}

class Satellite {
  const Satellite({
    required this.id,
    required this.noradId,
    required this.name,
    required this.description,
    required this.isSchedulable,
    required this.metadata,
  });

  final String id;
  final int noradId;
  final String name;
  final String description;
  final bool isSchedulable;

  /// Enrichment only. Missing metadata must never block scheduling
  /// (spec.md section 11.2), so the UI treats every entry as optional.
  final Map<String, dynamic> metadata;

  String? get status => metadata['status'] as String?;
  String? get countries => metadata['countries'] as String?;
  String? get operator => metadata['operator'] as String?;
  String? get website => metadata['website'] as String?;

  factory Satellite.fromJson(Map<String, dynamic> json) => Satellite(
    id: json['id'] as String,
    noradId: json['norad_id'] as int? ?? 0,
    name: json['name'] as String? ?? '',
    description: json['description'] as String? ?? '',
    isSchedulable: json['is_schedulable'] as bool? ?? false,
    metadata: (json['metadata'] as Map<String, dynamic>?) ?? const {},
  );
}

class TleSummary {
  const TleSummary({
    required this.epoch,
    required this.source,
    required this.ageHours,
  });

  final DateTime epoch;
  final String source;
  final double ageHours;

  factory TleSummary.fromJson(Map<String, dynamic> json) => TleSummary(
    epoch: DateTime.parse(json['epoch'] as String).toUtc(),
    source: json['source'] as String? ?? '',
    ageHours: (json['age_hours'] as num?)?.toDouble() ?? 0,
  );
}

/// A pass the user could request, from the prediction service.
class PredictedPass {
  const PredictedPass({
    required this.aos,
    required this.tca,
    required this.los,
    required this.maxElevationDegrees,
    required this.durationSeconds,
    required this.aosAzimuthDegrees,
    required this.losAzimuthDegrees,
  });

  final DateTime aos;
  final DateTime tca;
  final DateTime los;
  final double maxElevationDegrees;
  final double durationSeconds;
  final double aosAzimuthDegrees;
  final double losAzimuthDegrees;

  Duration get duration => Duration(seconds: durationSeconds.round());

  factory PredictedPass.fromJson(Map<String, dynamic> json) => PredictedPass(
    aos: DateTime.parse(json['aos'] as String).toUtc(),
    tca: DateTime.parse(json['tca'] as String).toUtc(),
    los: DateTime.parse(json['los'] as String).toUtc(),
    maxElevationDegrees: (json['max_elevation_degrees'] as num).toDouble(),
    durationSeconds: (json['duration_seconds'] as num).toDouble(),
    aosAzimuthDegrees: (json['aos_azimuth_degrees'] as num?)?.toDouble() ?? 0,
    losAzimuthDegrees: (json['los_azimuth_degrees'] as num?)?.toDouble() ?? 0,
  );
}

class PassPrediction {
  const PassPrediction({
    required this.passes,
    required this.stationActiveBand,
    required this.minimumElevationDegrees,
  });

  final List<PredictedPass> passes;
  final String stationActiveBand;
  final double minimumElevationDegrees;

  factory PassPrediction.fromJson(Map<String, dynamic> json) => PassPrediction(
    passes: ((json['passes'] as List<dynamic>?) ?? const [])
        .map((entry) => PredictedPass.fromJson(entry as Map<String, dynamic>))
        .toList(),
    stationActiveBand: json['station_active_band'] as String? ?? '',
    minimumElevationDegrees:
        (json['minimum_elevation_degrees'] as num?)?.toDouble() ?? 0,
  );
}

/// A requested or scheduled pass.
class Pass {
  const Pass({
    required this.id,
    required this.satelliteId,
    required this.status,
    required this.visibility,
    required this.band,
    required this.aos,
    required this.los,
    required this.maxElevationDegrees,
    required this.recordingMode,
    this.requestedBy,
    this.cancellationReason,
  });

  final String id;
  final String satelliteId;
  final String status;
  final String visibility;
  final String band;
  final DateTime aos;
  final DateTime los;
  final double maxElevationDegrees;
  final String recordingMode;

  /// Absent when the Server withheld it, which is how a public pass hides its
  /// owner from unrelated users (spec.md section 16).
  final String? requestedBy;
  final String? cancellationReason;

  bool get isPublic => visibility == 'public';
  bool get isPending => status == 'pending_approval';
  bool get isFinished =>
      status == 'completed' || status == 'failed' || status == 'missed';

  factory Pass.fromJson(Map<String, dynamic> json) => Pass(
    id: json['id'] as String,
    satelliteId: json['satellite_id'] as String? ?? '',
    status: json['status'] as String? ?? '',
    visibility: json['visibility'] as String? ?? 'private',
    band: json['band'] as String? ?? '',
    aos: DateTime.parse(json['aos'] as String).toUtc(),
    los: DateTime.parse(json['los'] as String).toUtc(),
    maxElevationDegrees:
        (json['max_elevation_degrees'] as num?)?.toDouble() ?? 0,
    recordingMode: json['recording_mode'] as String? ?? '',
    requestedBy: json['requested_by'] as String?,
    cancellationReason: json['cancellation_reason'] as String?,
  );
}

class Recording {
  const Recording({
    required this.recordingId,
    required this.relativePath,
    required this.sizeBytes,
    required this.downloadUrl,
    required this.receivedAt,
    this.passId,
    this.satelliteId,
    this.passAos,
    this.visibility,
  });

  final String recordingId;
  final String relativePath;
  final int sizeBytes;
  final String downloadUrl;
  final DateTime receivedAt;

  /// Set when the recording came from a listing across passes, so a row can
  /// say which pass produced it without another request.
  final String? passId;
  final String? satelliteId;
  final DateTime? passAos;
  final String? visibility;

  bool get isPublic => visibility == 'public';

  factory Recording.fromJson(Map<String, dynamic> json) => Recording(
    recordingId: json['recording_id'] as String,
    relativePath: json['relative_path'] as String? ?? '',
    sizeBytes: (json['size_bytes'] as num?)?.toInt() ?? 0,
    downloadUrl: json['download_url'] as String? ?? '',
    receivedAt: DateTime.parse(json['received_at'] as String).toUtc(),
    passId: json['pass_id'] as String?,
    satelliteId: json['satellite_id'] as String?,
    passAos: json['pass_aos'] == null
        ? null
        : DateTime.parse(json['pass_aos'] as String).toUtc(),
    visibility: json['visibility'] as String?,
  );
}

/// Advisory information attached to a scheduling result, such as a band
/// mismatch. A warning never means the request failed.
class SchedulingWarning {
  const SchedulingWarning({required this.code, required this.message});

  final String code;
  final String message;

  factory SchedulingWarning.fromJson(Map<String, dynamic> json) =>
      SchedulingWarning(
        code: json['code'] as String? ?? '',
        message: json['message'] as String? ?? '',
      );
}

class ScheduleResult {
  const ScheduleResult({
    required this.pass,
    required this.warnings,
    this.overriddenPassIds = const [],
  });

  final Pass pass;
  final List<SchedulingWarning> warnings;

  /// Passes a Root override cancelled to make room. Empty for every ordinary
  /// request (spec.md section 13.7).
  final List<String> overriddenPassIds;
}

// Operational views for Admin and Root (V14) ---------------------------------

/// Where the station is and what it is operating on.
///
/// Deliberately not the full configuration: an Admin reads station operation,
/// while hardware settings belong to the Root surface.
class StationStatus {
  const StationStatus({
    required this.name,
    required this.latitudeDegrees,
    required this.longitudeDegrees,
    required this.altitudeM,
    required this.timezone,
    required this.activeRfBand,
    required this.configuredBands,
    required this.antennaDescriptions,
  });

  final String name;
  final double latitudeDegrees;
  final double longitudeDegrees;
  final double altitudeM;
  final String timezone;
  final String activeRfBand;
  final List<String> configuredBands;
  final List<String> antennaDescriptions;

  factory StationStatus.fromJson(Map<String, dynamic> json) => StationStatus(
    name: json['name'] as String? ?? '',
    latitudeDegrees: (json['latitude_degrees'] as num?)?.toDouble() ?? 0,
    longitudeDegrees: (json['longitude_degrees'] as num?)?.toDouble() ?? 0,
    altitudeM: (json['altitude_m'] as num?)?.toDouble() ?? 0,
    timezone: json['timezone'] as String? ?? '',
    activeRfBand: json['active_rf_band'] as String? ?? '',
    configuredBands: ((json['configured_bands'] as List<dynamic>?) ?? const [])
        .map((entry) => entry as String)
        .toList(),
    antennaDescriptions:
        ((json['antenna_descriptions'] as List<dynamic>?) ?? const [])
            .map((entry) => entry as String)
            .toList(),
  );
}

/// A station Worker as the Server reports it. Nothing here describes the
/// transport or the process; it is reachability and backlog only
/// (client-spec section 4).
class WorkerStatus {
  const WorkerStatus({
    required this.name,
    required this.connectionState,
    required this.inSync,
    required this.pendingUploads,
    required this.pendingReports,
    this.lastSeenAt,
    this.secondsSinceSeen,
  });

  final String name;
  final String connectionState;
  final bool inSync;
  final int pendingUploads;
  final int pendingReports;
  final DateTime? lastSeenAt;
  final double? secondsSinceSeen;

  bool get isOnline => connectionState == 'online';
  bool get hasBacklog => pendingUploads > 0 || pendingReports > 0;

  factory WorkerStatus.fromJson(Map<String, dynamic> json) => WorkerStatus(
    name: json['name'] as String? ?? '',
    connectionState: json['connection_state'] as String? ?? 'offline',
    inSync: json['in_sync'] as bool? ?? false,
    pendingUploads: (json['pending_uploads'] as num?)?.toInt() ?? 0,
    pendingReports: (json['pending_reports'] as num?)?.toInt() ?? 0,
    lastSeenAt: json['last_seen_at'] == null
        ? null
        : DateTime.parse(json['last_seen_at'] as String).toUtc(),
    secondsSinceSeen: (json['seconds_since_seen'] as num?)?.toDouble(),
  );
}

/// The scheduling rules in force. Admin reads them; only Root changes them.
class SchedulingSettings {
  const SchedulingSettings({
    required this.minimumLeadTime,
    required this.prePassBuffer,
    required this.postPassBuffer,
    required this.recordingPreRoll,
    required this.recordingPostRoll,
    required this.minimumElevationDegrees,
    required this.effectiveFrom,
    this.pending,
  });

  final Duration minimumLeadTime;
  final Duration prePassBuffer;
  final Duration postPassBuffer;
  final Duration recordingPreRoll;
  final Duration recordingPostRoll;
  final double minimumElevationDegrees;
  final DateTime effectiveFrom;

  /// A saved change that has not taken effect yet. A new version becomes
  /// usable only after the newly chosen lead time, so without this a save
  /// looks like it was lost (spec.md section 13.6).
  final SchedulingSettings? pending;

  static Duration _seconds(dynamic value) =>
      Duration(seconds: (value as num?)?.toInt() ?? 0);

  factory SchedulingSettings.fromJson(Map<String, dynamic> json) =>
      SchedulingSettings(
        minimumLeadTime: _seconds(json['minimum_lead_time_seconds']),
        prePassBuffer: _seconds(json['pre_pass_buffer_seconds']),
        postPassBuffer: _seconds(json['post_pass_buffer_seconds']),
        recordingPreRoll: _seconds(json['recording_pre_roll_seconds']),
        recordingPostRoll: _seconds(json['recording_post_roll_seconds']),
        minimumElevationDegrees:
            (json['minimum_elevation_degrees'] as num?)?.toDouble() ?? 0,
        effectiveFrom: DateTime.parse(json['effective_from'] as String).toUtc(),
      );
}

/// Freshness of one satellite's orbital data.
class TleStatusEntry {
  const TleStatusEntry({
    required this.satelliteId,
    required this.noradId,
    required this.name,
    required this.hasOrbitalData,
    required this.stale,
    this.epoch,
    this.source,
    this.ageHours,
  });

  final String satelliteId;
  final int noradId;
  final String name;

  /// False when the satellite has never had orbital data, which is a
  /// different problem from data that has gone stale.
  final bool hasOrbitalData;
  final bool stale;
  final DateTime? epoch;
  final String? source;
  final double? ageHours;

  factory TleStatusEntry.fromJson(Map<String, dynamic> json) => TleStatusEntry(
    satelliteId: json['satellite_id'] as String? ?? '',
    noradId: (json['norad_id'] as num?)?.toInt() ?? 0,
    name: json['name'] as String? ?? '',
    hasOrbitalData: json['has_orbital_data'] as bool? ?? false,
    stale: json['stale'] as bool? ?? false,
    epoch: json['epoch'] == null
        ? null
        : DateTime.parse(json['epoch'] as String).toUtc(),
    source: json['source'] as String?,
    ageHours: (json['age_hours'] as num?)?.toDouble(),
  );
}

class TleStatus {
  const TleStatus({
    required this.satellites,
    required this.staleAfterHours,
    required this.missingCount,
    required this.staleCount,
  });

  final List<TleStatusEntry> satellites;
  final int staleAfterHours;
  final int missingCount;
  final int staleCount;

  factory TleStatus.fromJson(Map<String, dynamic> json) => TleStatus(
    satellites: ((json['satellites'] as List<dynamic>?) ?? const [])
        .map((entry) => TleStatusEntry.fromJson(entry as Map<String, dynamic>))
        .toList(),
    staleAfterHours: (json['stale_after_hours'] as num?)?.toInt() ?? 0,
    missingCount: (json['missing_count'] as num?)?.toInt() ?? 0,
    staleCount: (json['stale_count'] as num?)?.toInt() ?? 0,
  );
}

/// The outcome of a catalogue-wide TLE refresh.
class TleRefreshResult {
  const TleRefreshResult({
    required this.updated,
    required this.keptLastKnownGood,
    required this.failed,
  });

  final int updated;

  /// A provider failure leaves the previous data in place rather than
  /// discarding it (spec.md section 11.1).
  final int keptLastKnownGood;
  final int failed;

  factory TleRefreshResult.fromJson(Map<String, dynamic> json) =>
      TleRefreshResult(
        updated: (json['updated'] as num?)?.toInt() ?? 0,
        keptLastKnownGood: (json['kept_last_known_good'] as num?)?.toInt() ?? 0,
        failed: (json['failed'] as num?)?.toInt() ?? 0,
      );
}

// Root configuration and audit (V15) -----------------------------------------

/// Radio settings for one band.
class BandConfig {
  const BandConfig({
    required this.band,
    required this.antennaDescription,
    this.centerFrequencyHz,
    this.sampleRateHz,
    this.gainDb,
    this.ppmCorrection = 0,
    this.biasTeeEnabled = false,
  });

  final String band;
  final String antennaDescription;
  final int? centerFrequencyHz;
  final int? sampleRateHz;
  final double? gainDb;
  final int ppmCorrection;
  final bool biasTeeEnabled;

  factory BandConfig.fromJson(Map<String, dynamic> json) => BandConfig(
    band: json['band'] as String? ?? '',
    antennaDescription: json['antenna_description'] as String? ?? '',
    centerFrequencyHz: (json['center_frequency_hz'] as num?)?.toInt(),
    sampleRateHz: (json['sample_rate_hz'] as num?)?.toInt(),
    gainDb: (json['gain_db'] as num?)?.toDouble(),
    ppmCorrection: (json['ppm_correction'] as num?)?.toInt() ?? 0,
    biasTeeEnabled: json['bias_tee_enabled'] as bool? ?? false,
  );
}

/// Rotator and SDR settings.
class HardwareConfig {
  const HardwareConfig({
    required this.rotatorSerialPort,
    required this.rotatorBaudRate,
    required this.parkAzimuthDegrees,
    required this.parkElevationDegrees,
    required this.sdrDeviceIdentifier,
  });

  final String rotatorSerialPort;
  final int rotatorBaudRate;
  final int parkAzimuthDegrees;
  final int parkElevationDegrees;
  final String sdrDeviceIdentifier;

  factory HardwareConfig.fromJson(Map<String, dynamic> json) => HardwareConfig(
    rotatorSerialPort: json['rotator_serial_port'] as String? ?? '',
    rotatorBaudRate: (json['rotator_baud_rate'] as num?)?.toInt() ?? 0,
    parkAzimuthDegrees:
        (json['rotator_park_azimuth_degrees'] as num?)?.toInt() ?? 0,
    parkElevationDegrees:
        (json['rotator_park_elevation_degrees'] as num?)?.toInt() ?? 0,
    sdrDeviceIdentifier: json['sdr_device_identifier'] as String? ?? '',
  );
}

/// The complete station configuration, which only Root may read.
class StationConfig {
  const StationConfig({
    required this.name,
    required this.latitudeDegrees,
    required this.longitudeDegrees,
    required this.altitudeM,
    required this.timezone,
    required this.activeRfBand,
    required this.bands,
    required this.hardware,
    required this.upcomingPasses,
  });

  final String name;
  final double latitudeDegrees;
  final double longitudeDegrees;
  final double altitudeM;
  final String timezone;
  final String activeRfBand;
  final List<BandConfig> bands;
  final HardwareConfig hardware;

  /// Passes already committed under the current settings. A change reports
  /// this rather than rewriting them (client-spec section 29).
  final int upcomingPasses;

  factory StationConfig.fromJson(Map<String, dynamic> json) => StationConfig(
    name: json['name'] as String? ?? '',
    latitudeDegrees: (json['latitude_degrees'] as num?)?.toDouble() ?? 0,
    longitudeDegrees: (json['longitude_degrees'] as num?)?.toDouble() ?? 0,
    altitudeM: (json['altitude_m'] as num?)?.toDouble() ?? 0,
    timezone: json['timezone'] as String? ?? '',
    activeRfBand: json['active_rf_band'] as String? ?? '',
    bands: ((json['bands'] as List<dynamic>?) ?? const [])
        .map((entry) => BandConfig.fromJson(entry as Map<String, dynamic>))
        .toList(),
    hardware: HardwareConfig.fromJson(
      (json['hardware'] as Map<String, dynamic>?) ?? const {},
    ),
    upcomingPasses: (json['upcoming_passes'] as num?)?.toInt() ?? 0,
  );
}

/// One entry in the audit trail. What, who, which subject, when. The recorded
/// before and after state stays server-side.
class AuditRecord {
  const AuditRecord({
    required this.id,
    required this.action,
    required this.entityType,
    required this.entityId,
    required this.createdAt,
    this.actorUserId,
    this.reason,
  });

  final int id;
  final String action;
  final String entityType;
  final String entityId;
  final DateTime createdAt;
  final String? actorUserId;
  final String? reason;

  factory AuditRecord.fromJson(Map<String, dynamic> json) => AuditRecord(
    id: (json['id'] as num?)?.toInt() ?? 0,
    action: json['action'] as String? ?? '',
    entityType: json['entity_type'] as String? ?? '',
    entityId: json['entity_id'] as String? ?? '',
    createdAt: DateTime.parse(json['created_at'] as String).toUtc(),
    actorUserId: json['actor_user_id'] as String?,
    reason: json['reason'] as String?,
  );
}

/// Whether first-run setup has been completed.
class SetupStatus {
  const SetupStatus({required this.initialized, this.initializedAt});

  final bool initialized;
  final DateTime? initializedAt;

  factory SetupStatus.fromJson(Map<String, dynamic> json) => SetupStatus(
    initialized: json['initialized'] as bool? ?? false,
    initializedAt: json['initialized_at'] == null
        ? null
        : DateTime.parse(json['initialized_at'] as String).toUtc(),
  );
}

/// A SatDump pipeline a pass can be given.
///
/// Standard pipelines are whatever the station's SatDump provides; custom
/// ones are JSON an operator uploaded. The client never invents either
/// (spec.md section 19.1).
class SatDumpPipeline {
  const SatDumpPipeline({
    required this.id,
    required this.name,
    required this.isCustom,
  });

  final String id;
  final String name;
  final bool isCustom;

  factory SatDumpPipeline.fromJson(Map<String, dynamic> json) =>
      SatDumpPipeline(
        id: json['id'] as String? ?? '',
        name: json['name'] as String? ?? '',
        isCustom: json['is_custom'] as bool? ?? false,
      );
}

/// What the station offers. An empty list with [reportedByStation] false
/// means no ground station has reported an installation yet, which is a
/// different thing from a station with no pipelines.
class PipelineCatalogue {
  const PipelineCatalogue({
    required this.pipelines,
    required this.reportedByStation,
  });

  final List<SatDumpPipeline> pipelines;
  final bool reportedByStation;

  factory PipelineCatalogue.fromJson(Map<String, dynamic> json) =>
      PipelineCatalogue(
        pipelines: ((json['pipelines'] as List<dynamic>?) ?? const [])
            .map(
              (entry) =>
                  SatDumpPipeline.fromJson(entry as Map<String, dynamic>),
            )
            .toList(),
        reportedByStation: json['reported_by_station'] as bool? ?? false,
      );
}
