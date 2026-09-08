"""Pass prediction against known TLE and station combinations.

The element sets are real, captured from CelesTrak on 2026-08-24. The station
is the Dhritvan ground station used by the reference implementation.
"""

from datetime import datetime, timedelta, timezone

import pytest

from aagasa_prediction.predictor import (
    DEFAULT_MAX_PASSES,
    MAX_TRACK_POINTS,
    PredictionError,
    build_satellite,
    predict_passes,
)

ISS_LINE1 = "1 25544U 98067A   26236.17729445  .00008773  00000+0  16369-3 0  9990"
ISS_LINE2 = "2 25544  51.6333 323.5788 0007697  77.8713 282.3138 15.49600847582298"

NOAA19_LINE1 = "1 33591U 09005A   26236.28857781  .00000013  00000+0  30729-4 0  9998"
NOAA19_LINE2 = "2 33591  98.9473 307.1961 0013555 193.3958 166.6856 14.13483201904106"

# SJCIT, Karnataka.
STATION = dict(latitude_degrees=13.394944, longitude_degrees=77.729444, altitude_m=915)

WINDOW_START = datetime(2026, 8, 24, tzinfo=timezone.utc)
WINDOW_END = datetime(2026, 8, 26, tzinfo=timezone.utc)

MIN_ELEVATION = 10.0


def predict(**overrides):
    arguments = dict(
        line1=ISS_LINE1, line2=ISS_LINE2, **STATION,
        minimum_elevation_degrees=MIN_ELEVATION,
        search_start=WINDOW_START, search_end=WINDOW_END,
    )
    arguments.update(overrides)
    return predict_passes(**arguments)


def test_reports_the_catalog_number_from_the_elements():
    norad_id, _ = predict()
    assert norad_id == 25544


def test_finds_passes_for_a_known_satellite_and_station():
    _, passes = predict()

    # The ISS makes several visible passes over this station in two days.
    assert 2 <= len(passes) <= 12


# The same inputs must always produce the same output: scheduling decisions
# are built on these numbers.
def test_predictions_are_deterministic():
    first = predict()[1]
    second = predict()[1]

    assert len(first) == len(second)
    for left, right in zip(first, second):
        assert left.aos == right.aos
        assert left.los == right.los
        assert left.max_elevation_degrees == right.max_elevation_degrees


def test_pass_times_are_ordered_and_consistent():
    _, passes = predict()

    previous_los = None
    for satellite_pass in passes:
        assert satellite_pass.aos < satellite_pass.tca < satellite_pass.los
        assert satellite_pass.aos >= WINDOW_START
        assert satellite_pass.los <= WINDOW_END

        expected = (satellite_pass.los - satellite_pass.aos).total_seconds()
        assert satellite_pass.duration_seconds == pytest.approx(expected)

        # Passes come back in chronological order and cannot overlap.
        if previous_los is not None:
            assert satellite_pass.aos > previous_los
        previous_los = satellite_pass.los


def test_geometry_is_physically_plausible():
    _, passes = predict()

    for satellite_pass in passes:
        # A low-Earth-orbit pass is minutes, not hours.
        assert 0 < satellite_pass.duration_seconds < 1800
        # The peak must clear the threshold and stay below the zenith.
        assert MIN_ELEVATION <= satellite_pass.max_elevation_degrees <= 90
        for azimuth in (satellite_pass.aos_azimuth_degrees,
                        satellite_pass.tca_azimuth_degrees,
                        satellite_pass.los_azimuth_degrees):
            assert 0 <= azimuth <= 360


# AOS and LOS are defined by the configured threshold, so the satellite should
# sit essentially on it at both ends.
def test_pass_boundaries_sit_on_the_minimum_elevation():
    _, passes = predict(track_step_seconds=10)

    for satellite_pass in passes:
        assert satellite_pass.track[0].elevation_degrees == pytest.approx(MIN_ELEVATION, abs=0.1)
        assert satellite_pass.track[-1].elevation_degrees == pytest.approx(MIN_ELEVATION, abs=0.1)


def test_raising_the_threshold_yields_fewer_shorter_passes():
    _, low = predict(minimum_elevation_degrees=5.0)
    _, high = predict(minimum_elevation_degrees=40.0)

    assert len(high) <= len(low)
    for satellite_pass in high:
        assert satellite_pass.max_elevation_degrees >= 40.0


def test_no_passes_is_a_valid_answer():
    # A near-polar orbiter is not visible from an equatorial station at a very
    # high elevation threshold within a short window.
    _, passes = predict(
        line1=NOAA19_LINE1, line2=NOAA19_LINE2,
        minimum_elevation_degrees=85.0,
        search_end=WINDOW_START + timedelta(hours=6),
    )
    assert passes == []


def test_works_for_a_second_satellite():
    norad_id, passes = predict(line1=NOAA19_LINE1, line2=NOAA19_LINE2)

    assert norad_id == 33591
    assert len(passes) >= 1


def test_southern_hemisphere_station():
    _, passes = predict(latitude_degrees=-33.87, longitude_degrees=151.21, altitude_m=58)

    assert len(passes) >= 1
    for satellite_pass in passes:
        assert satellite_pass.max_elevation_degrees >= MIN_ELEVATION


