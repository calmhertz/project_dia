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
flutter run -d chrome --dart-define=AAGASA_SERVER_URL=http://127.0.0.1:8080
```

The Server base URL comes from the `AAGASA_SERVER_URL` compile-time define and
defaults to `http://127.0.0.1:8080`.

## V1 scope

Theme, responsive navigation shell (navigation bar under 600dp, navigation rail
above), and Server connectivity/error state. Screens are explicit empty states
until their feature phases land.
