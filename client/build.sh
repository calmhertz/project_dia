#!/usr/bin/env bash
# Build the Android APK with a configurable server base URL.
#
# Usage:
#   ./build.sh [extra flutter build args...]
#   SERVER_URL=https://host ./build.sh      # skip the prompt
#
# The app appends /api/... itself, so give only the origin (no /api suffix).
set -euo pipefail

DEFAULT_URL="https://sjcit.ac.in"

cd "$(dirname "$0")"

if [[ -z "${SERVER_URL:-}" ]]; then
  read -r -p "Server base URL [${DEFAULT_URL}]: " SERVER_URL || true
fi
SERVER_URL="${SERVER_URL:-$DEFAULT_URL}"
SERVER_URL="${SERVER_URL%/}"          # strip trailing slash
SERVER_URL="${SERVER_URL%/api}"       # app already prefixes /api
[[ "$SERVER_URL" =~ ^https?:// ]] || SERVER_URL="https://${SERVER_URL}"

echo "Building APK against: ${SERVER_URL}"

flutter pub get
flutter build apk --release \
  --dart-define=AAGASA_SERVER_URL="${SERVER_URL}" \
  "$@"

echo "APK: $(pwd)/build/app/outputs/flutter-apk/app-release.apk"
