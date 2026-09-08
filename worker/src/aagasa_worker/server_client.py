"""gRPC client for the Server connection.

V1 provides connectivity only. Desired-state synchronization, PassPlan delivery
and result reporting arrive in later phases.
"""

import logging

import grpc

from google.protobuf.timestamp_pb2 import Timestamp

from aagasa.worker.v1 import recording_pb2, worker_pb2, worker_pb2_grpc

from aagasa_worker import __version__
from aagasa_worker.config import Config
from aagasa_worker.recordings import read_chunks

logger = logging.getLogger(__name__)

CALL_TIMEOUT_SECONDS = 10
# A plan set is larger than a ping, so it gets longer.
SYNC_TIMEOUT_SECONDS = 60
# A recording can be large and the link can be slow.
UPLOAD_TIMEOUT_SECONDS = 3600


class ServerClient:
    """Talks to the Aagasa Server over gRPC."""

    def __init__(self, config: Config):
        self._config = config
        # Set once registration resolves it; empty until then.
        self.identity = None
        if config.server_tls_enabled:
            credentials = grpc.ssl_channel_credentials()
            self._channel = grpc.secure_channel(config.server_grpc_address, credentials)
        else:
            # Only for local development; deployments set TLS enabled.
            logger.warning("server connection is not using TLS")
            self._channel = grpc.insecure_channel(config.server_grpc_address)
        self._stub = worker_pb2_grpc.WorkerServiceStub(self._channel)

    def _metadata(self):
        # The credential is sent per call and must never be logged.
        return (("authorization", f"Bearer {self._config.server_shared_secret}"),)

    def register(self, worker_name: str, available_pipelines=()):
        """Resolve this Worker's identity from its configured name.

        The pipeline inventory travels with registration: the Server has no
        SatDump of its own, so this is the only honest source for what the
        Client may offer (server-spec section 21).
        """
        return self._stub.Register(
            worker_pb2.RegisterRequest(
                worker_name=worker_name, worker_version=__version__,
                available_pipelines=list(available_pipelines)),
            timeout=CALL_TIMEOUT_SECONDS,
            metadata=self._metadata(),
        )

    def heartbeat(self, state_generation: str, pending_uploads: int,
                  pending_reports: int, current_pass_id: str = ""):
        """Report liveness and what this Worker is carrying."""
        return self._stub.Heartbeat(
            worker_pb2.HeartbeatRequest(
                worker_id=self._worker_id(),
                worker_version=__version__,
                state_generation=state_generation,
                pending_uploads=pending_uploads,
                pending_reports=pending_reports,
                current_pass_id=current_pass_id,
            ),
            timeout=CALL_TIMEOUT_SECONDS,
            metadata=self._metadata(),
        )

    def _worker_id(self) -> str:
        return self.identity.worker_id if self.identity else ""

    def _station_id(self) -> str:
        return self.identity.station_id if self.identity else ""

    def ping(self) -> worker_pb2.PingResponse:
        """Check Server connectivity. Raises grpc.RpcError when unreachable."""
        request = worker_pb2.PingRequest(
            worker_id=self._worker_id(),
            station_id=self._station_id(),
            worker_version=__version__,
        )
        return self._stub.Ping(
            request,
            timeout=CALL_TIMEOUT_SECONDS,
            metadata=self._metadata(),
        )

    def sync_pass_plans(self, current_generation: str):
        """Fetch the desired execution state.

        Raises grpc.RpcError when the Server is unreachable; the caller keeps
        running on the state it already holds.
        """
        request = worker_pb2.SyncPassPlansRequest(
            worker_id=self._worker_id(),
            station_id=self._station_id(),
            current_generation=current_generation,
        )
        return self._stub.SyncPassPlans(
            request,
            timeout=SYNC_TIMEOUT_SECONDS,
            metadata=self._metadata(),
        )

    def upload_recording(self, recording, path):
        """Send one recording as a single streamed RPC.

        Metadata first, then the content in chunks. One RPC carries one whole
        file; this is not a resumable protocol (spec.md section 19.3).
        """
        def messages():
            yield recording_pb2.UploadRecordingRequest(
                metadata=recording_pb2.RecordingMetadata(
                    recording_id=recording.recording_id,
                    pass_id=recording.pass_id,
                    worker_id=self._worker_id(),
                    station_id=self._station_id(),
                    relative_path=recording.relative_path,
                    size_bytes=recording.size_bytes,
                    checksum_sha256=recording.checksum,
                    finished_at=_timestamp(recording.finished_at),
                )
            )
            for chunk in read_chunks(path):
                yield recording_pb2.UploadRecordingRequest(chunk=chunk)

        return self._stub.UploadRecording(
            messages(),
            timeout=UPLOAD_TIMEOUT_SECONDS,
            metadata=self._metadata(),
        )

    def report_executions(self, entries):
        """Report pass outcomes gathered locally."""
        request = recording_pb2.ReportExecutionsRequest(
            worker_id=self._worker_id(),
            records=[
                recording_pb2.ExecutionRecord(
                    pass_id=entry.pass_id,
                    state=entry.state,
                    detail=entry.detail,
                    recorded_at=_timestamp(entry.recorded_at),
                )
                for entry in entries
            ],
        )
        return self._stub.ReportExecutions(
            request,
            timeout=CALL_TIMEOUT_SECONDS,
            metadata=self._metadata(),
        )

    def close(self) -> None:
        self._channel.close()


def _timestamp(moment):
    """Convert a datetime to a protobuf timestamp, tolerating None."""
    stamp = Timestamp()
    if moment is not None:
        stamp.FromDatetime(moment)
    return stamp
