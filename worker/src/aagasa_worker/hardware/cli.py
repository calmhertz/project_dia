"""Hardware bring-up command line.

Lets an operator verify a real station without waiting for a scheduled pass:

    aagasa-hardware status                  report what is present
    aagasa-hardware pipelines               list installed SatDump pipelines
    aagasa-hardware park                    drive the rotator to safe position
    aagasa-hardware move <az> <el>          drive to a commanded position
"""

import argparse
import logging
import sys

from aagasa_worker.config import Config, load_config
from aagasa_worker.hardware.errors import HardwareError
from aagasa_worker.hardware.rotator import G550Rotator
from aagasa_worker.hardware.satdump import SatDump
from aagasa_worker.hardware.sdr import RTLSDRProbe
from aagasa_worker.hardware.station import Station

logger = logging.getLogger("aagasa_worker.hardware")


def build_station(config: Config) -> Station:
    """Assemble the station from configuration."""
    pipeline_dirs = [config.satdump_pipelines_dir] if config.satdump_pipelines_dir else None
    return Station(
        rotator=G550Rotator(
            port=config.rotator_serial_port,
            baud_rate=config.rotator_baud_rate,
            park_azimuth=config.rotator_park_azimuth,
            park_elevation=config.rotator_park_elevation,
        ),
        sdr=RTLSDRProbe(
            rtl_test_binary=config.rtl_test_binary,
            device_index=config.sdr_device_index,
        ),
        satdump=SatDump(binary=config.satdump_binary, pipeline_dirs=pipeline_dirs),
    )


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(prog="aagasa-hardware", description="Aagasa station hardware checks")
    subcommands = parser.add_subparsers(dest="command", required=True)
    subcommands.add_parser("status", help="report hardware availability")
    subcommands.add_parser("pipelines", help="list installed SatDump pipelines")
    subcommands.add_parser("park", help="drive the rotator to its safe position")
    move = subcommands.add_parser("move", help="drive the rotator to a position")
    move.add_argument("azimuth", type=int)
    move.add_argument("elevation", type=int)

    args = parser.parse_args(argv)
    logging.basicConfig(level=logging.INFO, format="%(levelname)s %(message)s")

    try:
        config = load_config()
    except ValueError as error:
        print(f"configuration error: {error}", file=sys.stderr)
        return 1

    station = build_station(config)

    try:
        if args.command == "status":
            return _print_status(station)
        if args.command == "pipelines":
            return _print_pipelines(station)
        if args.command == "park":
            azimuth, elevation = station.rotator.park()
            print(f"rotator parked at az={azimuth} el={elevation}")
            return 0
        if args.command == "move":
            azimuth, elevation = station.rotator.move_to(args.azimuth, args.elevation)
            print(f"rotator commanded to az={azimuth} el={elevation}")
            return 0
    except HardwareError as error:
        print(f"hardware error: {error}", file=sys.stderr)
        return 1

    return 2


def _print_status(station: Station) -> int:
    status = station.probe()
    print(f"rotator:  {_state(status.rotator_available, status.rotator_error)}")
    print(f"sdr:      {_state(status.sdr_available, status.sdr_error, status.sdr_description)}")
    print(f"satdump:  {_state(status.satdump_available, status.satdump_error, status.satdump_path)}")
    print(f"pipelines: {status.pipeline_count}")
    print(f"capture active: {status.capture_active}")
    # Exit non-zero if anything is missing, so a deployment check can use it.
    ready = status.rotator_available and status.sdr_available and status.satdump_available
    return 0 if ready else 1


def _print_pipelines(station: Station) -> int:
    pipelines = station.satdump.pipelines()
    if not pipelines:
        print("no pipelines found", file=sys.stderr)
        return 1
    for pipeline in pipelines:
        marker = "live" if pipeline.supports_live else "    "
        print(f"{marker}  {pipeline.identifier:<28} {pipeline.name}")
    print(f"total: {len(pipelines)}")
    return 0


def _state(available: bool, error: str, detail: str = "") -> str:
    if available:
        return f"OK {detail}".strip()
    return f"UNAVAILABLE ({error})"


if __name__ == "__main__":
    sys.exit(main())
