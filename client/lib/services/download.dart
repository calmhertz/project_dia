import 'package:flutter/foundation.dart';

import '../models/models.dart';
import 'download_stub.dart'
    if (dart.library.html) 'download_web.dart'
    as target;

/// Saving a download to disk is only implemented for the web app, which is
/// how the station is served; elsewhere the caller must say so instead of
/// pretending the file was saved.
bool get canSaveDownloads => debugCanSaveDownloads ?? kIsWeb;

/// Lets widget tests (which do not run as web) take the web download path.
@visibleForTesting
bool? debugCanSaveDownloads;

void saveRecordingDownload(String fileName, List<int> bytes) {
  if (kIsWeb) target.saveDownloadedBytes(fileName, bytes);
}

String recordingDownloadFileName(Recording recording) {
  final parts = recording.relativePath.split('/');
  final name = parts.isEmpty ? 'recording.bin' : parts.last;
  final dot = name.lastIndexOf('.');
  final stem = dot > 0 ? name.substring(0, dot) : name;
  final extension = dot > 0 ? name.substring(dot) : '';
  final at = (recording.passAos ?? recording.receivedAt).toLocal();
  final stamp = [
    at.day,
    at.month,
    at.year % 100,
    at.hour,
    at.minute,
    at.second,
  ].map((part) => part.toString().padLeft(2, '0')).join('_');
  return '${stem}_$stamp$extension';
}
