"""Satellite pass prediction using Skyfield and SGP4.

This is the only pass predictor in Aagasa. The Server calls it over gRPC and
does not compute passes itself; the Worker executes the timeline produced here
rather than inventing a schedule (spec.md section 12).

Predictions are deterministic: the same elements, observer, threshold and time
range always produce the same result. Skyfield's builtin leap-second data is
used, so no network access is required at runtime.
"""

import logging
from dataclasses import dataclass, field
from datetime import datetime, timezone

from skyfield.api import EarthSatellite, load, wgs84

logger = logging.getLogger(__name__)

# Skyfield's find_events labels: 0 rise, 1 culminate, 2 set.
EVENT_RISE = 0
EVENT_CULMINATE = 1
EVENT_SET = 2

MAX_SEARCH_DAYS = 30
DEFAULT_MAX_PASSES = 100
MIN_TRACK_STEP_SECONDS = 1
MAX_TRACK_POINTS = 5000


class PredictionError(ValueError):
    """The request cannot be satisfied as given.

    Distinct from an internal failure: this maps to an invalid-argument
    response so the caller can correct the request.
    """


@dataclass(frozen=True)
class TrackPoint:
    at: datetime
    azimuth_degrees: float
    elevation_degrees: float
    range_km: float


@dataclass(frozen=True)
class Pass:
    aos: datetime
    tca: datetime
    los: datetime
    aos_azimuth_degrees: float
    tca_azimuth_degrees: float
    los_azimuth_degrees: float
    max_elevation_degrees: float
    duration_seconds: float
    track: list[TrackPoint] = field(default_factory=list)


# One shared timescale: building it per request is wasteful and the builtin
# data does not change at runtime.
_timescale = load.timescale()


def build_satellite(line1: str, line2: str) -> EarthSatellite:
    """Construct a satellite from element lines, reporting bad input clearly."""
    if not line1 or not line2:
        raise PredictionError("both TLE lines are required")
    try:
        satellite = EarthSatellite(line1, line2, "", _timescale)
    except Exception as error:  # noqa: BLE001 - sgp4 raises assorted types
        raise PredictionError(f"invalid TLE: {error}") from error

    # sgp4 reports element-set problems through a numeric error code rather
    # than an exception, so a silently unusable satellite must be caught here.
    # error_message is not present on every sgp4 build, hence the getattr.
    code = getattr(satellite.model, "error", 0)
    if code:
        detail = getattr(satellite.model, "error_message", "") or ""
        raise PredictionError(f"invalid TLE: sgp4 error {code} {detail}".strip())

    # Note: sgp4 does not verify the TLE checksum, and neither does this
    # service. Checksum validation happens once, on ingest, in the Server's
    # Go tle package; duplicating it here would risk the two drifting apart.
    return satellite


