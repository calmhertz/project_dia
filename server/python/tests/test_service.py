"""Health contract tests for the prediction service."""

import grpc
import pytest

from aagasa.prediction.v1 import prediction_pb2, prediction_pb2_grpc

from aagasa_prediction import __version__
from aagasa_prediction.config import Config
from aagasa_prediction.main import build_server


@pytest.fixture
def server_address():
    config = Config(address="127.0.0.1:0", log_level="INFO", max_workers=2)
    server = build_server(config)
    # add_insecure_port on port 0 returns the port actually bound.
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    try:
        yield f"127.0.0.1:{port}"
    finally:
        server.stop(None)


def test_health_reports_serving(server_address):
    with grpc.insecure_channel(server_address) as channel:
        client = prediction_pb2_grpc.PredictionServiceStub(channel)
        response = client.Health(prediction_pb2.HealthRequest(), timeout=5)

    assert response.status == prediction_pb2.HealthResponse.STATUS_SERVING
    assert response.version == __version__


# PredictPasses over real gRPC ---------------------------------------------

def _timestamp(moment):
    from google.protobuf.timestamp_pb2 import Timestamp
    stamp = Timestamp()
    stamp.FromDatetime(moment)
    return stamp


def _request(**overrides):
    from datetime import datetime, timezone

    request = prediction_pb2.PredictPassesRequest(
        tle=prediction_pb2.TwoLineElements(
            line1="1 25544U 98067A   26236.17729445  .00008773  00000+0  16369-3 0  9990",
            line2="2 25544  51.6333 323.5788 0007697  77.8713 282.3138 15.49600847582298",
        ),
        observer=prediction_pb2.ObserverLocation(
            latitude_degrees=13.394944, longitude_degrees=77.729444, altitude_m=915),
        minimum_elevation_degrees=10.0,
        search_start=_timestamp(datetime(2026, 8, 24, tzinfo=timezone.utc)),
        search_end=_timestamp(datetime(2026, 8, 26, tzinfo=timezone.utc)),
        **overrides,
    )
    return request


def test_predict_passes_over_grpc(server_address):
    with grpc.insecure_channel(server_address) as channel:
        client = prediction_pb2_grpc.PredictionServiceStub(channel)
        response = client.PredictPasses(_request(track_step_seconds=30), timeout=30)

    assert response.norad_id == 25544
    assert len(response.passes) >= 2

    first = response.passes[0]
    assert first.aos.seconds < first.tca.seconds < first.los.seconds
    assert first.max_elevation_degrees >= 10.0
    assert len(first.track) > 0
    assert first.track[0].range_km > 0


def test_predict_passes_without_a_track(server_address):
    with grpc.insecure_channel(server_address) as channel:
        client = prediction_pb2_grpc.PredictionServiceStub(channel)
        response = client.PredictPasses(_request(), timeout=30)

    assert all(len(satellite_pass.track) == 0 for satellite_pass in response.passes)


# A bad request must be INVALID_ARGUMENT, not an opaque internal error.
def test_invalid_request_is_reported_as_invalid_argument(server_address):
    bad = _request()
    bad.observer.latitude_degrees = 120.0

    with grpc.insecure_channel(server_address) as channel:
        client = prediction_pb2_grpc.PredictionServiceStub(channel)
        with pytest.raises(grpc.RpcError) as caught:
            client.PredictPasses(bad, timeout=30)

    assert caught.value.code() == grpc.StatusCode.INVALID_ARGUMENT
    assert "latitude" in caught.value.details()


def test_unusable_tle_is_reported_as_invalid_argument(server_address):
    bad = _request()
    bad.tle.line1 = "not a tle"
    bad.tle.line2 = "nor is this"

    with grpc.insecure_channel(server_address) as channel:
        client = prediction_pb2_grpc.PredictionServiceStub(channel)
        with pytest.raises(grpc.RpcError) as caught:
            client.PredictPasses(bad, timeout=30)

    assert caught.value.code() == grpc.StatusCode.INVALID_ARGUMENT


# An error must never leak a Python traceback to the caller.
def test_errors_do_not_leak_internals(server_address):
    bad = _request()
    bad.observer.longitude_degrees = 999.0

    with grpc.insecure_channel(server_address) as channel:
        client = prediction_pb2_grpc.PredictionServiceStub(channel)
        with pytest.raises(grpc.RpcError) as caught:
            client.PredictPasses(bad, timeout=30)

    details = caught.value.details()
    assert "Traceback" not in details
    assert "aagasa_prediction/" not in details
