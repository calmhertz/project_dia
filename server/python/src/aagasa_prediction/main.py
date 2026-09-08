"""Entrypoint for the Aagasa prediction service."""

import logging
import signal
import sys
from concurrent import futures

import grpc

from aagasa.prediction.v1 import prediction_pb2_grpc

from aagasa_prediction import __version__
from aagasa_prediction.config import Config, load_config
from aagasa_prediction.service import PredictionService

logger = logging.getLogger("aagasa_prediction")

SHUTDOWN_GRACE_SECONDS = 10


def build_server(config: Config) -> grpc.Server:
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=config.max_workers))
    prediction_pb2_grpc.add_PredictionServiceServicer_to_server(PredictionService(), server)
    server.add_insecure_port(config.address)
    return server


def main() -> int:
    try:
        config = load_config()
    except ValueError as exc:
        print(f"configuration error: {exc}", file=sys.stderr)
        return 1

    logging.basicConfig(
        level=getattr(logging, config.log_level, logging.INFO),
        format="%(asctime)s %(levelname)s %(name)s %(message)s",
    )

    server = build_server(config)
    server.start()
    logger.info("prediction service listening on %s version %s", config.address, __version__)

    received = {}

    # A signal handler must not log: it can interrupt the main thread while it
    # holds the stream lock, which raises a reentrant-call error.
    def handle_signal(signum, _frame):
        received.setdefault("signal", signum)
        server.stop(SHUTDOWN_GRACE_SECONDS)

    signal.signal(signal.SIGINT, handle_signal)
    signal.signal(signal.SIGTERM, handle_signal)

    server.wait_for_termination()
    logger.info("prediction service stopped signal=%s", received.get("signal"))
    return 0


if __name__ == "__main__":
    sys.exit(main())
