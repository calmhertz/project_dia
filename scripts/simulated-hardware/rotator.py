#!/usr/bin/env python3
"""A stand-in for the Yaesu G-550, on a virtual serial port.

The Worker talks to a real serial device, so simulating the rotator means
providing one: this opens a pseudo-terminal, links it to a stable path, and
answers G-550 commands the way the controller does. Pointing can then be
exercised end to end with no rotator attached (plan.md V17).

    scripts/simulated-hardware/rotator.py --link /tmp/aagasa-rotator

Every commanded angle is logged, so a test can check where the antenna was
told to go.
"""

import argparse
import os
import pty
import re
import signal
import sys
import time

# The Worker sends "W<azimuth:04d> <elevation:03d>\r" and reads no reply; the
# G-550 has no position readback in the reference implementation. Getting this
# width wrong is not harmless: the stand-in silently logs every real command as
# unrecognised and the pointing it is meant to prove goes unrecorded.
MOVE = re.compile(rb"^W(\d{4}) (\d{3})\r$")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--link", default="/tmp/aagasa-rotator",
                        help="path to link the virtual serial port to")
    parser.add_argument("--quiet", action="store_true")
    arguments = parser.parse_args()

    primary, secondary = pty.openpty()
    device = os.ttyname(secondary)

    # A stable path, so configuration does not change every run.
    link = arguments.link
    try:
        if os.path.islink(link) or os.path.exists(link):
            os.remove(link)
        os.symlink(device, link)
    except OSError as error:
        print(f"could not link {link}: {error}", file=sys.stderr)
        return 1

    print(f"simulated rotator on {link} (-> {device})", flush=True)

    azimuth, elevation = 0, 0
    running = True

    def stop(_signal, _frame):
        nonlocal running
        running = False

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)

    buffer = b""
    try:
        while running:
            try:
                chunk = os.read(primary, 64)
            except OSError:
                time.sleep(0.05)
                continue
            if not chunk:
                continue
            buffer += chunk

            while b"\r" in buffer:
                line, buffer = buffer.split(b"\r", 1)
                command = line + b"\r"

                move = MOVE.match(command)
                if move:
                    azimuth, elevation = int(move.group(1)), int(move.group(2))
                    if not arguments.quiet:
                        print(f"move az={azimuth} el={elevation}", flush=True)
                    continue
                if command.startswith(b"C2") or command.startswith(b"B"):
                    os.write(primary, f"+0{azimuth:03d}+0{elevation:02d}\r".encode())
                    continue
                if not arguments.quiet:
                    print(f"unrecognised command {command!r}", flush=True)
    finally:
        try:
            os.remove(link)
        except OSError:
            pass
        os.close(primary)
        os.close(secondary)
    print("simulated rotator stopped", flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
