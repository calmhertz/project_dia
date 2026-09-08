"""Worker identity, resolved from the Server and cached locally.

The Worker is configured with a name; the Server owns the identifiers
(spec.md section 7). The resolved identity is cached in the durable store so a
Worker that restarts while the Server is unreachable still knows who it is and
can keep executing.
"""

import logging
from dataclasses import dataclass

logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class Identity:
    worker_id: str
    station_id: str
    station_name: str

    @property
    def resolved(self) -> bool:
        return bool(self.worker_id and self.station_id)


UNRESOLVED = Identity(worker_id="", station_id="", station_name="")


class IdentityResolver:
    """Resolves and caches the Worker's identity."""

    def __init__(self, client, store, worker_name: str, pipelines=None):
        self._client = client
        self._store = store
        self._worker_name = worker_name
        # Callable rather than a list: SatDump can be reinstalled while the
        # Worker runs, and registration happens again after every outage.
        self._pipelines = pipelines or (lambda: [])
        self._identity = store.load_identity() or UNRESOLVED
        if self._identity.resolved:
            logger.info("using cached identity worker_id=%s station=%s",
                        self._identity.worker_id, self._identity.station_name or "<unknown>")

    @property
    def current(self) -> Identity:
        return self._identity

    def resolve(self) -> Identity:
        """Register with the Server, caching the result.

        Raises grpc.RpcError when the Server is unreachable; the caller falls
        back to whatever was cached.
        """
        response = self._client.register(self._worker_name, self._pipelines())
        identity = Identity(
            worker_id=response.worker_id,
            station_id=response.station_id,
            station_name=response.station_name,
        )
        if identity != self._identity:
            self._store.save_identity(identity)
            logger.info("identity resolved worker_id=%s station=%s",
                        identity.worker_id, identity.station_name)
        self._identity = identity
        return identity
