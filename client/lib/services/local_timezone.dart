import 'local_timezone_stub.dart'
    if (dart.library.js_interop) 'local_timezone_web.dart'
    as target;

/// The IANA time zone the browser runs in, such as "Asia/Kolkata".
///
/// Null off the web, or when the browser does not say. Some browsers report
/// an older alias (Chrome has used "Asia/Calcutta"); the Server accepts those.
String? browserTimeZone() {
  try {
    final zone = target.browserTimeZone();
    return (zone == null || zone.isEmpty) ? null : zone;
  } catch (_) {
    return null;
  }
}
