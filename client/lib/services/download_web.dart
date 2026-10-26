// Web save: hand completed bytes to the browser's download manager.
//
// The recording is fetched with the session's Authorization header (an anchor
// click cannot set custom headers), then offered to the browser as a blob so
// the user gets a real file, not just an in-memory result.

import 'dart:js_interop';
import 'dart:typed_data';

import 'package:web/web.dart' as web;

void saveDownloadedBytes(String fileName, List<int> bytes) {
  final blob = web.Blob([Uint8List.fromList(bytes).toJS].toJS);
  final url = web.URL.createObjectURL(blob);
  // .download turns a navigate into a save, named after the stored recording.
  web.HTMLAnchorElement()
    ..href = url
    ..download = fileName
    ..click();
  web.URL.revokeObjectURL(url);
}
