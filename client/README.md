# Client

Flutter app targeting Web, Android, and iOS. See `client-spec.md` and
`../spec.md`.

Material 3, dark mode only. The theme seed `#487CE5` is defined once, in
`lib/theme/app_theme.dart`.

The Client talks only to the Aagasa Server - never to the Worker, a datastore,
or an external TLE provider.

## Development

```sh
flutter pub get
flutter analyze
flutter test
flutter run -d chrome
```

The endpoint is defined once, in `lib/config/app_config.dart`, and chosen by
platform at `ApiClient.defaultBaseUrl`:

- **web** uses the page's own origin. nginx serves the built client and
  proxies `/api` from the same origin, so the browser never sees CORS
  (port 50000 in a `deploy.sh` deployment).
- **android/iOS/desktop** use `AAGASA_SERVER_URL`, which defaults to the
  station at `http://127.0.0.1:8080`, overridable at build time:
  `flutter run -d linux --dart-define=AAGASA_SERVER_URL=https://host:9090`

## V1 scope

Theme, responsive navigation shell (navigation bar under 600dp, navigation rail
above), and Server connectivity/error state. Screens are explicit empty states
until their feature phases land.
