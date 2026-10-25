#!/usr/bin/env python3
"""Serve the built web client without letting the browser cache it.

A Flutter release build registers a service worker that will happily serve
yesterday's JavaScript; during development that looks like a change failing to
take. Building with --pwa-strategy=none and serving no-store avoids both.
"""

import functools
import http.server
import os
import socketserver

ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "client", "build", "web")


class NoCache(http.server.SimpleHTTPRequestHandler):
    def end_headers(self):
        self.send_header("Cache-Control", "no-store, max-age=0")
        super().end_headers()

    def log_message(self, *args):
        pass


def main() -> None:
    handler = functools.partial(NoCache, directory=os.path.abspath(ROOT))
    port = int(os.environ.get("AAGASA_WEB_PORT", "5001"))
    with socketserver.TCPServer(("127.0.0.1", port), handler) as server:
        server.serve_forever()


if __name__ == "__main__":
    main()
