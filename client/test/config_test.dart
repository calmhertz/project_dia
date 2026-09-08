import 'package:aagasa_client/api/api_client.dart';
import 'package:aagasa_client/config/app_config.dart';
import 'package:flutter_test/flutter_test.dart';

/// The endpoint is defined exactly once (client-spec section B): the page's
/// own origin on web, a single configured constant everywhere else.
void main() {
  test('the non-web base URL is the single configured constant', () {
    // flutter test runs on the VM (kIsWeb == false), so this is the mobile
    // path; there is no same-origin story there.
    expect(AppConfig.apiBaseUrl, AppConfig.mobileApiBaseUrl);
    expect(AppConfig.mobileApiBaseUrl, isNotEmpty);
  });

  test('the ApiClient defaults to the platform base URL', () {
    final client = ApiClient();
    expect(client.baseUrl, AppConfig.apiBaseUrl);
    client.dispose();
  });
}