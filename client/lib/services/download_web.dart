
import 'dart:js_interop';
import 'dart:typed_data';

import 'package:web/web.dart' as web;

void saveDownloadedBytes(String fileName, List<int> bytes) {
  final blob = web.Blob([Uint8List.fromList(bytes).toJS].toJS);
  final url = web.URL.createObjectURL(blob);
  web.HTMLAnchorElement()
    ..href = url
    ..download = fileName
    ..click();
  web.URL.revokeObjectURL(url);
}
