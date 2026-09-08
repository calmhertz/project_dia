"""PredictionService gRPC implementation."""

import logging
from datetime import timezone

import grpc
from google.protobuf.timestamp_pb2 import Timestamp

from aagasa.prediction.v1 import prediction_pb2, prediction_pb2_grpc

from aagasa_prediction import __version__
from aagasa_prediction.predictor import PredictionError, predict_passes

logger = logging.getLogger(__name__)


class PredictionService(prediction_pb2_grpc.PredictionServiceServicer):
    def Health(self, request, context):  # noqa: N802 - name fixed by protobuf
        return prediction_pb2.HealthResponse(
            status=prediction_pb2.HealthResponse.STATUS_SERVING,
            version=__version__,
        )

    def PredictPasses(self, request, context):  # noqa: N802 - name fixed by protobuf
        try:
            norad_id, passes = predict_passes(
                line1=request.tle.line1,
                line2=request.tle.line2,
                latitude_degrees=request.observer.latitude_degrees,
                longitude_degrees=request.observer.longitude_degrees,
                altitude_m=request.observer.altitude_m,
                minimum_elevation_degrees=request.minimum_elevation_degrees,
                search_start=_to_datetime(request.search_start),
                search_end=_to_datetime(request.search_end),
                track_step_seconds=request.track_step_seconds,
                max_passes=request.max_passes,
            )
        except PredictionError as error:
            # A bad request is the caller's to fix, so say so precisely
            # rather than returning an opaque internal error.
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, str(error))
            return prediction_pb2.PredictPassesResponse()
        except Exception as error:  # noqa: BLE001 - must not leak a traceback
            logger.exception("pass prediction failed")
            context.abort(grpc.StatusCode.INTERNAL, f"prediction failed: {type(error).__name__}")
            return prediction_pb2.PredictPassesResponse()

        return prediction_pb2.PredictPassesResponse(
            norad_id=norad_id,
            passes=[_to_proto(predicted) for predicted in passes],
        )


def _to_proto(predicted) -> prediction_pb2.PredictedPass:
    return prediction_pb2.PredictedPass(
        aos=_to_timestamp(predicted.aos),
        tca=_to_timestamp(predicted.tca),
        los=_to_timestamp(predicted.los),
        aos_azimuth_degrees=predicted.aos_azimuth_degrees,
        tca_azimuth_degrees=predicted.tca_azimuth_degrees,
        los_azimuth_degrees=predicted.los_azimuth_degrees,
        max_elevation_degrees=predicted.max_elevation_degrees,
        duration_seconds=predicted.duration_seconds,
        track=[
            prediction_pb2.TrackPoint(
                at=_to_timestamp(point.at),
                azimuth_degrees=point.azimuth_degrees,
                elevation_degrees=point.elevation_degrees,
                range_km=point.range_km,
            )
            for point in predicted.track
        ],
    )


def _to_timestamp(moment) -> Timestamp:
    timestamp = Timestamp()
    timestamp.FromDatetime(moment)
    return timestamp


def _to_datetime(timestamp):
    """Convert a protobuf timestamp, treating an unset field as absent."""
    if timestamp is None or (timestamp.seconds == 0 and timestamp.nanos == 0):
        return None
    return timestamp.ToDatetime(tzinfo=timezone.utc)
