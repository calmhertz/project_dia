"""Worker identity resolution.

The Worker is configured with a name; the Server owns the identifiers and
resolves them at registration (spec.md section 7). A cached identity is what
lets a Worker restarted during an outage keep executing.
"""

from aagasa_worker.identity import Identity, IdentityResolver


class Response:
    def __init__(self, worker_id, station_id, station_name):
        self.worker_id = worker_id
        self.station_id = station_id
        self.station_name = station_name


class RecordingClient:
    """A Server that answers registration and remembers what it was told."""

    def __init__(self, response=None):
        self.response = response or Response(
            "worker-uuid", "station-uuid", "Test Station")
        self.registered_pipelines = None
        self.registrations = 0

    def register(self, worker_name, available_pipelines=()):
        self.registrations += 1
        self.registered_pipelines = list(available_pipelines)
        return self.response


class MemoryStore:
    def __init__(self, identity=None):
        self.identity = identity
        self.saves = 0

    def load_identity(self):
        return self.identity

    def save_identity(self, identity):
        self.identity = identity
        self.saves += 1


def test_registration_resolves_and_caches_the_identity():
    client = RecordingClient()
    store = MemoryStore()
    resolver = IdentityResolver(client, store, "worker-1")

    identity = resolver.resolve()

    assert identity.worker_id == "worker-uuid"
    assert identity.station_name == "Test Station"
    # Cached, so a restart during an outage still knows who it is.
    assert store.identity == identity
    assert store.saves == 1


def test_an_unchanged_identity_is_not_rewritten():
    cached = Identity(worker_id="worker-uuid", station_id="station-uuid",
                      station_name="Test Station")
    client = RecordingClient()
    store = MemoryStore(identity=cached)
    resolver = IdentityResolver(client, store, "worker-1")

    resolver.resolve()

    assert store.saves == 0




# The pipeline inventory -----------------------------------------------------
#
# The Server has no SatDump of its own, so registration is the only moment it
# learns what this station can run (server-spec section 21).

def test_registration_reports_the_pipeline_inventory():
    client = RecordingClient()
    store = MemoryStore()
    resolver = IdentityResolver(
        client, store, "worker-1",
        pipelines=lambda: ["noaa_apt", "meteor_m2-x_lrpt"])

    resolver.resolve()

    assert client.registered_pipelines == ["noaa_apt", "meteor_m2-x_lrpt"]


def test_the_inventory_is_read_fresh_each_time():
    """SatDump can be reinstalled between registrations."""
    client = RecordingClient()
    store = MemoryStore()
    installed = ["noaa_apt"]
    resolver = IdentityResolver(
        client, store, "worker-1", pipelines=lambda: list(installed))

    resolver.resolve()
    assert client.registered_pipelines == ["noaa_apt"]

    installed.append("meteor_m2-x_lrpt")
    resolver.resolve()
    assert client.registered_pipelines == ["noaa_apt", "meteor_m2-x_lrpt"]


def test_a_worker_without_satdump_still_registers():
    """A missing installation must not stop the Worker from having identity."""
    client = RecordingClient()
    store = MemoryStore()
    resolver = IdentityResolver(client, store, "worker-1", pipelines=lambda: [])

    identity = resolver.resolve()

    assert identity.resolved
    assert client.registered_pipelines == []
