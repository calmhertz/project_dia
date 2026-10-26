// Saves a fetched recording so the user ends up with a file.
//
// The target is selected at compile time: web runs the browser save, every
// other platform the stub (which callers guard with kIsWeb).

import 'package:flutter/foundation.dart';

import 'download_stub.dart' if (dart.library.html) 'download_web.dart' as target;

/// Offers the bytes to the platform as a file named after the recording.
void saveRecordingDownload(String fileName, List<int> bytes) {
  if (kIsWeb) target.saveDownloadedBytes(fileName, bytes);
}

/// Basename of a stored recording, e.g. `recordings/<pass>/baseband.ziq`.
String recordingFileName(String relativePath) {
  final parts = relativePath.split('/');
  return parts.isEmpty ? 'recording.bin' : parts.last;
}