def predict_passes(
    line1: str,
    line2: str,
    latitude_degrees: float,
    longitude_degrees: float,
    altitude_m: float,
    minimum_elevation_degrees: float,
    search_start: datetime,
    search_end: datetime,
    track_step_seconds: int = 0,
    max_passes: int = DEFAULT_MAX_PASSES,
) -> tuple[int, list[Pass]]:
    """Compute visible passes, returning the catalog number and the passes."""
    _validate(latitude_degrees, longitude_degrees, minimum_elevation_degrees,
              search_start, search_end, track_step_seconds)

    satellite = build_satellite(line1, line2)
    observer = wgs84.latlon(latitude_degrees, longitude_degrees, elevation_m=altitude_m)
    difference = satellite - observer

    start = _to_skyfield(search_start)
    end = _to_skyfield(search_end)

    times, events = satellite.find_events(
        observer, start, end, altitude_degrees=minimum_elevation_degrees
    )

    limit = max_passes if max_passes > 0 else DEFAULT_MAX_PASSES
    passes: list[Pass] = []

    # find_events emits rise/culminate/set in order, but a search window can
    # start mid-pass or end mid-pass, so only complete triples are usable.
    for index in range(len(events) - 2):
        if events[index] != EVENT_RISE:
            continue
        if events[index + 1] != EVENT_CULMINATE or events[index + 2] != EVENT_SET:
            continue

        rise_time, culminate_time, set_time = times[index], times[index + 1], times[index + 2]

        rise_elevation, rise_azimuth, _ = difference.at(rise_time).altaz()
        peak_elevation, peak_azimuth, _ = difference.at(culminate_time).altaz()
        set_elevation, set_azimuth, _ = difference.at(set_time).altaz()

        aos = _to_datetime(rise_time)
        tca = _to_datetime(culminate_time)
        los = _to_datetime(set_time)

        track = []
        if track_step_seconds > 0:
            track = _build_track(difference, rise_time, set_time, track_step_seconds)

        passes.append(Pass(
            aos=aos, tca=tca, los=los,
            aos_azimuth_degrees=float(rise_azimuth.degrees),
            tca_azimuth_degrees=float(peak_azimuth.degrees),
            los_azimuth_degrees=float(set_azimuth.degrees),
            max_elevation_degrees=float(peak_elevation.degrees),
            duration_seconds=(los - aos).total_seconds(),
            track=track,
        ))

        if len(passes) >= limit:
            break

    logger.debug("predicted %d passes for %d", len(passes), satellite.model.satnum)
    return int(satellite.model.satnum), passes


def _build_track(difference, rise_time, set_time, step_seconds: int) -> list[TrackPoint]:
    """Sample the pointing timeline from AOS to LOS inclusive.

    The final sample is pinned to LOS so the Worker always has an explicit end
    point rather than stopping short of it.
    """
    duration = (set_time.tt - rise_time.tt) * 86400.0
    count = int(duration // step_seconds)
    # Cap the sample count so an absurd request cannot exhaust memory.
    if count > MAX_TRACK_POINTS:
        raise PredictionError(
            f"track_step_seconds {step_seconds} would produce more than {MAX_TRACK_POINTS} points"
        )

    offsets = [index * step_seconds for index in range(count + 1)]
    if not offsets or offsets[-1] < duration:
        offsets.append(duration)

    day = 1.0 / 86400.0
    times = _timescale.tt_jd([rise_time.tt + offset * day for offset in offsets])
    elevations, azimuths, distances = difference.at(times).altaz()

    return [
        TrackPoint(
            at=_to_datetime(times[index]),
            azimuth_degrees=float(azimuths.degrees[index]),
            elevation_degrees=float(elevations.degrees[index]),
            range_km=float(distances.km[index]),
        )
        for index in range(len(offsets))
    ]


def _validate(latitude, longitude, minimum_elevation, search_start, search_end, track_step):
    if not -90 <= latitude <= 90:
        raise PredictionError("latitude must be between -90 and 90")
    if not -180 <= longitude <= 180:
        raise PredictionError("longitude must be between -180 and 180")
    if not 0 <= minimum_elevation < 90:
        raise PredictionError("minimum_elevation_degrees must be at least 0 and below 90")
    if search_start is None or search_end is None:
        raise PredictionError("search_start and search_end are required")
    if search_end <= search_start:
        raise PredictionError("search_end must be after search_start")

    span_days = (search_end - search_start).total_seconds() / 86400.0
    # A very wide search is almost always a mistake, and it is expensive.
    if span_days > MAX_SEARCH_DAYS:
        raise PredictionError(f"search range must not exceed {MAX_SEARCH_DAYS} days")
    if track_step and track_step < MIN_TRACK_STEP_SECONDS:
        raise PredictionError("track_step_seconds must be at least 1")


def _to_skyfield(moment: datetime):
    """Convert to Skyfield time, treating a naive value as UTC."""
    if moment.tzinfo is None:
        moment = moment.replace(tzinfo=timezone.utc)
    return _timescale.from_datetime(moment.astimezone(timezone.utc))


def _to_datetime(moment) -> datetime:
    return moment.utc_datetime().replace(tzinfo=timezone.utc)