# Track --------------------------------------------------------------------

def test_track_is_omitted_unless_requested():
    _, passes = predict()
    assert all(satellite_pass.track == [] for satellite_pass in passes)


def test_track_spans_the_whole_pass_at_the_requested_spacing():
    _, passes = predict(track_step_seconds=30)
    satellite_pass = passes[0]

    assert satellite_pass.track[0].at == satellite_pass.aos
    # The last sample is pinned to LOS so the Worker has an explicit end.
    assert satellite_pass.track[-1].at == pytest.approx(
        satellite_pass.los, abs=timedelta(seconds=1))

    for earlier, later in zip(satellite_pass.track, satellite_pass.track[1:]):
        gap = (later.at - earlier.at).total_seconds()
        assert 0 < gap <= 30 + 1e-6


def test_track_elevation_peaks_near_the_reported_maximum():
    _, passes = predict(track_step_seconds=5)

    for satellite_pass in passes:
        peak = max(point.elevation_degrees for point in satellite_pass.track)
        assert peak == pytest.approx(satellite_pass.max_elevation_degrees, abs=0.5)


def test_track_range_is_plausible_for_low_earth_orbit():
    _, passes = predict(track_step_seconds=15)

    for point in passes[0].track:
        # Slant range from the horizon to overhead for a ~420 km orbit.
        assert 300 < point.range_km < 3000


def test_finer_spacing_produces_more_points():
    _, coarse = predict(track_step_seconds=60)
    _, fine = predict(track_step_seconds=10)

    assert len(fine[0].track) > len(coarse[0].track)


def test_a_one_second_track_over_a_normal_pass_is_allowed():
    _, passes = predict(track_step_seconds=1, max_passes=1)

    # A LEO pass is a few hundred seconds, well inside the point budget.
    assert 0 < len(passes[0].track) <= MAX_TRACK_POINTS


def test_a_track_that_would_be_unbounded_is_refused():
    """Guard the point budget directly.

    A long pass at a fine step, as a high orbit would produce, must be
    refused rather than allocating millions of points.
    """
    from skyfield.api import load, wgs84

    from aagasa_prediction.predictor import _build_track

    timescale = load.timescale()
    satellite = build_satellite(ISS_LINE1, ISS_LINE2)
    difference = satellite - wgs84.latlon(**{
        "latitude_degrees": STATION["latitude_degrees"],
        "longitude_degrees": STATION["longitude_degrees"],
        "elevation_m": STATION["altitude_m"],
    })

    rise = timescale.utc(2026, 8, 24, 0, 0, 0)
    # A ten-hour window at one-second spacing is far past the budget.
    setting = timescale.utc(2026, 8, 24, 10, 0, 0)

    with pytest.raises(PredictionError, match=str(MAX_TRACK_POINTS)):
        _build_track(difference, rise, setting, 1)


# Limits and validation ----------------------------------------------------

def test_max_passes_caps_the_result():
    _, passes = predict(max_passes=2)
    assert len(passes) == 2


def test_zero_max_passes_applies_the_default():
    _, passes = predict(max_passes=0)
    assert len(passes) <= DEFAULT_MAX_PASSES


@pytest.mark.parametrize("overrides,message", [
    (dict(latitude_degrees=91), "latitude"),
    (dict(latitude_degrees=-91), "latitude"),
    (dict(longitude_degrees=181), "longitude"),
    (dict(minimum_elevation_degrees=90), "minimum_elevation_degrees"),
    (dict(minimum_elevation_degrees=-1), "minimum_elevation_degrees"),
    (dict(search_end=WINDOW_START), "search_end must be after"),
    (dict(search_end=WINDOW_START - timedelta(days=1)), "search_end must be after"),
    (dict(search_end=WINDOW_START + timedelta(days=60)), "30 days"),
    (dict(search_start=None), "required"),
])
def test_invalid_requests_are_rejected_with_a_clear_message(overrides, message):
    with pytest.raises(PredictionError, match=message):
        predict(**overrides)


@pytest.mark.parametrize("line1,line2", [
    ("", ISS_LINE2),
    (ISS_LINE1, ""),
    ("not a tle", "nor is this"),
    ("1 99999U 00000A   26236.17729445  .00008773  00000+0  16369-3 0  9990",
     "2 99999 999.9999 999.9999 9999999 999.9999 999.9999 99.99999999999999"),
])
def test_unusable_elements_are_rejected(line1, line2):
    # Every rejection must be a PredictionError, never a leaked AttributeError
    # or a satellite that silently produces nonsense.
    with pytest.raises(PredictionError):
        build_satellite(line1, line2)


def test_a_bad_checksum_is_not_this_layer_s_job():
    """sgp4 does not verify TLE checksums, and neither does this service.

    Validation happens once on ingest, in the Server's Go tle package.
    Duplicating it here would risk the two implementations drifting apart.
    """
    bad_checksum = ISS_LINE1[:-1] + "9"
    assert bad_checksum != ISS_LINE1

    satellite = build_satellite(bad_checksum, ISS_LINE2)
    assert satellite.model.satnum == 25544
