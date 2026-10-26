
import 'package:flutter/foundation.dart';

import 'download_stub.dart' if (dart.library.html) 'download_web.dart' as target;

void saveRecordingDownload(String fileName, List<int> bytes) {
  if (kIsWeb) target.saveDownloadedBytes(fileName, bytes);
}

String recordingFileName(String relativePath) {
  final parts = relativePath.split('/');
  return parts.isEmpty ? 'recording.bin' : parts.last;
}
